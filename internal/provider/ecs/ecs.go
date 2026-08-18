// Package ecs deploys compose applications to Amazon ECS on Fargate.
//
// It is the first cloud adapter, and it follows the rules that keep a cloud
// from leaking: nothing outside this package imports an AWS SDK, credentials
// are references resolved for the duration of one call, and every compose
// directive the platform cannot honour is rejected at plan time with the
// offending service named.
//
// Mapping: one compose service becomes one ECS service running one task
// definition with one container. HEIMDALL's identity labels ride as resource
// tags on the service, exactly as they ride as Docker labels elsewhere — the
// tags are how live state names its owner, revision, and content hash.
package ecs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

// Tag keys mirror the Docker label keys, dots and all — one vocabulary for
// "whose is this" across every runtime.
const (
	tagManagedBy   = "dev.delma.heimdall.managed-by"
	tagProject     = "dev.delma.heimdall.project"
	tagApp         = "dev.delma.heimdall.app"
	tagService     = "dev.delma.heimdall.service"
	tagRevision    = "dev.delma.heimdall.revision"
	tagServiceHash = "dev.delma.heimdall.service-hash"
	tagSpecHash    = "dev.delma.heimdall.spec-hash"

	managedBy = "heimdall"
)

// Provider is the adapter. Endpoint overrides exist so the conformance suite
// runs against a fake AWS; production leaves them empty.
type Provider struct {
	SecretResolver func(ctx context.Context, ref string) (string, error)

	// EndpointOverride points every AWS client at one URL. Tests only.
	EndpointOverride string
	// StaticCredentials bypasses the credential chain. Tests only.
	StaticCredentials aws.CredentialsProvider

	// httpClient is shared across every call: credentials resolve per call,
	// but the connection pool must not — the SDK otherwise builds a fresh
	// client and transport per LoadDefaultConfig, and a dropped transport
	// never returns its keep-alive sockets.
	httpOnce   sync.Once
	httpClient *awshttp.BuildableClient
}

func (p *Provider) Name() string { return "ecs" }

func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Provider: "ecs",
		Support: map[provider.Feature]provider.Support{
			provider.FeatureMultiService: provider.Full,
			provider.FeatureReplicas:     provider.Full,
			provider.FeatureResources:    provider.Full,
			provider.FeatureSecretRef:    provider.Full,
			provider.FeatureFileSecret:   provider.Rejected,
			provider.FeatureHealthcheck:  provider.Full,
			provider.FeatureMultiPort:    provider.Full,
			// awsvpc networking exposes the container port on the task's own
			// interface; there is no host-port remapping to honour.
			provider.FeaturePorts:     provider.Partial,
			provider.FeatureDependsOn: provider.Partial,
			// Fargate restarts essential containers as service scheduling;
			// compose's "no" and "on-failure" cannot be expressed.
			provider.FeatureRestart: provider.Partial,
			// EFS integration is real work with real IAM; until a slice needs
			// it, a named volume is rejected rather than half-mounted.
			provider.FeatureNamedVolume: provider.Rejected,
			provider.FeatureBindMount:   provider.Rejected,
			provider.FeatureSidecars:    provider.Rejected,
			provider.FeatureScaleToZero: provider.Rejected,
		},
		Caveats: map[provider.Feature]string{
			provider.FeatureFileSecret:  "mount secrets through the task definition is future work; use an environment ${secret:...} reference",
			provider.FeaturePorts:       "awsvpc networking publishes the container port on the task interface; the host side of a mapping is ignored",
			provider.FeatureDependsOn:   "ECS orders service creation by wave; task start order belongs to the scheduler",
			provider.FeatureRestart:     "Fargate always restarts an exited essential container; 'no' and 'on-failure' cannot be honoured",
			provider.FeatureNamedVolume: "named volumes need EFS, which is not wired yet",
			provider.FeatureBindMount:   "there is no host to bind-mount on Fargate",
			provider.FeatureSidecars:    "declare a sidecar as its own service",
			provider.FeatureScaleToZero: "ECS keeps desiredCount running; use Cloud Run or Container Apps",
		},
	}
}

