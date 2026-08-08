package tmux

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Liveness classification for an ABSENT tmux server.
//
// IsAlive reports literal absence through ErrRuntimeServerAbsent on both the
// default and namespaced sockets. It remains an error so board probes keep
// treating one server outage as uncertainty rather than N independent deaths.

// exitErrRunner fails the probe with a real *exec.ExitError (tmux exits 1 for
// everything) carrying the given stderr, which is what IsAlive classifies on.
func exitErrRunner(t *testing.T, output string) *fakeRunner {
	t.Helper()
	// A genuine ExitError is required: IsAlive only inspects output when
	// errors.As finds one, so a plain error would skip the whole branch and the
	// test would pass for the wrong reason.
	cmd := exec.Command("/bin/sh", "-c", "exit 1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("could not construct an ExitError: %v", err)
	}
	return &fakeRunner{outputs: [][]byte{[]byte(output)}, err: exitErr}
}

func runtimeWithSocket(t *testing.T, socket, output string) *Runtime {
	t.Helper()
	r := New(Options{Binary: "tmux-test", Timeout: time.Second, Shell: "/bin/sh", Socket: socket})
	r.runner = exitErrRunner(t, output)
	return r
}

func TestIsAlive_MissingServerReportsTypedAbsenceOnDefaultAndNamespacedSockets(t *testing.T) {
	defaultDir := t.TempDir()
	sockets := map[string]string{
		"default":    SocketForDataDir(defaultDir, defaultDir),
		"namespaced": SocketForDataDir(t.TempDir(), defaultDir),
	}
	if sockets["default"] != "" {
		t.Fatalf("default SocketForDataDir = %q, want empty", sockets["default"])
	}
	if sockets["namespaced"] == "" {
		t.Fatal("isolated data dir must produce a namespaced socket")
	}
	for name, socket := range sockets {
		t.Run(name, func(t *testing.T) {
			r := runtimeWithSocket(t, socket, "no server running on /private/tmp/tmux-501/x")
			alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "mer-1"})
			if alive {
				t.Fatal("alive = true with no server")
			}
			if !errors.Is(err, ports.ErrRuntimeServerAbsent) {
				t.Fatalf("err = %v, want ErrRuntimeServerAbsent", err)
			}
			if !errors.Is(err, ports.ErrRuntimeUnavailable) {
				t.Fatalf("err = %v must retain ErrRuntimeUnavailable compatibility", err)
			}
		})
	}
}

// 2. Missing pane on a live server confirms dead. Pre-existing behaviour, pinned
// here so the new branch cannot be mistaken for the thing that provides it.
func TestIsAlive_MissingSessionOnLiveServerConfirmsDead(t *testing.T) {
	r := runtimeWithSocket(t, "ao-deadbeef1234", "can't find session: mer-1")
	alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "mer-1"})
	if err != nil || alive {
		t.Fatalf("alive=%v err=%v; a missing session on a live server is confirmed dead", alive, err)
	}
}

func TestIsAlive_UnreachableAndUnknownFailuresAreNotServerAbsence(t *testing.T) {
	for _, out := range []string{
		"error connecting to /private/tmp/tmux-501/ao-deadbeef1234 (Permission denied)",
		"error connecting to /private/tmp/tmux-501/ao-deadbeef1234 (No such file or directory)",
		"tmux: unknown catastrophe",
		"",
	} {
		r := runtimeWithSocket(t, "ao-deadbeef1234", out)
		alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "mer-1"})
		if err == nil {
			t.Fatalf("output %q: err = nil; only absence proves death, reachability failures do not", out)
		}
		if errors.Is(err, ports.ErrRuntimeServerAbsent) {
			t.Fatalf("output %q: err = %v falsely classified as server absence", out, err)
		}
		if alive {
			t.Fatalf("output %q: alive = true on a failed probe", out)
		}
	}
}

// 4. An existing session remains alive. The new branch must not be reachable on
// a successful probe at all.
func TestIsAlive_ExistingSessionRemainsAlive(t *testing.T) {
	r := New(Options{Binary: "tmux-test", Timeout: time.Second, Shell: "/bin/sh", Socket: "ao-deadbeef1234"})
	r.runner = &fakeRunner{}
	alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "mer-1"})
	if err != nil || !alive {
		t.Fatalf("alive=%v err=%v; a successful has-session probe means alive", alive, err)
	}
}

// A malformed handle is a caller error, not a liveness verdict, and must not be
// answered with "dead" by any of the branches above.
func TestIsAlive_MalformedHandleIsAnErrorNotDeath(t *testing.T) {
	r := New(Options{Binary: "tmux-test", Timeout: time.Second, Shell: "/bin/sh", Socket: "ao-deadbeef1234"})
	r.runner = &fakeRunner{}
	_, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "  "})
	if err == nil {
		t.Fatal("an empty handle must be an error, not a death verdict")
	}
	if errors.Is(err, ports.ErrRuntimeServerAbsent) {
		t.Fatal("a malformed handle must not read as server absence")
	}
}

// killSessionMissingOutput must keep treating BOTH absence and unreachability as
// "nothing left to kill". Teardown is generous on purpose, and the new split in
// liveness must not tighten it into a failure to clean up.
func TestKillSessionMissingOutput_StaysGenerousAfterTheLivenessSplit(t *testing.T) {
	for _, out := range []string{
		"no server running on /tmp/x",
		"error connecting to /tmp/x (Permission denied)",
		"can't find session: mer-1",
	} {
		if !killSessionMissingOutput(out) {
			t.Fatalf("output %q: teardown must still treat this as nothing-to-kill", out)
		}
	}
}
