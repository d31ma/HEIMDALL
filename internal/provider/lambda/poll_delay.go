package lambda

import (
	"context"
	"time"
)

type pollDelay struct{}

func (p pollDelay) Wait(ctx context.Context) error {
	timer := time.NewTimer((functionLimits{}).PollDelay())
	defer timer.Stop()
	return p.wait(ctx, timer)
}
func (pollDelay) wait(ctx context.Context, timer *time.Timer) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