// clients builds the AWS clients for one call. Credentials resolve here — at
// call time, through a reference — and exist only inside the returned
// clients.
func (p *Provider) clients(ctx context.Context, target provider.Target) (*awsecs.Client, *cloudwatch.Client, *cloudwatchlogs.Client, error) {
	region := target.Region
	if region == "" {
		return nil, nil, nil, fmt.Errorf("HD0350: ECS target %s names no region", target.ID)
	}

	p.httpOnce.Do(func() { p.httpClient = awshttp.NewBuildableClient() })
	options := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
		awsconfig.WithHTTPClient(p.httpClient),
	}
	switch {
	case p.StaticCredentials != nil:
		options = append(options, awsconfig.WithCredentialsProvider(p.StaticCredentials))
	case target.CredentialRef != "":
		if p.SecretResolver == nil {
			return nil, nil, nil, fmt.Errorf("HD0351: target %s names credential %q but no resolver is configured",
				target.ID, target.CredentialRef)
		}
		raw, err := p.SecretResolver(ctx, target.CredentialRef)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("HD0351: resolve credential %q: %w", target.CredentialRef, err)
		}
		var parsed struct {
			AccessKeyID     string `json:"access_key_id"`
			SecretAccessKey string `json:"secret_access_key"`
			SessionToken    string `json:"session_token"`
		}
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil || parsed.AccessKeyID == "" {
			return nil, nil, nil, fmt.Errorf(
				"HD0351: credential %q is not the JSON {access_key_id, secret_access_key} an ECS target needs",
				target.CredentialRef)
		}
		options = append(options, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(parsed.AccessKeyID, parsed.SecretAccessKey, parsed.SessionToken)))
	}
	// No ref and no override: the default chain — instance role, IRSA, env —
	// which is how a control plane running in AWS is expected to authenticate.

	configuration, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("HD0352: load AWS configuration: %w", err)
	}

	endpoint := p.EndpointOverride
	ecsClient := awsecs.NewFromConfig(configuration, func(o *awsecs.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	cwClient := cloudwatch.NewFromConfig(configuration, func(o *cloudwatch.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	logsClient := cloudwatchlogs.NewFromConfig(configuration, func(o *cloudwatchlogs.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	return ecsClient, cwClient, logsClient, nil
}

func clusterOf(target provider.Target) string {
	if target.Endpoint != "" {
		return target.Endpoint
	}
	return "default"
}

func projectOf(target provider.Target) string {
	if target.Project != "" {
		return target.Project
	}
	if target.Region != "" {
		return target.Region
	}
	return "default"
}

// resourceName is the ECS service and task-definition family name.
func resourceName(app provider.AppRef, service string) string {
	return fmt.Sprintf("hd-%s-%s-%s", app.Project, app.App, service)
}

// liveService is one deployed ECS service with its identity tags decoded.
type liveService struct {
	arn         string
	name        string
	tags        map[string]string
	running     int
	desired     int
	status      string
	taskDefined string
	events      []ecstypes.ServiceEvent
	image       string
}

// servicesFor lists this application's services by tag, the same ownership
// filter every other adapter applies.
func (p *Provider) servicesFor(ctx context.Context, client *awsecs.Client, target provider.Target, app provider.AppRef) (map[string]liveService, error) {
	cluster := clusterOf(target)
	listed, err := client.ListServices(ctx, &awsecs.ListServicesInput{Cluster: aws.String(cluster)})
	if err != nil {
		return nil, fmt.Errorf("HD0353: list ECS services in %s: %w", cluster, err)
	}
	if len(listed.ServiceArns) == 0 {
		return map[string]liveService{}, nil
	}

	described, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster: aws.String(cluster), Services: listed.ServiceArns,
		Include: []ecstypes.ServiceField{ecstypes.ServiceFieldTags},
	})
	if err != nil {
		return nil, fmt.Errorf("HD0353: describe ECS services: %w", err)
	}

	out := map[string]liveService{}
	for _, service := range described.Services {
		tags := map[string]string{}
		for _, tag := range service.Tags {
			tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
		}
		if tags[tagManagedBy] != managedBy || tags[tagProject] != app.Project || tags[tagApp] != app.App {
			continue
		}
		name := tags[tagService]
		if name == "" {
			continue
		}
		out[name] = liveService{
			arn:  aws.ToString(service.ServiceArn),
			name: aws.ToString(service.ServiceName),
			tags: tags, running: int(service.RunningCount), desired: int(service.DesiredCount),
			status:      aws.ToString(service.Status),
			taskDefined: aws.ToString(service.TaskDefinition),
			events:      service.Events,
		}
	}
	return out, nil
}

func (p *Provider) Plan(ctx context.Context, target provider.Target, want spec.DeploySpec) (provider.Plan, error) {
	app := provider.AppRef{Project: projectOf(target), App: want.App}

	if err := provider.Validate(p.Capabilities(), want); err != nil {
		return provider.Plan{}, err
	}
	specHash, err := spec.Hash(want)
	if err != nil {
		return provider.Plan{}, err
	}
	client, _, _, err := p.clients(ctx, target)
	if err != nil {
		return provider.Plan{}, err
	}
	live, err := p.servicesFor(ctx, client, target, app)
	if err != nil {
		return provider.Plan{}, err
	}

	plan := provider.Plan{Target: target.ID, App: app, Revision: want.Revision, SpecHash: specHash}
	desired := map[string]bool{}
	for _, service := range want.Services {
		desired[service.Name] = true
		serviceHash, err := spec.HashService(service)
		if err != nil {
			return provider.Plan{}, err
		}
		existing, running := live[service.Name]
		switch {
		case !running || existing.status == "INACTIVE":
			plan.Operations = append(plan.Operations, provider.Operation{
				Kind: provider.OpCreate, Service: service.Name, Wave: service.Wave, Reason: "not deployed",
			})
		case existing.tags[tagServiceHash] != serviceHash:
			plan.Operations = append(plan.Operations, provider.Operation{
				Kind: provider.OpUpdate, Service: service.Name, Wave: service.Wave,
				Reason: "the desired service differs from the deployed one",
			})
		default:
			plan.Operations = append(plan.Operations, provider.Operation{
				Kind: provider.OpNoop, Service: service.Name, Wave: service.Wave,
			})
		}
	}
	for name := range live {
		if !desired[name] {
			plan.Operations = append(plan.Operations, provider.Operation{
				Kind: provider.OpDelete, Service: name, Prune: true,
				Reason: "no longer declared in the desired state",
			})
		}
	}
	sort.SliceStable(plan.Operations, func(i, j int) bool {
		if plan.Operations[i].Wave != plan.Operations[j].Wave {
			return plan.Operations[i].Wave < plan.Operations[j].Wave
		}
		return plan.Operations[i].Service < plan.Operations[j].Service
	})
	return plan, nil
}

// ApplyOptions mirrors the Docker adapter's, threaded the same way.
type ApplyOptions struct {
	Spec       spec.DeploySpec
	Prune      bool
	Registries provider.RegistryResolver
}

type applyKey struct{}

func WithApply(ctx context.Context, options ApplyOptions) context.Context {
	return context.WithValue(ctx, applyKey{}, options)
}

func applyOptions(ctx context.Context) (ApplyOptions, bool) {
	options, ok := ctx.Value(applyKey{}).(ApplyOptions)
	return options, ok
}

func (p *Provider) Apply(ctx context.Context, target provider.Target, plan provider.Plan) (provider.Result, error) {
	options, ok := applyOptions(ctx)
	if !ok {
		return provider.Result{}, fmt.Errorf("HD0354: apply called without options; use WithApply")
	}
	specHash, err := spec.Hash(options.Spec)
	if err != nil {
		return provider.Result{}, err
	}
	if specHash != plan.SpecHash {
		return provider.Result{}, fmt.Errorf(
			"HD0355: the plan was computed for spec %s but apply was asked to run %s; re-plan",
			plan.SpecHash, specHash)
	}

	client, _, _, err := p.clients(ctx, target)
	if err != nil {
		return provider.Result{}, err
	}
	live, err := p.servicesFor(ctx, client, target, plan.App)
	if err != nil {
		return provider.Result{}, err
	}
	services := map[string]spec.Service{}
	for _, service := range options.Spec.Services {
		services[service.Name] = service
	}

	result := provider.Result{}
	operations := plan.Operations
	for start := 0; start < len(operations); {
		wave := operations[start].Wave
		end := start
		for end < len(operations) && operations[end].Wave == wave {
			end++
		}
		waveFailed := false
		for _, operation := range operations[start:end] {
			if operation.Prune && !options.Prune {
				continue
			}
			if operation.Kind == provider.OpNoop {
				continue
			}
			if err := p.execute(ctx, client, target, plan, operation, services, live); err != nil {
				result.Failures = map[string]string{operation.Service: err.Error()}
				waveFailed = true
				break
			}
			result.Applied = append(result.Applied, operation)
		}
		if waveFailed {
			break
		}
		start = end
	}
	return result, nil
}

func (p *Provider) execute(
	ctx context.Context,
	client *awsecs.Client,
	target provider.Target,
	plan provider.Plan,
	operation provider.Operation,
	services map[string]spec.Service,
	live map[string]liveService,
) error {
	cluster := clusterOf(target)
	switch operation.Kind {
	case provider.OpDelete:
		existing, ok := live[operation.Service]
		if !ok {
			return nil
		}
		_, err := client.DeleteService(ctx, &awsecs.DeleteServiceInput{
			Cluster: aws.String(cluster), Service: aws.String(existing.name), Force: aws.Bool(true),
		})
		return err

	case provider.OpCreate, provider.OpUpdate, provider.OpRestart:
		service, ok := services[operation.Service]
		if !ok {
			return fmt.Errorf("HD0356: the plan names service %q but the spec does not", operation.Service)
		}
		taskDefinition, err := p.registerTaskDefinition(ctx, client, target, plan, service)
		if err != nil {
			return err
		}
		tags, err := p.serviceTags(plan, service)
		if err != nil {
			return err
		}
		replicas := int32(service.Replicas)
		if replicas == 0 {
			replicas = 1
		}

		if existing, running := live[operation.Service]; running && existing.status != "INACTIVE" {
			if _, err := client.UpdateService(ctx, &awsecs.UpdateServiceInput{
				Cluster: aws.String(cluster), Service: aws.String(existing.name),
				TaskDefinition: aws.String(taskDefinition), DesiredCount: aws.Int32(replicas),
			}); err != nil {
				return err
			}
			// UpdateService cannot change tags; the hash tags must move with
			// the deploy or the next plan sees stale state forever.
			_, err := client.TagResource(ctx, &awsecs.TagResourceInput{
				ResourceArn: aws.String(existing.arn), Tags: tags,
			})
			return err
		}

		balancers, err := loadBalancersFor(target, service, services)
		if err != nil {
			return err
		}

		input := &awsecs.CreateServiceInput{
			Cluster:        aws.String(cluster),
			ServiceName:    aws.String(resourceName(plan.App, service.Name)),
			TaskDefinition: aws.String(taskDefinition),
			DesiredCount:   aws.Int32(replicas),
			LaunchType:     ecstypes.LaunchTypeFargate,
			Tags:           tags,
			LoadBalancers:  balancers,
		}
		if subnets := strings.Split(target.Config["subnets"], ","); subnets[0] != "" {
			assignPublic := ecstypes.AssignPublicIpDisabled
			if target.Config["assign_public_ip"] == "true" {
				assignPublic = ecstypes.AssignPublicIpEnabled
			}
			input.NetworkConfiguration = &ecstypes.NetworkConfiguration{
				AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
					Subnets:        subnets,
					SecurityGroups: nonEmpty(strings.Split(target.Config["security_groups"], ",")),
					AssignPublicIp: assignPublic,
				},
			}
		}
		_, err = client.CreateService(ctx, input)
		return err
	}
	return nil
}

// loadBalancersFor registers a service into the target's load-balancer
// target group. Infrastructure owns the load balancer and the target group —
// Terraform or its peer creates them and hands the ARN to the target as
// `target_group_arn` config; the adapter's job ends at attaching the one
// service that fronts it, on creation.
//
// ponytail: attach on CreateService only — a service that existed before the
// target group did needs recreating to pick it up; wire UpdateService's
// LoadBalancers field when that recreation becomes a real operational cost.
func loadBalancersFor(target provider.Target, service spec.Service, all map[string]spec.Service) ([]ecstypes.LoadBalancer, error) {
	arn := target.Config["target_group_arn"]
	if arn == "" {
		return nil, nil
	}
	named := target.Config["load_balanced_service"]
	switch {
	case named == "":
		// Unambiguous only when exactly one service publishes a port.
		ported := 0
		for _, candidate := range all {
			if len(candidate.Ports) > 0 {
				ported++
			}
		}
		if ported > 1 {
			return nil, fmt.Errorf(
				"HD0363: target %s carries target_group_arn and %d services publish ports; name the fronted one in load_balanced_service",
				target.ID, ported)
		}
	case named != service.Name:
		return nil, nil
	case len(service.Ports) == 0:
		return nil, fmt.Errorf(
			"HD0363: target %s names %s as load_balanced_service but it publishes no port",
			target.ID, service.Name)
	}
	if len(service.Ports) == 0 {
		return nil, nil
	}
	return []ecstypes.LoadBalancer{{
		TargetGroupArn: aws.String(arn),
		ContainerName:  aws.String(service.Name),
		ContainerPort:  aws.Int32(int32(service.Ports[0].Target)),
	}}, nil
}

func nonEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

// registerTaskDefinition builds the Fargate task for one compose service.
// Secrets resolve here, into the container environment, and nowhere else.
func (p *Provider) registerTaskDefinition(
	ctx context.Context,
	client *awsecs.Client,
	target provider.Target,
	plan provider.Plan,
	service spec.Service,
) (string, error) {
	var environment []ecstypes.KeyValuePair
	for _, env := range service.Env {
		value := env.Value
		if env.Ref != "" {
			if p.SecretResolver == nil {
				return "", fmt.Errorf("HD0357: %s needs secret %q but no resolver is configured", service.Name, env.Ref)
			}
			resolved, err := p.SecretResolver(ctx, env.Ref)
			if err != nil {
				return "", fmt.Errorf("HD0358: resolve secret %q for %s: %w", env.Ref, service.Name, err)
			}
			value = resolved
		}
		environment = append(environment, ecstypes.KeyValuePair{
			Name: aws.String(env.Key), Value: aws.String(value),
		})
	}

	var ports []ecstypes.PortMapping
	for _, port := range service.Ports {
		protocol := ecstypes.TransportProtocolTcp
		if port.Protocol == "udp" {
			protocol = ecstypes.TransportProtocolUdp
		}
		ports = append(ports, ecstypes.PortMapping{
			ContainerPort: aws.Int32(int32(port.Target)), Protocol: protocol,
		})
	}

	cpu, memory := fargateSize(service)
	container := ecstypes.ContainerDefinition{
		Name:         aws.String(service.Name),
		Image:        aws.String(service.Image),
		Essential:    aws.Bool(true),
		Environment:  environment,
		PortMappings: ports,
	}
	if group := target.Config["log_group"]; group != "" {
		container.LogConfiguration = &ecstypes.LogConfiguration{
			LogDriver: ecstypes.LogDriverAwslogs,
			Options: map[string]string{
				"awslogs-group":         group,
				"awslogs-region":        target.Region,
				"awslogs-stream-prefix": resourceName(plan.App, service.Name),
			},
		}
	}
	if service.Healthcheck != nil && len(service.Healthcheck.Test) > 0 {
		container.HealthCheck = &ecstypes.HealthCheck{Command: service.Healthcheck.Test}
	}

	input := &awsecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(resourceName(plan.App, service.Name)),
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		Cpu:                     aws.String(cpu),
		Memory:                  aws.String(memory),
		ContainerDefinitions:    []ecstypes.ContainerDefinition{container},
	}
	if role := target.Config["execution_role_arn"]; role != "" {
		input.ExecutionRoleArn = aws.String(role)
	}

	registered, err := client.RegisterTaskDefinition(ctx, input)
	if err != nil {
		return "", fmt.Errorf("HD0359: register task definition for %s: %w", service.Name, err)
	}
	return aws.ToString(registered.TaskDefinition.TaskDefinitionArn), nil
}

// fargateSize snaps compose resource requests onto Fargate's tiers — the
// resource-tier snapping provider.Validate already promises.
func fargateSize(service spec.Service) (cpu, memory string) {
	requestedCPU := 256
	requestedMemory := 512
	if service.Resources != nil {
		if service.Resources.CPUMillis > 0 {
			// Fargate CPU units are 1024ths of a vCPU; compose millis are
			// 1000ths. Scale rather than pretending they are the same unit.
			requestedCPU = service.Resources.CPUMillis * 1024 / 1000
		}
		if service.Resources.MemoryMiB > 0 {
			requestedMemory = service.Resources.MemoryMiB
		}
	}
	for _, tier := range []struct{ cpu, minMem, maxMem int }{
		{256, 512, 2048}, {512, 1024, 4096}, {1024, 2048, 8192},
		{2048, 4096, 16384}, {4096, 8192, 30720},
	} {
		if requestedCPU <= tier.cpu {
			memoryMB := requestedMemory
			if memoryMB < tier.minMem {
				memoryMB = tier.minMem
			}
			if memoryMB > tier.maxMem {
				memoryMB = tier.maxMem
			}
			return strconv.Itoa(tier.cpu), strconv.Itoa(memoryMB)
		}
	}
	return "4096", "30720"
}

func (p *Provider) serviceTags(plan provider.Plan, service spec.Service) ([]ecstypes.Tag, error) {
	serviceHash, err := spec.HashService(service)
	if err != nil {
		return nil, err
	}
	pairs := map[string]string{
		tagManagedBy: managedBy, tagProject: plan.App.Project, tagApp: plan.App.App,
		tagService: service.Name, tagRevision: plan.Revision,
		tagServiceHash: serviceHash, tagSpecHash: plan.SpecHash,
	}
	tags := make([]ecstypes.Tag, 0, len(pairs))
	for key, value := range pairs {
		tags = append(tags, ecstypes.Tag{Key: aws.String(key), Value: aws.String(value)})
	}
	return tags, nil
}

func (p *Provider) Observe(ctx context.Context, target provider.Target, app provider.AppRef) (provider.LiveState, error) {
	client, _, _, err := p.clients(ctx, target)
	if err != nil {
		return provider.LiveState{}, err
	}
	live, err := p.servicesFor(ctx, client, target, app)
	if err != nil {
		return provider.LiveState{}, err
	}

	state := provider.LiveState{App: app, Target: target.ID, ReadAt: time.Now().UTC(),
		Services: map[string]provider.ServiceState{}}
	for name, service := range live {
		if service.status == "INACTIVE" {
			continue
		}
		health := provider.Healthy
		switch {
		case service.running == 0 && service.desired > 0:
			health = provider.Missing
		case service.running < service.desired:
			health = provider.Degraded
		}
		state.Services[name] = provider.ServiceState{
			Health: health, Replicas: service.desired, Ready: service.running,
			Image:   service.image,
			Message: fmt.Sprintf("%d/%d tasks running", service.running, service.desired),
		}
		if revision := service.tags[tagRevision]; revision != "" {
			state.Revision = revision
		}
		if hash := service.tags[tagSpecHash]; hash != "" {
			state.SpecHash = hash
		}
	}
	return state, nil
}

func (p *Provider) Instances(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Instance, error) {
	client, _, _, err := p.clients(ctx, target)
	if err != nil {
		return nil, err
	}
	live, err := p.servicesFor(ctx, client, target, app)
	if err != nil {
		return nil, err
	}
	cluster := clusterOf(target)

	var instances []provider.Instance
	for name, service := range live {
		if service.status == "INACTIVE" {
			continue
		}
		listed, err := client.ListTasks(ctx, &awsecs.ListTasksInput{
			Cluster: aws.String(cluster), ServiceName: aws.String(service.name),
		})
		if err != nil {
			return nil, err
		}
		if len(listed.TaskArns) == 0 {
			continue
		}
		described, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
			Cluster: aws.String(cluster), Tasks: listed.TaskArns,
		})
		if err != nil {
			return nil, err
		}
		for _, task := range described.Tasks {
			health := provider.Healthy
			if aws.ToString(task.LastStatus) != "RUNNING" {
				health = provider.Degraded
			}
			image := ""
			if len(task.Containers) > 0 {
				image = aws.ToString(task.Containers[0].Image)
			}
			started := time.Time{}
			if task.StartedAt != nil {
				started = *task.StartedAt
			}
			instances = append(instances, provider.Instance{
				Ref: provider.InstanceRef{
					AppRef: app, Service: name, Instance: taskID(aws.ToString(task.TaskArn)),
				},
				Image: image, Status: strings.ToLower(aws.ToString(task.LastStatus)),
				Health: health, StartedAt: started,
				Revision: service.tags[tagRevision],
			})
		}
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].Ref.Instance < instances[j].Ref.Instance })
	return instances, nil
}

