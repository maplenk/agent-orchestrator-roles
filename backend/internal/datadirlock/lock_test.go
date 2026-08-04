package datadirlock_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/datadirlock"
)

func TestAcquire_Exclusive(t *testing.T) {
	dir := t.TempDir()
	l1, err := datadirlock.Acquire(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer l1.Close()

	l2, err := datadirlock.Acquire(dir)
	if !errors.Is(err, datadirlock.ErrLocked) {
		t.Fatalf("second acquire err=%v, want ErrLocked", err)
	}
	if l2 != nil {
		t.Fatal("second lease must be nil")
	}

	if err := l1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// After release, another process can acquire.
	l3, err := datadirlock.Acquire(dir)
	if err != nil {
		t.Fatalf("re-acquire after close: %v", err)
	}
	_ = l3.Close()
}

func TestAcquire_ConcurrentOnlyOneSucceeds(t *testing.T) {
	dir := t.TempDir()
	const n = 16
	var wins atomic.Int32
	var wg sync.WaitGroup
	leases := make(chan *datadirlock.Lease, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			l, err := datadirlock.Acquire(dir)
			if err == nil {
				wins.Add(1)
				leases <- l
				return
			}
			if !errors.Is(err, datadirlock.ErrLocked) {
				t.Errorf("unexpected err: %v", err)
			}
		}()
	}
	wg.Wait()
	close(leases)
	if wins.Load() != 1 {
		t.Fatalf("winners=%d, want 1", wins.Load())
	}
	for l := range leases {
		_ = l.Close()
	}
}

// TestConcurrentSubprocess_OneReachesReconcileMarker proves two real processes
// contending for the same data dir: exactly one creates the reconcile marker
// after acquiring the lease (mirrors boot: lease → mutate).
func TestConcurrentSubprocess_OneReachesReconcileMarker(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "reconciled.pids")
	// Use this test binary as the child via helper env.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	startChild := func() *exec.Cmd {
		cmd := exec.Command(exe, "-test.run=TestHelperProcess_AcquireAndReconcile", "-test.v")
		cmd.Env = append(os.Environ(),
			"DATADIRLOCK_HELPER=1",
			"DATADIRLOCK_DIR="+dir,
			"DATADIRLOCK_MARKER="+marker,
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd
	}
	c1, c2 := startChild(), startChild()
	if err := c1.Start(); err != nil {
		t.Fatal(err)
	}
	if err := c2.Start(); err != nil {
		_ = c1.Process.Kill()
		t.Fatal(err)
	}
	// Let both contend briefly.
	time.Sleep(200 * time.Millisecond)
	// Wait with timeout.
	done := make(chan error, 2)
	go func() { done <- c1.Wait() }()
	go func() { done <- c2.Wait() }()
	var exitErrs int
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				exitErrs++
			}
		case <-time.After(10 * time.Second):
			_ = c1.Process.Kill()
			_ = c2.Process.Kill()
			t.Fatal("timeout waiting for helper children")
		}
	}
	// Exactly one should succeed (exit 0); the other exits non-zero (locked).
	if exitErrs != 1 {
		t.Fatalf("non-zero exits=%d, want 1 (one locked, one owner)", exitErrs)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	// One line with one pid.
	lines := 0
	for _, b := range raw {
		if b == '\n' {
			lines++
		}
	}
	if lines != 1 {
		t.Fatalf("marker lines=%d content=%q, want exactly one pid line", lines, raw)
	}
	if _, err := strconv.Atoi(string(raw[:len(raw)-1])); err != nil {
		t.Fatalf("marker pid parse: %v (%q)", err, raw)
	}
}

// TestHelperProcess_AcquireAndReconcile is not a real test; it is the child
// process body for TestConcurrentSubprocess_OneReachesReconcileMarker.
func TestHelperProcess_AcquireAndReconcile(t *testing.T) {
	if os.Getenv("DATADIRLOCK_HELPER") != "1" {
		t.Skip("helper process")
	}
	dir := os.Getenv("DATADIRLOCK_DIR")
	marker := os.Getenv("DATADIRLOCK_MARKER")
	lease, err := datadirlock.Acquire(dir)
	if err != nil {
		// Locked: exit 1 so parent can count winners.
		os.Exit(1)
	}
	// Hold lease while "reconciling" (append pid once).
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		_ = lease.Close()
		os.Exit(2)
	}
	if _, err := f.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		_ = f.Close()
		_ = lease.Close()
		os.Exit(2)
	}
	_ = f.Close()
	// Hold briefly so the peer has time to observe ErrLocked.
	time.Sleep(500 * time.Millisecond)
	_ = lease.Close()
	os.Exit(0)
}
