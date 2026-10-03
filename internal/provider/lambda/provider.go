package lambda

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
	"io"
)

type Provider struct {
	SecretResolver    func(context.Context, string) (string, error)
	EndpointOverride  string
	StaticCredentials aws.CredentialsProvider
	HTTPClient        aws.HTTPClient
}

func (p *Provider) Name() string                        { return "lambda" }
func (p *Provider) Capabilities() provider.Capabilities { return (capabilitySet{}).Build() }
func (p *Provider) ValidateTargetDefinition(target provider.Target) error {
	return (TargetContract{Target: target}).ValidateDefinition()
}
func (p *Provider) Plan(ctx context.Context, target provider.Target, want spec.DeploySpec) (provider.Plan, error) {
	return (&planner{adapter: p, target: target, want: want}).Plan(ctx)
}
func (p *Provider) Apply(ctx context.Context, target provider.Target, plan provider.Plan) (provider.Result, error) {
	return (&applier{adapter: p, target: target, plan: plan}).Apply(ctx)
}
func (p *Provider) Observe(ctx context.Context, target provider.Target, app provider.AppRef) (provider.LiveState, error) {
	return (&observer{adapter: p, target: target, app: app}).Observe(ctx)
}
func (p *Provider) Instances(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Instance, error) {
	_, err := p.Observe(ctx, target, app)
	return nil, err
}
func (p *Provider) Metrics(_ context.Context, _ provider.Target, _ provider.InstanceRef, _ provider.Window) (provider.Series, error) {
	return provider.Series{}, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "Lambda exposes function/version invocation metrics, not per-container CPU or memory series")
}
func (p *Provider) Logs(_ context.Context, _ provider.Target, _ provider.InstanceRef, _ provider.LogFilter) (io.ReadCloser, error) {
	return nil, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "Lambda logs require a native CloudWatch function/version stream; execution environments have no stable instance IDs")
}
func (p *Provider) Events(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Event, error) {
	_, err := p.Observe(ctx, target, app)
	return nil, err
}
func (p *Provider) FunctionMetrics(ctx context.Context, target provider.Target, ref provider.FunctionRef, window provider.Window) (provider.FunctionSeries, error) {
	return (functionObservation{adapter: p, target: target, ref: ref}).metrics(ctx, window)
}
func (p *Provider) FunctionLogs(ctx context.Context, target provider.Target, ref provider.FunctionRef, filter provider.LogFilter) (io.ReadCloser, error) {
	return (functionObservation{adapter: p, target: target, ref: ref}).logs(ctx, filter)
}
func (p *Provider) Invoke(ctx context.Context, request provider.InvocationRequest) (provider.Invocation, error) {
	return (&invocation{adapter: p, request: request}).Invoke(ctx)
}
func (p *Provider) configuration(ctx context.Context, target provider.Target) (aws.Config, error) {
	return (&clientFactory{adapter: p, target: target}).Configuration(ctx)
}
func (p *Provider) client(ctx context.Context, target provider.Target) (*awslambda.Client, error) {
	return (&clientFactory{adapter: p, target: target}).Client(ctx)
}
func (p *Provider) functions(ctx context.Context, client *awslambda.Client, app provider.AppRef) (map[string]FunctionState, error) {
	return (&inventory{client: client, app: app}).Read(ctx)
}
func (p *Provider) waitReady(ctx context.Context, client *awslambda.Client, name, version string) (*awslambda.GetFunctionOutput, error) {
	return (&readiness{client: client, name: name, version: version}).Wait(ctx)
}
func (p *Provider) waitDeleted(ctx context.Context, client *awslambda.Client, name string) error {
	return (&deletion{client: client, name: name}).Wait(ctx)
}
func (p *Provider) waitMetadata(ctx context.Context, client *awslambda.Client, identity Identity, tags map[string]string) error {
	return (&metadata{client: client, identity: identity, tags: tags}).Wait(ctx)
}
func (p *Provider) environment(ctx context.Context, service spec.Service) (*types.Environment, error) {
	return (&environment{resolver: p.SecretResolver, service: service}).Build(ctx)
}

func (p *Provider) ValidateDesired(target provider.Target, want spec.DeploySpec) error {
	return (RuntimeContract{Target: target, Spec: want}).Validate()
}
