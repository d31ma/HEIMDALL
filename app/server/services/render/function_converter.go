package render

import (
	"time"

	"github.com/d31ma/heimdall/app/server/models/spec"
)

type functionConverter struct {
	service *composeService
	file    string
	name    string
}

func (c functionConverter) Convert() (*spec.Function, error) {
	return c.convert()
}

func (c functionConverter) convert() (*spec.Function, error) {
	if c.service.Function == nil {
		return nil, nil
	}
	if err := c.contradictions(); err != nil {
		return nil, err
	}
	timeout, err := c.timeout()
	if err != nil {
		return nil, err
	}
	function := &spec.Function{RuntimeContract: c.service.Function.RuntimeContract,
		Invocation: c.service.Function.Invocation, Architecture: c.service.Function.Architecture, TimeoutMS: timeout}
	return c.validated(function)
}

func (c functionConverter) validated(function *spec.Function) (*spec.Function, error) {
	if err := function.Validate(); err != nil {
		return nil, renderError("HD0223", c.file, c.name, "x-heimdall-function", "%s", err)
	}
	return function, nil
}

func (c functionConverter) timeout() (int, error) {
	if c.service.Function.Timeout == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(c.service.Function.Timeout)
	if err != nil {
		return 0, renderError("HD0223", c.file, c.name, "x-heimdall-function.timeout", "use a duration such as 30s")
	}
	bounds := spec.Function{}
	if duration < time.Duration(bounds.MinimumTimeoutMS())*time.Millisecond || duration > time.Duration(bounds.MaximumTimeoutMS())*time.Millisecond || duration%time.Second != 0 {
		return 0, renderError("HD0223", c.file, c.name, "x-heimdall-function.timeout", "use whole seconds from 1s through 900s")
	}
	return int(duration.Milliseconds()), nil
}

func (c functionConverter) contradictions() error {
	for _, field := range c.contradictoryFields() {
		if field.present {
			return renderError("HD0223", c.file, c.name, field.name, "%s is a function; declare its invocation contract instead of container lifecycle settings", c.name)
		}
	}
	return nil
}

func (c functionConverter) contradictoryFields() []struct {
	name    string
	present bool
} {
	return []struct {
		name    string
		present bool
	}{
		{"ports", len(c.service.Ports) > 0},
		{"volumes", len(c.service.Volumes) > 0},
		{"healthcheck", c.service.Healthcheck != nil},
		{"restart", c.service.Restart != ""},
		{"depends_on", len(c.service.DependsOn) > 0},
		{"x-heimdall-schedule", c.service.Schedule != nil},
		{"deploy.replicas", c.hasReplicas()},
		{"deploy.update_config", c.hasUpdate()},
		{"secrets", len(c.service.Secrets) > 0},
	}
}

func (c functionConverter) hasReplicas() bool {
	return c.service.Deploy != nil && c.service.Deploy.Replicas != nil
}

func (c functionConverter) hasUpdate() bool {
	return c.service.Deploy != nil && c.service.Deploy.UpdateConfig != nil
}