// taskID is the last ARN segment — the id an operator sees in the console.
func taskID(arn string) string {
	if index := strings.LastIndex(arn, "/"); index >= 0 {
		return arn[index+1:]
	}
	return arn
}

// Metrics answers from CloudWatch — provider-native first, per the rule that
// HEIMDALL never builds a time-series database for a cloud that already has
// one.
func (p *Provider) Metrics(
	ctx context.Context,
	target provider.Target,
	instance provider.InstanceRef,
	window provider.Window,
) (provider.Series, error) {
	_, cw, _, err := p.clients(ctx, target)
	if err != nil {
		return provider.Series{}, err
	}

	from := window.From
	if from.IsZero() {
		from = time.Now().UTC().Add(-24 * time.Hour)
	}
	to := window.To
	if to.IsZero() {
		to = time.Now().UTC()
	}

	dimensions := []cwtypes.Dimension{
		{Name: aws.String("ClusterName"), Value: aws.String(clusterOf(target))},
		{Name: aws.String("ServiceName"), Value: aws.String(resourceName(instance.AppRef, instance.Service))},
	}
	query := func(id, metric string) cwtypes.MetricDataQuery {
		return cwtypes.MetricDataQuery{
			Id: aws.String(id),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace: aws.String("AWS/ECS"), MetricName: aws.String(metric),
					Dimensions: dimensions,
				},
				Period: aws.Int32(60), Stat: aws.String("Average"),
			},
		}
	}

	data, err := cw.GetMetricData(ctx, &cloudwatch.GetMetricDataInput{
		StartTime: aws.Time(from), EndTime: aws.Time(to),
		MetricDataQueries: []cwtypes.MetricDataQuery{
			query("cpu", "CPUUtilization"), query("memory", "MemoryUtilization"),
		},
	})
	if err != nil {
		return provider.Series{}, fmt.Errorf("HD0360: CloudWatch metrics: %w", err)
	}

	series := provider.Series{Ref: instance}
	for _, result := range data.MetricDataResults {
		samples := make([]provider.Sample, 0, len(result.Timestamps))
		for i := range result.Timestamps {
			samples = append(samples, provider.Sample{At: result.Timestamps[i], Value: result.Values[i]})
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i].At.Before(samples[j].At) })
		switch aws.ToString(result.Id) {
		case "cpu":
			series.CPUPercent = samples
		case "memory":
			// CloudWatch reports memory as a percentage of the task limit.
			// The chart's unit is bytes, so a percent series would lie on the
			// axis; scaled against a 100-unit limit it reads as a percent
			// chart, which is the honest thing CloudWatch actually knows.
			series.MemoryBytes = samples
			series.MemoryLimit = 100
		}
	}
	return series, nil
}

