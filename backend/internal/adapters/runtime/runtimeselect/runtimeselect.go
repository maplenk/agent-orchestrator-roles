// Package runtimeselect picks the correct runtime backend by platform:
// tmux on Darwin/Linux, conpty (ConPTY) on Windows.
package runtimeselect

import (
	"context"
	"log/slog"
	"runtime"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Runtime is the union interface that both tmux and conpty satisfy.
// It extends ports.Runtime (Create/Destroy/IsAlive) with the additional methods
// the daemon wires directly, including ports.Attacher (Attach) so the terminal
// layer can open a Stream against the selected runtime.
type Runtime interface {
	ports.Runtime // Create, Destroy, IsAlive
	ports.Attacher
	Interrupt(ctx context.Context, handle ports.RuntimeHandle) error
	SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
	GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
}

// Compile-time assertions: both adapters must implement the union interface.
var _ Runtime = (*tmux.Runtime)(nil)
var _ Runtime = (*conpty.Runtime)(nil)

// Both shipped adapters must also resolve a session id to the handle Create
// would register. Consumers type-assert this optional capability and fail
// closed without it (see the orchestrator reap queue), so losing it on either
// adapter must be a compile error rather than a boot-time surprise.
var _ ports.RuntimeSessionHandleResolver = (*tmux.Runtime)(nil)
var _ ports.RuntimeSessionHandleResolver = (*conpty.Runtime)(nil)

// New returns the per-platform runtime: tmux on Darwin/Linux, conpty on Windows.
// log is accepted for signature stability with callers but is currently unused.
//
// dataDir and defaultDataDir namespace the tmux server so two daemons rooted at
// different data directories cannot address — or destroy — each other's panes.
// Session names derive from the session id alone, so the same project under two
// data dirs produces the same name; on a shared tmux server the second daemon's
// create collides with the first's pane, and its kill destroys it.
//
// Windows is deliberately unaffected: conpty processes are owned by the daemon
// that spawned them and are not addressable by name from another process, so
// there is nothing to namespace and its behaviour is unchanged.
func New(_ *slog.Logger, dataDir, defaultDataDir string) Runtime {
	if runtime.GOOS != "windows" {
		return tmux.New(tmux.Options{Socket: tmux.SocketForDataDir(dataDir, defaultDataDir)})
	}
	return conpty.New(conpty.Options{})
}
