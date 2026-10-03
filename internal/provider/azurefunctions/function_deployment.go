package azurefunctions

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

type functionDeployment struct{ client *armClient }

func (d functionDeployment) apply(ctx context.Context, plan provider.Plan, options ApplyOptions) (provider.Result, error) {
	result := provider.Result{}
	for _, operation := range plan.Operations {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if operation.Kind == provider.OpNoop || operation.Prune && !options.Prune {
			continue
		}
		if err := d.execute(ctx, plan, operation, options.Spec); err != nil {
			result.Failures = map[string]string{operation.Service: err.Error()}
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, nil
		}
		result.Applied = append(result.Applied, operation)
	}
	return result, nil
}

func (d functionDeployment) execute(ctx context.Context, plan provider.Plan, operation provider.Operation, want spec.DeploySpec) error {
	c := d.client
	name := (resourceIdentity{}).name(plan.App, operation.Service)
	live, err := c.get(ctx, name)
	if err != nil {
		return err
	}
	if live != nil && (!(resourceIdentity{}).owned(live, plan.App) || live.tagValue("service") != operation.Service) {
		return (&AdapterError{Code: "HD0474", Message: "refuse mutation of a function outside this application's ownership"})
	}
	if operation.Kind == provider.OpDelete {
		if live == nil {
			return nil
		}
		response, err := c.request(ctx, http.MethodDelete, c.path(name), nil)
		if err != nil {
			return err
		}
		if response.StatusCode != 200 && response.StatusCode != 202 && response.StatusCode != 204 && response.StatusCode != 404 {
			return (armResponse{response: response}).failure()
		}
		response.Body.Close()
		return d.wait(ctx, name, nil)
	}
	if operation.Kind != provider.OpCreate && operation.Kind != provider.OpUpdate {
		return (&AdapterError{Code: "HD0475", Message: fmt.Sprintf("unsupported function operation %q", operation.Kind)})
	}
	service, ok := want.Service(operation.Service)
	if !ok {
		return (&AdapterError{Code: "HD0475", Message: "the planned function does not exist in the supplied spec"})
	}
	desired, err := (functionDefinition{config: c.config}).build(plan, service)
	if err != nil {
		return err
	}
	response, err := c.request(ctx, http.MethodPut, c.path(name), desired)
	if err != nil {
		return err
	}
	if response.StatusCode != 200 && response.StatusCode != 201 && response.StatusCode != 202 {
		return (armResponse{response: response}).failure()
	}
	response.Body.Close()
	return d.wait(ctx, name, &desired)
}

func (d functionDeployment) wait(ctx context.Context, name string, desired *nativeApp) error {
	c := d.client
	bounded, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for {
		app, err := c.get(bounded, name)
		if err != nil {
			return err
		}
		if desired == nil && app == nil {
			return nil
		}
		if desired != nil && app != nil {
			if (appComparison{}).ready(app) == provider.Degraded {
				return (&AdapterError{Code: "HD0476", Message: "native function provisioning failed or returned an incompatible resource kind"})
			}
			health, err := (revisionReadback{client: c}).health(bounded, app, *desired)
			if err != nil {
				return err
			}
			if health == provider.Degraded {
				return &AdapterError{Code: "HD0476", Message: "the requested native function revision failed readiness"}
			}
			if health == provider.Healthy {
				return nil
			}
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-bounded.Done():
			timer.Stop()
			return (&AdapterError{Code: "HD0476", Message: "wait for native function convergence", Cause: bounded.Err()})
		case <-timer.C:
		}
	}
}
