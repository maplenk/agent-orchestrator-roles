package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// callOrder returns, for each call expression in fn's body, the source offset
// at which it appears, keyed by its rendered callee (e.g. "sessMgr.Reconcile",
// "sweepShellTerminals"). Only the FIRST occurrence of each callee is recorded,
// which is what an ordering assertion cares about, alongside a count so a
// duplicated boot step is visible.
func callOrder(fn *ast.FuncDecl) (first map[string]token.Pos, count map[string]int) {
	first = map[string]token.Pos{}
	count = map[string]int{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := renderCallee(call.Fun)
		if name == "" {
			return true
		}
		count[name]++
		if _, seen := first[name]; !seen {
			first[name] = call.Pos()
		}
		return true
	})
	return first, count
}

// renderCallee flattens an ident or a (possibly chained) selector into dotted
// text. Anything else — a call on a call, a func literal — renders empty and is
// ignored, since the boot steps this test guards are all plain calls.
func renderCallee(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		base := renderCallee(e.X)
		if base == "" {
			return e.Sel.Name
		}
		return base + "." + e.Sel.Name
	default:
		return ""
	}
}

func parseRunFunc(t *testing.T) (*token.FileSet, *ast.FuncDecl) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "daemon.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse daemon.go: %v", err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == "Run" {
			return fset, fn
		}
	}
	t.Fatal("daemon.go has no func Run")
	return nil, nil
}

// TestBootOrder_ReapQueueDrainsBeforeSessionReconciliationAndServing is the
// regression for the drain sitting too late on the boot path.
//
// DrainOrchestratorReapQueue is the only FATAL step in boot: a queue entry means
// a superseded orchestrator's process may still be executing inside the
// canonical workspace its successor now owns, and migration 0057's constraint
// exists to prevent AO running in that state at all. Session and runtime
// reconciliation, and every client-facing surface, must therefore sit behind
// it — the earlier arrangement ran the best-effort shell sweep, started the
// browser runtime listener, and re-armed the mobile LAN listener first, so an
// unconfirmed obligation did not actually stop AO from acting.
//
// Scope, deliberately: this does NOT claim the drain precedes all
// initialization. Notification reconciliation and the lifecycle/activity/SCM
// pollers are already running by then, and moving them would be a far larger
// change for no gain — none of them touch orchestrator ownership or expose a
// surface. The assertions below are exactly the steps that do.
//
// The one thing that must come BEFORE the drain is the shell closer wiring:
// the drain confirms scoped shells are closed and refuses to discharge an
// obligation without a closer (see requireShellTerminalTeardown), so an
// unwired closer would wedge boot permanently.
//
// This reads source order rather than booting a daemon because the invariant IS
// the source order: Run takes no arguments, binds real listeners, and has no
// seam to observe these steps through.
func TestBootOrder_ReapQueueDrainsBeforeSessionReconciliationAndServing(t *testing.T) {
	fset, run := parseRunFunc(t)
	first, count := callOrder(run)

	const drain = "sessMgr.DrainOrchestratorReapQueue"
	drainPos, ok := first[drain]
	if !ok {
		t.Fatalf("%s is not called in Run: the boot reaper is not wired at all", drain)
	}
	if count[drain] != 1 {
		t.Fatalf("%s called %d times, want exactly once", drain, count[drain])
	}

	// Must come after: without a wired closer the drain cannot confirm shells.
	before := []struct {
		call, why string
	}{
		{"sessMgr.SetShellTerminalCloser", "the drain needs a shell closer to confirm scoped shells are shut"},
	}
	for _, b := range before {
		pos, ok := first[b.call]
		if !ok {
			t.Errorf("%s is not called in Run at all", b.call)
			continue
		}
		if pos > drainPos {
			t.Errorf("%s runs at %s, AFTER the reap drain at %s — %s",
				b.call, fset.Position(pos), fset.Position(drainPos), b.why)
		}
	}

	// Must come before: every one of these either mutates durable state or
	// exposes a surface, and none may happen while an obligation is unconfirmed.
	for _, a := range stepsBehindTheFatalGates() {
		pos, ok := first[a.call]
		if !ok {
			t.Errorf("%s is not called in Run at all; this assertion has gone stale", a.call)
			continue
		}
		if pos < drainPos {
			t.Errorf("%s runs at %s, BEFORE the reap drain at %s — %s",
				a.call, fset.Position(pos), fset.Position(drainPos), a.why)
		}
	}
}

