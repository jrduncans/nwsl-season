// Package lifetime separates a caller's short deadline from the lifetime of
// the component that owns the work.
//
// Derived calculations and forecast warming intentionally outlive the short
// source-request deadline that triggered them, so they cannot simply inherit
// that context. They must still stop when their owner, such as the scheduler,
// shuts down; otherwise process shutdown waits for a full calculation budget.
package lifetime

import "context"

type ownerKey struct{}

// Root marks ctx as the owner of work detached beneath it. Work detached from
// a descendant of the returned context is canceled when ctx is done.
func Root(ctx context.Context) context.Context {
	return context.WithValue(ctx, ownerKey{}, ctx)
}

// Detach returns a context that keeps ctx's values but ignores its deadline and
// cancellation, like context.WithoutCancel. When ctx descends from Root, the
// returned context is still canceled once that owner is done. The caller must
// call the returned CancelFunc to release resources.
func Detach(ctx context.Context) (context.Context, context.CancelFunc) {
	detached, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	owner, ok := ctx.Value(ownerKey{}).(context.Context)
	if !ok {
		return detached, func() { cancel(context.Canceled) }
	}
	stop := context.AfterFunc(owner, func() { cancel(context.Cause(owner)) })
	return detached, func() {
		stop()
		cancel(context.Canceled)
	}
}