// Logs reads the awslogs stream Fargate wrote. A bounded tail, not a follow:
// the UI polls, and CloudWatch's own API is a paged read anyway.
func (p *Provider) Logs(
	ctx context.Context,
	target provider.Target,
	instance provider.InstanceRef,
	filter provider.LogFilter,
) (io.ReadCloser, error) {
	group := target.Config["log_group"]
	if group == "" {
		return nil, fmt.Errorf("HD0361: target %s has no log_group configured, so tasks were started without the awslogs driver", target.ID)
	}
	_, _, logs, err := p.clients(ctx, target)
	if err != nil {
		return nil, err
	}

	// awslogs stream name: <prefix>/<container>/<task-id>.
	stream := fmt.Sprintf("%s/%s/%s",
		resourceName(instance.AppRef, instance.Service), instance.Service, instance.Instance)
	limit := int32(filter.Tail)
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	events, err := logs.GetLogEvents(ctx, &cloudwatchlogs.GetLogEventsInput{
		LogGroupName: aws.String(group), LogStreamName: aws.String(stream),
		Limit: aws.Int32(limit), StartFromHead: aws.Bool(false),
	})
	if err != nil {
		return nil, fmt.Errorf("HD0362: read log stream %s: %w", stream, err)
	}

	var builder strings.Builder
	for _, event := range events.Events {
		builder.WriteString(aws.ToString(event.Message))
		builder.WriteByte('\n')
	}
	return io.NopCloser(strings.NewReader(builder.String())), nil
}

// Events surfaces the service scheduler's own narration.
func (p *Provider) Events(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Event, error) {
	client, _, _, err := p.clients(ctx, target)
	if err != nil {
		return nil, err
	}
	live, err := p.servicesFor(ctx, client, target, app)
	if err != nil {
		return nil, err
	}

	var events []provider.Event
	for name, service := range live {
		for _, event := range service.events {
			at := time.Time{}
			if event.CreatedAt != nil {
				at = *event.CreatedAt
			}
			events = append(events, provider.Event{
				At: at, Type: "service", Service: name,
				Message: aws.ToString(event.Message), Source: "ecs",
			})
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].At.After(events[j].At) })
	if len(events) > 100 {
		events = events[:100]
	}
	return events, nil
}
