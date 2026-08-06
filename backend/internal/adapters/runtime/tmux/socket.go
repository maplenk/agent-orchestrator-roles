package tmux

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// SocketForDataDir derives the tmux server socket a daemon rooted at dataDir
// must use.
//
// The DEFAULT data directory maps to the default server (empty result, no -L),
// deliberately: an existing installation's panes live there, and moving them
// would make every running session look dead to the daemon that owns it — boot
// would then reconcile them as crashed and tear them down. Isolation is what
// non-default directories need; continuity is what the default one needs.
//
// Any other directory gets a stable per-directory socket. Stable matters more
// than readable: the same directory must resolve to the same socket across
// restarts, or a daemon loses its own sessions on reboot.
func SocketForDataDir(dataDir, defaultDataDir string) string {
	clean := func(p string) string {
		p = strings.TrimSpace(p)
		if p == "" {
			return ""
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		return filepath.Clean(p)
	}
	d, def := clean(dataDir), clean(defaultDataDir)
	if d == "" || d == def {
		return ""
	}
	sum := sha256.Sum256([]byte(d))
	// tmux socket names live in a shared per-user directory, so the prefix
	// makes ownership obvious to a human running `tmux -L ... ls` or clearing
	// stale sockets by hand.
	return "ao-" + hex.EncodeToString(sum[:])[:12]
}
