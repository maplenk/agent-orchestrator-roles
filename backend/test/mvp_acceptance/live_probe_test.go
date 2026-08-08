package mvpacceptance

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
)

// TestCapabilityMatrixMVP is record 12's executable assertion. AllDocumented
// is the registry used by validation; checking it avoids trusting a markdown
// mirror or a hand-picked list of popular harnesses.
func TestCapabilityMatrixMVP(t *testing.T) {
	for harness, caps := range capabilities.AllDocumented() {
		if caps.LimitDetectionSupported {
			t.Errorf("%s limit_detection_supported=true; MVP requires false", harness)
		}
	}
	if capabilities.For(domain.HarnessClaudeCode).ReadOnlyEnforced {
		t.Error("claude-code read_only_enforced=true; capability claim is unsupported")
	}
}

type muxFrame struct {
	Ch    string `json:"ch"`
	ID    string `json:"id,omitempty"`
	Type  string `json:"type"`
	Data  string `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// TestLiveMuxInputFence targets a real isolated daemon. The harness only sets
// these variables while switch_pending_json is durable. A data frame does not
// need an open attachment: the durable input gate runs before attachment
// lookup, which makes this probe independent of whether the source pane has
// already stopped.
func TestLiveMuxInputFence(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("AO_ACCEPTANCE_MUX_URL"))
	terminalID := strings.TrimSpace(os.Getenv("AO_ACCEPTANCE_TERMINAL_ID"))
	if url == "" || terminalID == "" {
		t.Skip("live probe requires AO_ACCEPTANCE_MUX_URL and AO_ACCEPTANCE_TERMINAL_ID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	defer c.Close(websocket.StatusNormalClosure, "acceptance probe complete")

	data := base64.StdEncoding.EncodeToString([]byte("MVP_FENCE_MUST_NOT_REACH_PROVIDER\n"))
	if err := wsjson.Write(ctx, c, muxFrame{Ch: "terminal", ID: terminalID, Type: "data", Data: data}); err != nil {
		t.Fatalf("write data frame: %v", err)
	}
	for {
		var got muxFrame
		if err := wsjson.Read(ctx, c, &got); err != nil {
			t.Fatalf("read fence response: %v", err)
		}
		if got.Ch != "terminal" || got.ID != terminalID {
			continue
		}
		if got.Type != "error" || got.Error != "input blocked: switch in progress" {
			t.Fatalf("frame = %+v, want terminal error fence", got)
		}
		t.Logf("durable mux input fence refused terminal %s", terminalID)
		return
	}
}
