package azurefunctions

import (
	"context"
	"net/http"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
	"github.com/d31ma/heimdall/internal/spec"
)

type functionChange struct {
	client      *armClient
	name        string
	plan        provider.Plan
	want        spec.DeploySpec
	serviceName string
	context     context.Context
	service     spec.Service
	desired     nativeApp
	response    *http.Response
}

func (c functionChange) apply(ctx context.Context) error {
	c.context = ctx
	return (sequence.Steps{Actions: []func() error{c.selectService, c.define, c.request, c.status, c.close, c.wait}}).Run()
}
func (c *functionChange) selectService() error {
	service, found := c.want.Service(c.serviceName)
	c.service = service
	return c.requireService(found)
}
func (functionChange) requireService(found bool) error {
	if !found {
		return &AdapterError{Code: (functionContract{}).planCode(), Message: "the planned function does not exist in the supplied spec"}
	}
	return nil
}
func (c *functionChange) define() error {
	desired, err := (functionDefinition{config: c.client.config}).build(c.plan, c.service)
	c.desired = desired
	return err
}
func (c *functionChange) request() error {
	response, err := c.client.request(c.context, http.MethodPut, c.client.path(c.name), c.desired)
	c.response = response
	return err
}
func (c *functionChange) status() error {
	if c.response.StatusCode != http.StatusOK && c.response.StatusCode != http.StatusCreated && c.response.StatusCode != http.StatusAccepted {
		return (armResponse{response: c.response}).failure()
	}
	return nil
}
func (c *functionChange) close() error { return c.response.Body.Close() }
func (c *functionChange) wait() error {
	return (functionConvergence{client: c.client, name: c.name, desired: &c.desired}).wait(c.context)
}
