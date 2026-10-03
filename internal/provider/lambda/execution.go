package lambda

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/internal/provider"
	"strconv"
)

type execution struct {
	adapter *Provider
	client  *awslambda.Client
	request executionRequest
}

func (operation execution) Run(ctx context.Context) error {
	return operation.run(ctx)
}

func (operation execution) run(ctx context.Context) error {
	client, target, plan, want, op := operation.client, operation.request.Target, operation.request.Plan, operation.request.Desired, operation.request.Operation

	id := Identity{App: plan.App, Service: op.Service}
	existing, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(id.Name())})
	var missing *types.ResourceNotFoundException
	if err != nil && !errors.As(err, &missing) {
		return err
	}
	if err == nil && (existing.TagsError != nil || !id.Owns(existing.Tags)) {
		return (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), "resource name is owned outside this HEIMDALL application")
	}
	if op.Kind == provider.OpDelete {
		if missing != nil {
			return nil
		}
		_, err = client.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{FunctionName: aws.String(id.Name())})
		if err != nil {
			return err
		}
		return operation.adapter.waitDeleted(ctx, client, id.Name())
	}
	if op.Kind != provider.OpCreate && op.Kind != provider.OpUpdate {
		return (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), "unsupported Lambda operation %s", op.Kind)
	}
	s, ok := want.Service(op.Service)
	if !ok {
		return (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), "operation names a function absent from desired state")
	}
	env, err := operation.adapter.environment(ctx, s)
	if err != nil {
		return err
	}
	sizing := FunctionState{}
	imageConfig := &types.ImageConfig{EntryPoint: s.Entrypoint, Command: s.Command, WorkingDirectory: aws.String("")}
	if missing != nil {
		tags, err := id.Tags(plan, s, "")
		if err != nil {
			return err
		}
		_, err = client.CreateFunction(ctx, &awslambda.CreateFunctionInput{FunctionName: aws.String(id.Name()), PackageType: types.PackageTypeImage, Code: &types.FunctionCode{ImageUri: aws.String(s.Image)}, Role: aws.String(target.Config[(TargetContract{}).ExecutionRoleKey()]), Timeout: aws.Int32(sizing.Timeout(s)), MemorySize: aws.Int32(sizing.Memory(s)), Architectures: []types.Architecture{sizing.Architecture(s)}, Environment: env, ImageConfig: imageConfig, Tags: tags})
		if err != nil {
			return err
		}
	} else {
		if existing.Configuration == nil || existing.Configuration.CapacityProviderConfig != nil || existing.Configuration.DurableConfig != nil {
			return (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), "default Lambda cannot update a managed-instance or durable function")
		}
		_, err = client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{FunctionName: aws.String(id.Name()), RevisionId: existing.Configuration.RevisionId, Role: aws.String(target.Config[(TargetContract{}).ExecutionRoleKey()]), Timeout: aws.Int32(sizing.Timeout(s)), MemorySize: aws.Int32(sizing.Memory(s)), Environment: env, ImageConfig: imageConfig})
		if err != nil {
			return err
		}
		configured, err := operation.adapter.waitReady(ctx, client, id.Name(), "")
		if err != nil {
			return err
		}
		if configured.TagsError != nil || !id.Owns(configured.Tags) {
			return (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), "Lambda ownership changed before code update; re-plan")
		}
		_, err = client.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{FunctionName: aws.String(id.Name()), RevisionId: configured.Configuration.RevisionId, ImageUri: aws.String(s.Image), Architectures: []types.Architecture{sizing.Architecture(s)}})
		if err != nil {
			return err
		}
	}
	ready, err := operation.adapter.waitReady(ctx, client, id.Name(), "")
	if err != nil {
		return err
	}
	if !(FunctionState{}).matchesImage(ready, s) || !(FunctionState{}).matchesConfiguration(ready.Configuration, target, s) {
		return (Diagnostic{}).Format((Diagnostic{}).ReadinessCode(), "native Lambda code/configuration did not converge")
	}
	if ready.TagsError != nil || !id.Owns(ready.Tags) {
		return (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), "Lambda ownership changed before publication; re-plan")
	}
	version, err := (publication{client: client, identity: id, ready: ready, target: target, service: s}).Version(ctx)
	if err != nil {
		return err
	}
	if number, err := strconv.Atoi(version); err != nil || number < 1 {
		return (Diagnostic{}).Format((Diagnostic{}).ReadinessCode(), "Lambda did not return an immutable published version")
	}
	immutable, err := operation.adapter.waitReady(ctx, client, id.Name(), version)
	if err != nil {
		return err
	}
	if !(FunctionState{Live: ready, Published: immutable}).Matches(target, s) {
		return (Diagnostic{}).Format((Diagnostic{}).ReadinessCode(), "published Lambda configuration did not converge")
	}
	tags, err := id.Tags(plan, s, version)
	if err != nil {
		return err
	}
	owned, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(id.Name())})
	if err != nil {
		return err
	}
	if owned.TagsError != nil || !id.Owns(owned.Tags) {
		return (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), "Lambda ownership changed before metadata update; re-plan")
	}
	_, err = client.TagResource(ctx, &awslambda.TagResourceInput{Resource: owned.Configuration.FunctionArn, Tags: tags})
	if err != nil {
		return err
	}
	return operation.adapter.waitMetadata(ctx, client, id, tags)
}
