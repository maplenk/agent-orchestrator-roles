package controllers_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestProjectsAPIContainsUnreadableStoredConfigAndFencesMutations(t *testing.T) {
	ctx := t.Context()
	dataDir := t.TempDir()
	store, err := sqlitetest.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	for _, row := range []domain.ProjectRecord{
		{ID: "broken", Path: "/repo/broken", DisplayName: "Broken", RegisteredAt: now},
		{ID: "healthy", Path: "/repo/healthy", DisplayName: "Healthy", RegisteredAt: now},
	} {
		if err := store.UpsertProject(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	rawConfig := `{"roleMap":{"role_map_schema_version":2,"orchestratorRole":"orchestrator","roles":{"orchestrator":{"template":"orchestrator","harness":"codex","permissions":{"workspaceWrites":true,"canSpawn":true}}}},"futureConfig":"preserve"}`
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, `UPDATE projects SET config = ? WHERE id = ?`, rawConfig, "broken"); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Projects: projectsvc.New(store),
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/projects", "")
	if status != http.StatusOK {
		t.Fatalf("list status=%d body=%s", status, body)
	}
	var list struct {
		Projects []struct {
			ID           string `json:"id"`
			ResolveError string `json:"resolveError"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Projects) != 2 || list.Projects[0].ID != "broken" || list.Projects[0].ResolveError != "Project configuration is unreadable" || list.Projects[1].ID != "healthy" {
		t.Fatalf("list = %#v, want broken degraded plus healthy", list.Projects)
	}

	body, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/projects/broken", "")
	if status != http.StatusOK || !json.Valid(body) {
		t.Fatalf("get status=%d body=%s", status, body)
	}
	var get struct {
		Status  string `json:"status"`
		Project struct {
			ID           string `json:"id"`
			ResolveError string `json:"resolveError"`
		} `json:"project"`
	}
	if err := json.Unmarshal(body, &get); err != nil {
		t.Fatal(err)
	}
	if get.Status != "degraded" || get.Project.ID != "broken" || get.Project.ResolveError != "Project configuration is unreadable" {
		t.Fatalf("get = %#v, want degraded unreadable", get)
	}

	mutations := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPut, "/api/v1/projects/broken/config", `{"config":{"defaultBranch":"develop"}}`},
		{http.MethodPut, "/api/v1/projects/broken", `{"displayName":"Changed","config":{"defaultBranch":"develop"}}`},
		{http.MethodDelete, "/api/v1/projects/broken", ""},
	}
	for _, mutation := range mutations {
		body, status, _ := doRequest(t, srv, mutation.method, mutation.path, mutation.body)
		assertErrorCode(t, body, status, http.StatusConflict, "PROJECT_CONFIG_UNREADABLE")
	}

	var persisted, displayName string
	var archived sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT config, display_name, archived_at FROM projects WHERE id = ?`, "broken").Scan(&persisted, &displayName, &archived); err != nil {
		t.Fatal(err)
	}
	if persisted != rawConfig || displayName != "Broken" || archived.Valid {
		t.Fatalf("broken row mutated: config=%q displayName=%q archived=%v", persisted, displayName, archived)
	}
}
