package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// deadChatLauncher is a chat session whose controller has gone away: the
// session row is still there, the provider process is not. Every command
// answers with the sentinel the real chat service returns from its controller
// registry.
type deadChatLauncher struct{}

func (deadChatLauncher) PreflightChat(context.Context, domain.AgentHarness) error { return nil }

func (deadChatLauncher) StartChat(context.Context, sessionmanager.ChatStart) (sessionmanager.ChatStarted, error) {
	return sessionmanager.ChatStarted{}, chatsvc.ErrNoController
}

func (deadChatLauncher) StartChatTurn(context.Context, domain.SessionID, string) (string, error) {
	return "", chatsvc.ErrNoController
}

func (deadChatLauncher) RelayChatTurn(context.Context, domain.SessionID, string) (string, error) {
	return "", chatsvc.ErrNoController
}

func (deadChatLauncher) RelayChatTurnWithID(context.Context, domain.SessionID, string, string) (string, error) {
	return "", chatsvc.ErrNoController
}

func (deadChatLauncher) StopChat(context.Context, domain.SessionID) error { return nil }
func (deadChatLauncher) HasLiveChatController(domain.SessionID) bool      { return false }

// A dead chat controller must answer POST /sessions/{id}/send the way it
// already answers the conversation routes: 409 CHAT_CONTROLLER_NOT_READY.
//
// Live dogfood found it returning 500 INTERNAL_ERROR instead, with the real
// reason ("no live chat controller for session") only in the daemon log. The
// send path reaches the controller through Manager.send → sendChat, never
// through writeConversationError, so the sentinel had to be mapped in the
// session service — and only the real service maps anything, which is why this
// test wires the production Manager and store rather than fakeSessionService.
func TestSendToChatSessionWithDeadController_Returns409(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "plain", Path: t.TempDir(), RegisteredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	rec, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "plain",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Mode:      domain.SessionModeChat,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := sessionmanager.New(sessionmanager.Deps{Store: store, Chat: deadChatLauncher{}, Logger: log})
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Sessions:      sessionsvc.New(mgr, store),
		OperatorSpawn: allowOperatorSpawn{},
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/"+string(rec.ID)+"/send", `{"message":"ping"}`)
	assertErrorCode(t, body, status, http.StatusConflict, "CHAT_CONTROLLER_NOT_READY")
}