// gatedStep is a boot step that must not run until the fatal gates have passed.
type gatedStep struct{ call, why string }

// stepsBehindTheFatalGates lists everything that either mutates durable state or
// exposes a surface to a client. Both fatal gates — the reap-queue drain and
// session reconciliation — are checked against this same list, because a gate
// that runs after a listener is already live cannot prevent serving.
func stepsBehindTheFatalGates() []gatedStep {
	return []gatedStep{
		{"httpd.NewWithDeps", "the API server must not be built before the gates pass"},
		{"preview.NewPoller", "the preview poller drives sessions"},
		{"browserruntime.Listen", "the browser runtime listener is a live surface"},
		{"browserBroker.Serve", "serving browser control exposes the workspace"},
		{"restoreMobileOnBoot", "re-arming the mobile LAN listener exposes the API"},
		{"supervisor.Listen", "the supervisor link is a live surface"},
		{"srv.Run", "the daemon must not serve with an outstanding obligation"},
	}
}

// TestBootOrder_SessionReconcileGateAlsoPrecedesEverySurface pins the second
// fatal gate.
//
// Reconcile is only mostly best-effort: ErrLaunchCleanupUnresolved means a
// relaunch left a runtime executing that nothing will sweep before the next
// restart, and that aborts boot. It previously ran AFTER browserruntime.Listen
// /Serve and restoreMobileOnBoot, so making it fatal would still have exposed
// live surfaces first — the gate has to precede them to mean anything.
//
// It must still follow the reap drain and the shell-closer wiring: Reconcile
// tears down worktrees, which needs the closer, and the drain owns the
// stronger claim on the same runtimes.
func TestBootOrder_SessionReconcileGateAlsoPrecedesEverySurface(t *testing.T) {
	fset, run := parseRunFunc(t)
	first, count := callOrder(run)

	const reconcile = "sessMgr.Reconcile"
	pos, ok := first[reconcile]
	if !ok {
		t.Fatalf("%s is not called in Run", reconcile)
	}
	if count[reconcile] != 1 {
		t.Fatalf("%s called %d times, want exactly once", reconcile, count[reconcile])
	}

	for _, b := range []gatedStep{
		{"sessMgr.SetShellTerminalCloser", "Reconcile tears down worktrees and needs the shell closer"},
		{"sessMgr.DrainOrchestratorReapQueue", "the reap queue owns the stronger claim on the same runtimes"},
		{"sweepShellTerminals", "the previous-run shell sweep precedes session reconciliation"},
	} {
		bp, found := first[b.call]
		if !found {
			t.Errorf("%s is not called in Run at all", b.call)
			continue
		}
		if bp > pos {
			t.Errorf("%s runs at %s, AFTER session reconcile at %s — %s",
				b.call, fset.Position(bp), fset.Position(pos), b.why)
		}
	}

	for _, a := range stepsBehindTheFatalGates() {
		ap, found := first[a.call]
		if !found {
			t.Errorf("%s is not called in Run at all; this assertion has gone stale", a.call)
			continue
		}
		if ap < pos {
			t.Errorf("%s runs at %s, BEFORE session reconcile at %s — %s",
				a.call, fset.Position(ap), fset.Position(pos), a.why)
		}
	}
}

