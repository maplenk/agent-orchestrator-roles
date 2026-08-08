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
// tmux panes live inside the server process, so a server that does not exist
// cannot be hosting one of our sessions. That makes "no server running" on a
// socket AO owns an authoritative answer about liveness rather than a failure
// to obtain one -- which matters because a session whose agent exited takes the
// namespaced server down with it, and reading that as inconclusive left such a
// session permanently un-switchable: every switch, continue and recovery
// answered SWITCH_UNCERTAIN forever.
//
// The scoping is deliberately tight, because issue #3475 is what happens when
// it is not: reading a server-level outage as N session deaths archived every
// session on the board. Only the literal absence message counts, and only on a
// namespaced socket.

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

func namespacedRuntime(t *testing.T, output string) *Runtime {
	t.Helper()
	r := New(Options{Binary: "tmux-test", Timeout: time.Second, Shell: "/bin/sh", Socket: "ao-deadbeef1234"})
	r.runner = exitErrRunner(t, output)
	return r
}

// 1. Missing server confirms dead.
func TestIsAlive_AbsentNamespacedServerConfirmsDead(t *testing.T) {
	r := namespacedRuntime(t, "no server running on /private/tmp/tmux-501/ao-deadbeef1234")
	alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "mer-1"})
	if err != nil {
		t.Fatalf("err = %v; an AO-owned server that does not exist is an answer, not a failure", err)
	}
	if alive {
		t.Fatal("alive = true with no server to host the pane")
	}
}

// 2. Missing pane on a live server confirms dead. Pre-existing behaviour, pinned
// here so the new branch cannot be mistaken for the thing that provides it.
func TestIsAlive_MissingSessionOnLiveServerConfirmsDead(t *testing.T) {
	r := namespacedRuntime(t, "can't find session: mer-1")
	alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "mer-1"})
	if err != nil || alive {
		t.Fatalf("alive=%v err=%v; a missing session on a live server is confirmed dead", alive, err)
	}
}

// 3. Unexpected probe failure stays inconclusive -> SWITCH_UNCERTAIN upstream.
func TestIsAlive_UnexpectedFailureStaysInconclusive(t *testing.T) {
	for _, out := range []string{
		"error connecting to /private/tmp/tmux-501/ao-deadbeef1234 (Permission denied)",
		"error connecting to /private/tmp/tmux-501/ao-deadbeef1234 (No such file or directory)",
		"tmux: unknown catastrophe",
		"",
	} {
		r := namespacedRuntime(t, out)
		alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "mer-1"})
		if err == nil {
			t.Fatalf("output %q: err = nil; only absence proves death, reachability failures do not", out)
		}
		if alive {
			t.Fatalf("output %q: alive = true on a failed probe", out)
		}
	}
}

// The default server is shared with whatever tmux the human runs, so its
// absence is not a fact about AO's sessions and must stay inconclusive.
func TestIsAlive_AbsentDefaultServerStaysInconclusive(t *testing.T) {
	r := New(Options{Binary: "tmux-test", Timeout: time.Second, Shell: "/bin/sh"})
	r.runner = exitErrRunner(t, "no server running on /private/tmp/tmux-501/default")
	alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "mer-1"})
	if err == nil {
		t.Fatal("err = nil on the DEFAULT socket; that server is not exclusively ours")
	}
	if !errors.Is(err, ports.ErrRuntimeUnavailable) {
		t.Fatalf("err = %v, want ErrRuntimeUnavailable", err)
	}
	if alive {
		t.Fatal("alive = true on an inconclusive probe")
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
	if _, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "  "}); err == nil {
		t.Fatal("an empty handle must be an error, not a death verdict")
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
