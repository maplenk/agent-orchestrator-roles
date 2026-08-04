// Package authctx carries request-scoped authentication facts across httpd
// middleware and controllers without import cycles.
package authctx

import "context"

type key int

const lanAuthenticated key = 1

// WithLANAuthenticated marks the request as having passed LAN password auth.
// Controllers treat this as a trusted operator spawn path (mobile).
func WithLANAuthenticated(ctx context.Context) context.Context {
	return context.WithValue(ctx, lanAuthenticated, true)
}

// IsLANAuthenticated reports whether the request was authenticated by the LAN
// password middleware.
func IsLANAuthenticated(ctx context.Context) bool {
	v, _ := ctx.Value(lanAuthenticated).(bool)
	return v
}
