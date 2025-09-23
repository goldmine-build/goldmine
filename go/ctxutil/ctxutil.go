package ctxutil

import (
	"context"
	"time"

	"go.goldmine.build/go/skerr"
	"go.goldmine.build/go/sklog"
)

// ConfirmContextHasDeadline logs errors with the full stack trace if the given
// context does not have a deadline. Useful for confirming that all SQL client
// calls have a timeout.
func ConfirmContextHasDeadline(ctx context.Context) {
	if _, ok := ctx.Deadline(); !ok {
		stack := []string{}
		for _, st := range skerr.CallStack(10, 1) {
			stack = append(stack, st.String())
		}
		sklog.Errorf("ctx is missing deadline at %s", stack)
	}
}

// WithContextTimeout calls `f` with a context that has a timeout, and ensures
// that the cancel function gets called.
func WithContextTimeout(ctx context.Context, timeout time.Duration, f func(ctx context.Context)) {
	timeoutContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	f(timeoutContext)
}
