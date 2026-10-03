package azurefunctions

import (
	"context"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
)

type functionConvergence struct {
	client         *armClient
	context        context.Context
	name           string
	desired        *nativeApp
	app            *nativeApp
	appHealth      provider.Health
	revisionHealth provider.Health
	complete       bool
	err            error
}

func (c functionConvergence) wait(ctx context.Context) error {
	bounded, cancel := context.WithTimeout(ctx, (functionContract{}).convergenceTimeout())
	defer cancel()
	c.context = bounded
	c.poll()
	return c.err
}
func (c *functionConvergence) poll() {
	for !c.complete && c.err == nil {
		c.err = (sequence.Steps{Actions: []func() error{c.read, c.deletion, c.readAppHealth, c.requireApp, c.readRevision, c.requireRevision, c.finish, c.pause}}).Run()
	}
}
func (c *functionConvergence) read() error {
	app, err := c.client.get(c.context, c.name)
	c.app = app
	return err
}
func (c *functionConvergence) deletion() error {
	c.complete = c.desired == nil && c.app == nil
	return nil
}
func (c *functionConvergence) readAppHealth() error {
	c.appHealth = (appComparison{}).ready(c.app)
	return nil
}
func (c *functionConvergence) requireApp() error {
	if c.desired != nil && c.app != nil && c.appHealth == provider.Degraded {
		return &AdapterError{Code: (functionContract{}).convergenceCode(), Message: "native function provisioning failed or returned an incompatible resource kind"}
	}
	return nil
}
func (c *functionConvergence) readRevision() error {
	c.revisionHealth = provider.Progressing
	return c.inspectRevision()
}
func (c *functionConvergence) inspectRevision() error {
	if c.desired == nil || c.app == nil {
		return nil
	}
	health, err := (revisionReadback{client: c.client}).health(c.context, c.app, *c.desired)
	c.revisionHealth = health
	return err
}
func (c *functionConvergence) requireRevision() error {
	if c.revisionHealth == provider.Degraded {
		return &AdapterError{Code: (functionContract{}).convergenceCode(), Message: "the requested native function revision failed readiness"}
	}
	return nil
}
func (c *functionConvergence) finish() error {
	c.complete = c.complete || c.desired != nil && c.app != nil && c.revisionHealth == provider.Healthy
	return nil
}
func (c *functionConvergence) pause() error {
	if c.complete {
		return nil
	}
	return (pollDelay{}).run(c.context)
}
