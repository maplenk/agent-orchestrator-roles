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

// TestBootOrder_ReapQueueDrainsBeforeAnyReconciliationOrServing is the
// regression for the drain sitting too late on the boot path.
//
// DrainOrchestratorReapQueue is the only FATAL step in boot: a queue entry means
// a superseded orchestrator's process may still be executing inside the
// canonical workspace its successor now owns, and migration 0046's constraint
// exists to prevent AO running in that state at all. Anything that reconciles
// durable state or exposes a surface to a client must therefore sit behind it —
// the earlier arrangement ran the best-effort shell sweep, started the browser
// runtime listener, and re-armed the mobile LAN listener first, so an
// unconfirmed obligation did not actually stop AO from acting.
//
// The one thing that must come BEFORE the drain is the shell closer wiring:
// the drain confirms scoped shells are closed and refuses to discharge an
// obligation without a closer (see requireShellTerminalTeardown), so an
// unwired closer would wedge boot permanently.
//
// This reads source order rather than booting a daemon because the invariant IS
// the source order: Run takes no arguments, binds real listeners, and has no
// seam to observe these steps through.
func TestBootOrder_ReapQueueDrainsBeforeAnyReconciliationOrServing(t *testing.T) {
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
	after := []struct {
		call, why string
	}{
		{"sweepShellTerminals", "a best-effort sweep must not precede the fatal gate"},
		{"httpd.NewWithDeps", "the API server must not be built before the gate passes"},
		{"browserruntime.Listen", "the browser runtime listener is a live surface"},
		{"browserBroker.Serve", "serving browser control exposes the workspace"},
		{"restoreMobileOnBoot", "re-arming the mobile LAN listener exposes the API"},
		{"sessMgr.Reconcile", "generic reconciliation must follow the queue drain"},
		{"lcStack.ReconcileRuntime", "runtime reconciliation must follow the queue drain"},
		{"srv.Run", "the daemon must not serve with an outstanding obligation"},
	}
	for _, a := range after {
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
