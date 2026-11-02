// Package principal carries the authenticated caller through a request's
// context. The auth module sets it; other modules read it, so they never
// depend on how authentication works.
package principal

import "context"

// Principal is the authenticated caller of a request.
type Principal struct {
	UserID int64
}

type ctxKey struct{}

// With returns a copy of ctx carrying p.
func With(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// From returns the principal in ctx and whether one is present.
func From(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}
