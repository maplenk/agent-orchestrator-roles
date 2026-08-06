package tmux

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The default installation keeps the default tmux server. Moving it would make
// every running pane invisible to the daemon that owns it, and boot would then
// reconcile them as crashed and tear them down.
func TestSocketForDataDir_DefaultKeepsTheDefaultServer(t *testing.T) {
	def := "/Users/x/.ao/data"
	if got := SocketForDataDir(def, def); got != "" {
		t.Fatalf("socket = %q, want empty (default server)", got)
	}
	// Equivalent spellings of the same directory are the same directory.
	if got := SocketForDataDir("/Users/x/.ao/./data/", def); got != "" {
		t.Fatalf("socket = %q for an equivalent path, want empty", got)
	}
}

func TestSocketForDataDir_IsolatedIsStableAndDistinct(t *testing.T) {
	def := "/Users/x/.ao/data"
	a := SocketForDataDir("/tmp/review/data", def)
	if a == "" {
		t.Fatal("an isolated data dir got the default server; two daemons could then address the same panes")
	}
	if a != SocketForDataDir("/tmp/review/data", def) {
		t.Fatal("socket is not stable across calls; a daemon would lose its own sessions on restart")
	}
	if b := SocketForDataDir("/tmp/other/data", def); b == a {
		t.Fatal("two different data dirs share a socket")
	}
	if !strings.HasPrefix(a, "ao-") {
		t.Errorf("socket %q should be recognisable to a human clearing stale sockets", a)
	}
}

// An unresolvable default must NOT hand out the shared server: an isolated
// socket is always safe, a shared one is not.
func TestSocketForDataDir_UnknownDefaultFailsClosed(t *testing.T) {
	if got := SocketForDataDir("/tmp/review/data", ""); got == "" {
		t.Fatal("an unknown default resolved to the shared server")
	}
}

// Every tmux operation must carry the server selector. This asserts on the argv
// the runtime actually builds, because isolation that holds for create and not
// for destroy is worse than none — the second daemon would then kill the
// first's pane.
func TestEveryOperationCarriesTheSocket(t *testing.T) {
	rec := &fakeRunner{}
	r := New(Options{Socket: "ao-testsock", Binary: "tmux"})
	r.runner = rec

	ctx := t.Context()
	h := ports.RuntimeHandle{ID: "sess-1"}
	_, _ = r.IsAlive(ctx, h)
	_ = r.Destroy(ctx, h)
	_, _ = r.GetOutput(ctx, h, 10)

	if len(rec.calls) == 0 {
		t.Fatal("no tmux invocations recorded")
	}
	for _, c := range rec.calls {
		if len(c.args) < 2 || c.args[0] != "-L" || c.args[1] != "ao-testsock" {
			t.Fatalf("invocation %v does not carry the server selector; that operation escapes the namespace", c.args)
		}
	}
}

// Attach is built as an argv for a terminal client rather than run directly, so
// it is the operation most likely to be forgotten.
func TestAttachCommandCarriesTheSocket(t *testing.T) {
	r := New(Options{Socket: "ao-testsock", Binary: "tmux"})
	argv, err := r.attachCommand(ports.RuntimeHandle{ID: "sess-1"})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "-L ao-testsock") {
		t.Fatalf("attach argv %q has no server selector; the terminal would attach to the DEFAULT server", joined)
	}
	if filepath.Base(argv[0]) != "tmux" {
		t.Fatalf("argv[0] = %q", argv[0])
	}
}

// The default server keeps clean argv, so an existing installation's commands
// are byte-identical to before.
func TestNoSocketMeansNoSelector(t *testing.T) {
	rec := &fakeRunner{}
	r := New(Options{Binary: "tmux"})
	r.runner = rec
	_, _ = r.IsAlive(t.Context(), ports.RuntimeHandle{ID: "sess-1"})
	for _, c := range rec.calls {
		if len(c.args) > 0 && c.args[0] == "-L" {
			t.Fatalf("default server invocation carries a selector: %v", c.args)
		}
	}
}
