package httpclient

import (
	"context"
	"sync"
)

// TokenRefresher mints a fresh Brightspace OAuth access token. The login
// package implements this; httpclient holds an indirection so the two
// packages don't import each other.
type TokenRefresher interface {
	Refresh(ctx context.Context) (token string, err error)
}

// TokenRefresherFunc lets a plain function satisfy TokenRefresher without
// declaring a new type.
type TokenRefresherFunc func(ctx context.Context) (string, error)

// Refresh implements TokenRefresher.
func (f TokenRefresherFunc) Refresh(ctx context.Context) (string, error) { return f(ctx) }

var (
	refresherMu sync.RWMutex
	refresher   TokenRefresher
)

// RegisterTokenRefresher wires in the package that knows how to mint a new
// token (typically internal/login). Passing nil clears the registration.
func RegisterTokenRefresher(r TokenRefresher) {
	refresherMu.Lock()
	defer refresherMu.Unlock()
	refresher = r
}

// InvalidateBearerCache clears the registered refresher and returns the
// previously-registered value (nil if none was set or it was already cleared).
// Future bearer-token callers that want to round-trip via the refresher can
// re-register it; today this is used by the login package after a manual
// refresh so the next 401 path sees a fresh binding.
func InvalidateBearerCache() TokenRefresher {
	refresherMu.Lock()
	defer refresherMu.Unlock()
	prev := refresher
	refresher = nil
	return prev
}
