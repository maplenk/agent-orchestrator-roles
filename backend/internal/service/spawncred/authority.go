// Package spawncred issues session-scoped spawn credentials and validates
// operator spawn tokens.
//
// Security model (same-OS-user residual risk remains; we close app-level holes):
//
//   - Session tokens are random (not HMAC over a global key). Only a SHA-256
//     hash is persisted on the session row. The plaintext is injected solely as
//     AO_SPAWN_CAPABILITY for that process — workers cannot mint tokens for
//     other sessions by reading a signing key under AO_DATA_DIR.
//   - Operator spawn requires X-AO-Operator-Spawn-Token matching the
//     daemon-launch secret published only in running.json (never injected into
//     session environments). Headerless "operator" is rejected.
//   - Terminated sessions are rejected even with a still-valid token hash.
package spawncred

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const tokenBytes = 32

// Issue creates a new random session spawn capability and its durable hash.
// Persist hash on the session; inject plaintext into the session environment only.
func Issue() (plaintext, hash string, err error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("spawncred: generate token: %w", err)
	}
	plaintext = base64.RawURLEncoding.EncodeToString(raw)
	return plaintext, Hash(plaintext), nil
}

// Hash returns the hex-encoded SHA-256 of token (empty → empty).
func Hash(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ValidToken reports whether plaintext matches the stored hash (constant-time).
func ValidToken(plaintext, storedHash string) bool {
	if plaintext == "" || storedHash == "" {
		return false
	}
	got := Hash(plaintext)
	return subtle.ConstantTimeCompare([]byte(got), []byte(storedHash)) == 1
}

// Operator holds the daemon-launch operator spawn secret (never given to sessions).
type Operator struct {
	token string
}

// NewOperator generates a random operator spawn token for this daemon process.
func NewOperator() (*Operator, string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("spawncred: generate operator token: %w", err)
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	return &Operator{token: tok}, tok, nil
}

// OperatorFromToken wraps an existing operator secret (tests / restart plumbing).
func OperatorFromToken(token string) *Operator {
	return &Operator{token: token}
}

// Valid reports whether the presented operator token matches (constant-time).
func (o *Operator) Valid(token string) bool {
	if o == nil || o.token == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(o.token), []byte(token)) == 1
}

// Token returns the operator secret (for runfile publication only).
func (o *Operator) Token() string {
	if o == nil {
		return ""
	}
	return o.token
}