// TestBootOrder_UnresolvedCleanupAbortsBoot pins that the fatal condition is
// actually wired, and that it is SELECTIVE: only ErrBootUnsafe aborts. Every
// other reconcile failure stays logged, or an unrelated store hiccup would stop
// the daemon starting at all.
//
// The guard must name ErrBootUnsafe specifically, not one of the sentinels that
// wrap it. Keying on a leaf (ErrLaunchCleanupUnresolved, say) silently drops
// every sibling condition — which is exactly how the restore-marker case would
// have gone unnoticed.
func TestBootOrder_UnresolvedCleanupAbortsBoot(t *testing.T) {
	fset, run := parseRunFunc(t)

	var branch *ast.IfStmt
	ast.Inspect(run.Body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || ifs.Init == nil {
			return true
		}
		if strings.Contains(stmtText(ifs.Init), "sessMgr.Reconcile") {
			branch = ifs
			return false
		}
		return true
	})
	if branch == nil {
		t.Fatal("no `if err := sessMgr.Reconcile(...); err != nil` in Run")
	}

	var (
		guarded bool
		returns int
		logs    int
	)
	ast.Inspect(branch.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			name := renderCallee(node.Fun)
			if name == "errors.Is" {
				for _, arg := range node.Args {
					if strings.Contains(renderCallee(arg), "ErrBootUnsafe") {
						guarded = true
					}
				}
			}
			if name == "log.Error" {
				logs++
			}
		case *ast.ReturnStmt:
			returns++
		}
		return true
	})
	if !guarded {
		t.Errorf("the reconcile-failure branch at %s does not test for ErrBootUnsafe: "+
			"boot cannot tell an outstanding runtime from an ordinary failure", fset.Position(branch.Pos()))
	}
	if returns == 0 {
		t.Errorf("the reconcile-failure branch at %s never returns: an unresolved runtime would not "+
			"stop the daemon serving", fset.Position(branch.Pos()))
	}
	if logs == 0 {
		t.Errorf("the reconcile-failure branch at %s has no logged path: making EVERY reconcile "+
			"failure fatal would stop boot on unrelated errors", fset.Position(branch.Pos()))
	}
}

// TestBootOrder_ReapDrainFailureAbortsBoot pins that the drain's error is
// returned rather than logged. Reconcile deliberately logs-and-continues; if the
// drain were folded into that shape, an unconfirmed obligation would print a
// warning and the daemon would serve anyway — the exact failure mode the queue
// exists to prevent.
func TestBootOrder_ReapDrainFailureAbortsBoot(t *testing.T) {
	fset, run := parseRunFunc(t)

	var stmt *ast.IfStmt
	ast.Inspect(run.Body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || ifs.Init == nil {
			return true
		}
		if strings.Contains(renderStmt(fset, ifs.Init), "DrainOrchestratorReapQueue") {
			stmt = ifs
			return false
		}
		return true
	})
	if stmt == nil {
		t.Fatal("no `if err := sessMgr.DrainOrchestratorReapQueue(...); err != nil` in Run")
	}

	var returns int
	ast.Inspect(stmt.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.ReturnStmt); ok {
			returns++
		}
		return true
	})
	if returns == 0 {
		t.Fatalf("the drain-failure branch at %s does not return: boot would continue "+
			"with an unconfirmed superseded orchestrator", fset.Position(stmt.Pos()))
	}
}

// renderStmt renders a statement's source text span for substring matching.
func renderStmt(fset *token.FileSet, stmt ast.Stmt) string {
	start := fset.Position(stmt.Pos())
	end := fset.Position(stmt.End())
	if start.Filename != end.Filename {
		return ""
	}
	return start.Filename + ":" + stmtText(stmt)
}

// stmtText flattens the callees inside a statement, which is enough to identify
// the drain without re-reading the file.
func stmtText(stmt ast.Stmt) string {
	var b strings.Builder
	ast.Inspect(stmt, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			b.WriteString(renderCallee(call.Fun))
			b.WriteString(" ")
		}
		return true
	})
	return b.String()
}
