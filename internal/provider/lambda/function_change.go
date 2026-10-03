package lambda

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
	"github.com/d31ma/heimdall/internal/spec"
	"strconv"
)

type functionChange struct {
	adapter     *Provider
	client      *awslambda.Client
	request     executionRequest
	identity    Identity
	existing    *awslambda.GetFunctionOutput
	missing     bool
	service     spec.Service
	environment *types.Environment
	ready       *awslambda.GetFunctionOutput
	configured  *awslambda.GetFunctionOutput
	immutable   *awslambda.GetFunctionOutput
	version     string
	tags        map[string]string
	owned       *awslambda.GetFunctionOutput
}

func (c *functionChange) Run(ctx context.Context) error {
	return (sequence.Steps{Actions: []func() error{func() error { return c.read(ctx) }, c.ownership, func() error { return c.operation(ctx) }}}).Run()
}
func (c *functionChange) read(ctx context.Context) error {
	existing, err := c.client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(c.identity.Name())})
	c.existing = existing
	var missing *types.ResourceNotFoundException
	c.missing = errors.As(err, &missing)
	return c.readResult(err)
}
func (c *functionChange) readResult(err error) error {
	if c.missing {
		return nil
	}
	return err
}
func (c *functionChange) ownership() error {
	tags, readable := c.metadata(c.existing)
	owned := c.identity.Owns(tags)
	return c.requireOwnership(c.missing, readable, owned, "resource name is owned outside this HEIMDALL application")
}
func (functionChange) metadata(live *awslambda.GetFunctionOutput) (map[string]string, bool) {
	if live == nil {
		return nil, false
	}
	return live.Tags, live.TagsError == nil
}
func (functionChange) requireOwnership(missing, readable, owned bool, message string) error {
	if !missing && (!readable || !owned) {
		return (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), message)
	}
	return nil
}
func (c *functionChange) operation(ctx context.Context) error {
	steps := c.operationSteps(ctx)
	return (sequence.Steps{Actions: steps}).Run()
}
func (c *functionChange) operationSteps(ctx context.Context) []func() error {
	switch c.request.Operation.Kind {
	case provider.OpDelete:
		return []func() error{func() error { return c.remove(ctx) }}
	case provider.OpCreate, provider.OpUpdate:
		return []func() error{func() error { return c.deploy(ctx) }}
	}
	return []func() error{c.invalidOperation}
}
func (c *functionChange) invalidOperation() error {
	return (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), "unsupported Lambda operation %s", c.request.Operation.Kind)
}
func (c *functionChange) remove(ctx context.Context) error {
	steps := c.removalSteps(ctx)
	return (sequence.Steps{Actions: steps}).Run()
}
func (c *functionChange) removalSteps(ctx context.Context) []func() error {
	if c.missing {
		return nil
	}
	return []func() error{func() error { return c.delete(ctx) }, func() error { return c.adapter.waitDeleted(ctx, c.client, c.identity.Name()) }}
}
func (c *functionChange) delete(ctx context.Context) error {
	_, err := c.client.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{FunctionName: aws.String(c.identity.Name())})
	return err
}
func (c *functionChange) deploy(ctx context.Context) error {
	return (sequence.Steps{Actions: []func() error{c.desired, func() error { return c.resolveEnvironment(ctx) }, func() error { return c.write(ctx) }, func() error { return c.waitReady(ctx) }, c.converged, c.readyOwnership, func() error { return c.publish(ctx) }, c.immutableVersion, func() error { return c.waitImmutable(ctx) }, c.immutableMatches, c.prepareTags, func() error { return c.readOwnership(ctx) }, c.tagOwnership, func() error { return c.tag(ctx) }, func() error { return c.adapter.waitMetadata(ctx, c.client, c.identity, c.tags) }}}).Run()
}
func (c *functionChange) desired() error {
	service, found := c.request.Desired.Service(c.request.Operation.Service)
	c.service = service
	return c.requireService(found)
}
func (functionChange) requireService(found bool) error {
	if !found {
		return (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), "operation names a function absent from desired state")
	}
	return nil
}
func (c *functionChange) resolveEnvironment(ctx context.Context) error {
	environment, err := c.adapter.environment(ctx, c.service)
	c.environment = environment
	return err
}
func (c *functionChange) write(ctx context.Context) error {
	steps := c.writeSteps(ctx)
	return (sequence.Steps{Actions: steps}).Run()
}
func (c *functionChange) writeSteps(ctx context.Context) []func() error {
	if c.missing {
		return []func() error{c.creationTags, func() error { return c.create(ctx) }}
	}
	return []func() error{c.defaultFunction, func() error { return c.configure(ctx) }, func() error { return c.waitConfigured(ctx) }, c.configuredOwnership, func() error { return c.code(ctx) }}
}
func (c *functionChange) creationTags() error {
	tags, err := c.identity.Tags(c.request.Plan, c.service, "")
	c.tags = tags
	return err
}
func (c *functionChange) imageConfig() *types.ImageConfig {
	return &types.ImageConfig{EntryPoint: c.service.Entrypoint, Command: c.service.Command, WorkingDirectory: aws.String("")}
}
func (c *functionChange) create(ctx context.Context) error {
	sizing := FunctionState{}
	_, err := c.client.CreateFunction(ctx, &awslambda.CreateFunctionInput{FunctionName: aws.String(c.identity.Name()), PackageType: types.PackageTypeImage, Code: &types.FunctionCode{ImageUri: aws.String(c.service.Image)}, Role: aws.String(c.request.Target.Config[(TargetContract{}).ExecutionRoleKey()]), Timeout: aws.Int32(sizing.Timeout(c.service)), MemorySize: aws.Int32(sizing.Memory(c.service)), Architectures: []types.Architecture{sizing.Architecture(c.service)}, Environment: c.environment, ImageConfig: c.imageConfig(), Tags: c.tags})
	return err
}
func (c *functionChange) defaultFunction() error {
	configuration := (FunctionState{}).configuration(c.existing)
	present := c.presentConfiguration()
	return c.requireDefault(configuration, present)
}
func (c *functionChange) presentConfiguration() bool {
	return c.existing != nil && c.existing.Configuration != nil
}
func (functionChange) requireDefault(configuration types.FunctionConfiguration, present bool) error {
	if !present || configuration.CapacityProviderConfig != nil || configuration.DurableConfig != nil {
		return (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), "default Lambda cannot update a managed-instance or durable function")
	}
	return nil
}
func (c *functionChange) configure(ctx context.Context) error {
	sizing := FunctionState{}
	_, err := c.client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{FunctionName: aws.String(c.identity.Name()), RevisionId: c.existing.Configuration.RevisionId, Role: aws.String(c.request.Target.Config[(TargetContract{}).ExecutionRoleKey()]), Timeout: aws.Int32(sizing.Timeout(c.service)), MemorySize: aws.Int32(sizing.Memory(c.service)), Environment: c.environment, ImageConfig: c.imageConfig()})
	return err
}
func (c *functionChange) waitConfigured(ctx context.Context) error {
	configured, err := c.adapter.waitReady(ctx, c.client, c.identity.Name(), "")
	c.configured = configured
	return err
}
func (c *functionChange) configuredOwnership() error {
	tags, readable := c.metadata(c.configured)
	owned := c.identity.Owns(tags)
	return c.requireOwnership(false, readable, owned, "Lambda ownership changed before code update; re-plan")
}
func (c *functionChange) code(ctx context.Context) error {
	_, err := c.client.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{FunctionName: aws.String(c.identity.Name()), RevisionId: c.configured.Configuration.RevisionId, ImageUri: aws.String(c.service.Image), Architectures: []types.Architecture{(FunctionState{}).Architecture(c.service)}})
	return err
}
func (c *functionChange) waitReady(ctx context.Context) error {
	ready, err := c.adapter.waitReady(ctx, c.client, c.identity.Name(), "")
	c.ready = ready
	return err
}
func (c *functionChange) converged() error {
	image := (FunctionState{}).matchesImage(c.ready, c.service)
	configuration := (FunctionState{}).matchesConfiguration(c.ready.Configuration, c.request.Target, c.service)
	matches := (FunctionState{}).all([]bool{image, configuration})
	return c.requireMatch(matches, "native Lambda code/configuration did not converge")
}
func (functionChange) requireMatch(matches bool, message string) error {
	if !matches {
		return (Diagnostic{}).Format((Diagnostic{}).ReadinessCode(), message)
	}
	return nil
}
func (c *functionChange) readyOwnership() error {
	tags, readable := c.metadata(c.ready)
	owned := c.identity.Owns(tags)
	return c.requireOwnership(false, readable, owned, "Lambda ownership changed before publication; re-plan")
}
func (c *functionChange) publish(ctx context.Context) error {
	version, err := (publication{client: c.client, identity: c.identity, ready: c.ready, target: c.request.Target, service: c.service}).Version(ctx)
	c.version = version
	return err
}
func (c *functionChange) immutableVersion() error {
	number, err := strconv.Atoi(c.version)
	return c.requireVersion(number, err)
}
func (functionChange) requireVersion(number int, err error) error {
	if err != nil || number < 1 {
		return (Diagnostic{}).Format((Diagnostic{}).ReadinessCode(), "Lambda did not return an immutable published version")
	}
	return nil
}
func (c *functionChange) waitImmutable(ctx context.Context) error {
	immutable, err := c.adapter.waitReady(ctx, c.client, c.identity.Name(), c.version)
	c.immutable = immutable
	return err
}
func (c *functionChange) immutableMatches() error {
	matches := (FunctionState{Live: c.ready, Published: c.immutable}).Matches(c.request.Target, c.service)
	return c.requireMatch(matches, "published Lambda configuration did not converge")
}
func (c *functionChange) prepareTags() error {
	tags, err := c.identity.Tags(c.request.Plan, c.service, c.version)
	c.tags = tags
	return err
}
func (c *functionChange) readOwnership(ctx context.Context) error {
	owned, err := c.client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(c.identity.Name())})
	c.owned = owned
	return err
}
func (c *functionChange) tagOwnership() error {
	tags, readable := c.metadata(c.owned)
	owned := c.identity.Owns(tags)
	return c.requireOwnership(false, readable, owned, "Lambda ownership changed before metadata update; re-plan")
}
func (c *functionChange) tag(ctx context.Context) error {
	_, err := c.client.TagResource(ctx, &awslambda.TagResourceInput{Resource: c.owned.Configuration.FunctionArn, Tags: c.tags})
	return err
}
