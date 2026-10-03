package azurefunctions

import (
	"context"
	"io"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

type Provider struct {
	Credential    azcore.TokenCredential
	ClientOptions *arm.ClientOptions
}

func (p *Provider) Name() string                        { return "azurefunctions" }
func (p *Provider) Capabilities() provider.Capabilities { return (capabilitySet{}).build() }
func (p *Provider) Plan(ctx context.Context, target provider.Target, want spec.DeploySpec) (provider.Plan, error) {
	return (planner{provider: p}).plan(ctx, target, want)
}
func (p *Provider) Apply(ctx context.Context, target provider.Target, plan provider.Plan) (provider.Result, error) {
	return (applier{provider: p}).apply(ctx, target, plan)
}
func (p *Provider) Observe(ctx context.Context, target provider.Target, app provider.AppRef) (provider.LiveState, error) {
	return (observer{provider: p}).observe(ctx, target, app)
}
func (p *Provider) Instances(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Instance, error) {
	return (observer{provider: p}).instances(ctx, target, app)
}
func (p *Provider) Metrics(_ context.Context, _ provider.Target, _ provider.InstanceRef, _ provider.Window) (provider.Series, error) {
	return provider.Series{}, &AdapterError{Code: "HD0477", Message: "native function invocation metrics require an Azure Monitor function/revision scope; per-container metrics are unavailable"}
}
func (p *Provider) Logs(_ context.Context, _ provider.Target, _ provider.InstanceRef, _ provider.LogFilter) (io.ReadCloser, error) {
	return nil, &AdapterError{Code: "HD0477", Message: "native function invocation logs require Application Insights or environment logging with a function/revision scope"}
}
func (p *Provider) Events(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Event, error) {
	return (observer{provider: p}).events(ctx, target, app)
}
