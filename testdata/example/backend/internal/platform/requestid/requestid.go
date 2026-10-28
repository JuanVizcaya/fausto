// Package requestid carries a request ID through contexts and HTTP headers so
// logs from every module can be correlated.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

const Header = "X-Request-ID"

type contextKey struct{}

// With returns a copy of ctx carrying id.
func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// From returns the request ID in ctx, or "" if there is none.
func From(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}

// Valid reports whether id is safe to accept from a caller and log.
func Valid(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// New returns a random request ID.
func New() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
