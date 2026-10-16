package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
)

type convergence struct {
	engine       *Engine
	input        convergenceInput
	context      context.Context
	cancel       context.CancelFunc
	touched      map[string]bool
	hold         time.Duration
	interval     time.Duration
	timeout      time.Duration
	deadline     time.Time
	healthySince time.Time
	lastReason   string
	pending      bool
	result       error
}

func (c *convergence) Run(ctx context.Context) error {
	c.initialize(ctx)
	defer c.cancel()
	return c.wait(ctx)
}

func (c *convergence) initialize(ctx context.Context) {
	c.touched = map[string]bool{}
	c.collectTouched()
	c.collectMonitor()
	c.configureInterval()
	c.configureTimeout()
	c.deadline = time.Now().Add(c.timeout + c.hold)
	c.context, c.cancel = context.WithDeadline(ctx, c.deadline)
	c.lastReason = "the runtime never reported the new revision as healthy"
	c.pending = len(c.touched) != 0
}

func (c *convergence) collectTouched() {
	for _, operation := range c.input.plan.Operations {
		c.MarkTouched(operation)
	}
}

func (c *convergence) MarkTouched(operation provider.Operation) {
	c.markTouched(operation)
}

func (c *convergence) markTouched(operation provider.Operation) {
	switch operation.Kind {
	case provider.OpCreate, provider.OpUpdate, provider.OpRestart:
		c.touched[operation.Service] = true
	}
}

func (c *convergence) collectMonitor() {
	for _, service := range c.input.want.Services {
		c.IncludeMonitor(service)
	}
}

func (c *convergence) IncludeMonitor(service spec.Service) {
	c.includeMonitor(service)
}

func (c *convergence) includeMonitor(service spec.Service) {
	if c.touched[service.Name] && service.Update != nil {
		c.hold = max(c.hold, time.Duration(service.Update.MonitorMS)*time.Millisecond)
	}
}

func (c *convergence) configureInterval() {
	minimum := 250 * time.Millisecond
	maximum := 5 * time.Second
	monitorReadings := time.Duration(5)
	c.interval = min(max(c.hold/monitorReadings, minimum), maximum)
}

func (c *convergence) configureTimeout() {
	c.timeout = c.engine.ConvergeTimeout
	if c.timeout <= 0 {
		c.timeout = defaultConvergeTimeout
	}
}

func (c *convergence) wait(ctx context.Context) error {
	for c.pending {
		c.Poll(ctx)
	}
	return c.result
}

func (c *convergence) Poll(ctx context.Context) {
	c.cancelled(ctx.Err())
	c.expire()
	c.pollRuntime(ctx)
}

func (c *convergence) pollRuntime(ctx context.Context) {
	if c.pending {
		c.readRuntime(ctx)
	}
}

func (c *convergence) readRuntime(ctx context.Context) {
	c.heartbeat()
	live, observeError := c.input.resolved.adapter.Observe(c.context, c.input.resolved.target.Ref(), c.input.resolved.app.AppRef())
	planned, planError := c.input.resolved.adapter.Plan(c.context, c.input.resolved.target.Ref(), c.input.want)
	proof := convergenceProof{expected: c.input.plan, observed: planned, live: live, touched: c.touched, observeError: observeError, planError: planError}
	reason := proof.Reason()
	c.recordReason(reason)
	c.beginHealthy(reason)
	c.complete(reason)
	c.expire()
	delay := c.pauseDuration()
	cancelError := c.waitTick(ctx, delay)
	c.cancelled(cancelError)
}

func (c *convergence) heartbeat() {
	if c.engine.Heartbeat != nil {
		c.engine.Heartbeat()
	}
}

func (c *convergence) recordReason(reason string) {
	if reason != "" {
		c.healthySince = time.Time{}
		c.lastReason = reason
	}
}

func (c *convergence) beginHealthy(reason string) {
	if reason == "" && c.healthySince.IsZero() {
		c.healthySince = time.Now()
	}
}

func (c *convergence) complete(reason string) {
	if reason == "" && time.Since(c.healthySince) >= c.hold {
		c.pending = false
	}
}

func (c *convergence) expire() {
	if c.pending && time.Now().After(c.deadline) {
		c.result = fmt.Errorf("HD0273: %s did not converge within %s: %s", c.input.resolved.app.Name, c.timeout, c.lastReason)
		c.pending = false
	}
}

func (c *convergence) pauseDuration() time.Duration {
	if !c.pending {
		return 0
	}
	return min(c.interval, max(time.Until(c.deadline), 0))
}

func (c *convergence) waitTick(ctx context.Context, delay time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}

func (c *convergence) cancelled(err error) {
	if c.pending && err != nil {
		c.result = fmt.Errorf("HD0274: waiting for %s to converge: %w", c.input.resolved.app.Name, err)
		c.pending = false
	}
}
