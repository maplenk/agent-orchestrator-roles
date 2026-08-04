// Package spawncred issues session-scoped spawn capabilities so the daemon can
// identify the calling agent session and enforce RoleExecutionPolicy.CanSpawn.
//
// Operator/desktop clients omit the caller identity headers and are allowed
// (loopback trust model). Agent sessions always present X-AO-Caller-Session-Id
// (from AO_SESSION_ID); only a valid HMAC capability proves identity. Sessions
// with a role pin and canSpawn=false are rejected even with a valid capability.
package spawncred

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const capabilityKeyFile = "spawn-capability.key"

// HMAC message prefix separates spawn tokens from browser capabilities that use
// the same key-derivation shape against session ids.
const macPrefix = "ao-spawn-v1:"

// Authority issues stable, unguessable per-session spawn capabilities.
type Authority struct {
	key []byte
}

// LoadAuthority loads or creates the daemon-local spawn capability key.
func LoadAuthority(dataDir string) (*Authority, error) {
	path := filepath.Join(dataDir, capabilityKeyFile)
	key, err := os.ReadFile(path) //nolint:gosec // path under AO data dir
	if err == nil {
		if len(key) != sha256.Size {
			return nil, fmt.Errorf("spawn capability key has invalid length")
		}
		return &Authority{key: key}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read spawn capability key: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create spawn capability directory: %w", err)
	}
	key = make([]byte, sha256.Size)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate spawn capability key: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec
	if errors.Is(err, os.ErrExist) {
		return LoadAuthority(dataDir)
	}
	if err != nil {
		return nil, fmt.Errorf("create spawn capability key: %w", err)
	}
	written, err := file.Write(key)
	if err == nil && written != len(key) {
		err = io.ErrShortWrite
	}
	if err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write spawn capability key: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close spawn capability key: %w", err)
	}
	return &Authority{key: key}, nil
}

// Token derives the capability injected into one session runtime.
func (a *Authority) Token(sessionID domain.SessionID) string {
	if a == nil || len(a.key) == 0 || sessionID == "" {
		return ""
	}
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(macPrefix))
	_, _ = mac.Write([]byte(sessionID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Valid performs a constant-time comparison against the expected capability.
func (a *Authority) Valid(sessionID domain.SessionID, token string) bool {
	expected := a.Token(sessionID)
	return expected != "" && token != "" && hmac.Equal([]byte(expected), []byte(token))
}
