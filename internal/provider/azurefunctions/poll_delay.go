package azurefunctions

import (
	"context"
	"time"
)

type pollDelay struct{}

func (d pollDelay) run(ctx context.Context) error {
	timer := time.NewTimer((functionContract{}).pollDelay())
	defer timer.Stop()
	return d.wait(ctx, timer)
}
func (pollDelay) wait(ctx context.Context, timer *time.Timer) error {
	select {
	case <-ctx.Done():
		return &AdapterError{Code: (functionContract{}).convergenceCode(), Message: "wait for native function convergence", Cause: ctx.Err()}
	case <-timer.C:
		return nil
	}
}
