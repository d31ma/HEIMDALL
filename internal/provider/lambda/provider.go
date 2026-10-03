package lambda

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

type Provider struct {
	SecretResolver    func(context.Context, string) (string, error)
	EndpointOverride  string
	StaticCredentials aws.CredentialsProvider
	HTTPClient        aws.HTTPClient
}

func (p *Provider) Name() string { return "lambda" }

func (p *Provider) Capabilities() provider.Capabilities {
	return p.capabilities()
}

func (p *Provider) capabilities() provider.Capabilities {
	support := map[provider.Feature]provider.Support{}
	caveats := map[provider.Feature]string{}
	for _, feature := range provider.Features {
		support[feature] = provider.Rejected
		caveats[feature] = "default Lambda functions do not implement this container lifecycle feature"
	}
	support[provider.FeatureMultiService] = provider.Full
	support[provider.FeatureFunction] = provider.Partial
	caveats[provider.FeatureFunction] = "default Lambda only; immutable same-region ECR Runtime API image, direct JSON invocation and existing execution role required"
	support[provider.FeatureResources] = provider.Partial
	caveats[provider.FeatureResources] = "memory 128-10240 MiB; CPU is determined by Lambda; independent CPU requests are rejected"
	support[provider.FeatureSecretRef] = provider.Partial
	caveats[provider.FeatureSecretRef] = "environment references resolve only during apply; plan compares keys, references and literal values without reading secrets"
	support[provider.FeatureScaleToZero] = provider.Partial
	caveats[provider.FeatureScaleToZero] = "idle execution environments are native Lambda behavior; container replica counts and provisioned concurrency are not managed"
	return provider.Capabilities{Provider: p.Name(), Support: support, Caveats: caveats}
}

func (p *Provider) configuration(ctx context.Context, target provider.Target) (aws.Config, error) {
	if target.Region == "" {
		return aws.Config{}, (Diagnostic{}).Format("HD0410: Lambda target requires region")
	}
	transport := p.HTTPClient
	if transport == nil {
		transport = &http.Client{Transport: http.DefaultTransport, Timeout: 910 * time.Second}
	}
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(target.Region), awsconfig.WithHTTPClient(httpClient{delegate: transport})}
	if p.StaticCredentials != nil {
		opts = append(opts, awsconfig.WithCredentialsProvider(p.StaticCredentials))
	} else if target.CredentialRef != "" {
		credential, err := p.credential(ctx, target)
		if err != nil {
			return aws.Config{}, err
		}
		opts = append(opts, awsconfig.WithCredentialsProvider(credential))
	}
	config, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, err
	}
	return config, nil
}

func (p *Provider) client(ctx context.Context, target provider.Target) (*awslambda.Client, error) {
	config, err := p.configuration(ctx, target)
	if err != nil {
		return nil, err
	}
	return awslambda.NewFromConfig(config, func(o *awslambda.Options) {
		if p.EndpointOverride != "" {
			o.BaseEndpoint = aws.String(p.EndpointOverride)
		}
	}), nil
}

func (p *Provider) credential(ctx context.Context, target provider.Target) (aws.CredentialsProvider, error) {
	if p.SecretResolver == nil {
		return nil, (Diagnostic{}).Format("HD0411: Lambda credential reference requires a resolver")
	}
	raw, err := p.SecretResolver(ctx, target.CredentialRef)
	if err != nil {
		return nil, (Diagnostic{}).Format("HD0411: Lambda credential reference could not be resolved")
	}
	var parsed awsCredential
	if json.Unmarshal([]byte(raw), &parsed) != nil || parsed.AccessKeyID == "" || parsed.SecretAccessKey == "" {
		return nil, (Diagnostic{}).Format("HD0411: Lambda credential reference requires access_key_id and secret_access_key JSON")
	}
	return credentials.NewStaticCredentialsProvider(parsed.AccessKeyID, parsed.SecretAccessKey, parsed.SessionToken), nil
}

func (p *Provider) Plan(ctx context.Context, target provider.Target, want spec.DeploySpec) (provider.Plan, error) {
	return p.plan(ctx, target, want)
}

func (p *Provider) plan(ctx context.Context, target provider.Target, want spec.DeploySpec) (provider.Plan, error) {
	if err := (RuntimeContract{Target: target, Spec: want}).Validate(); err != nil {
		return provider.Plan{}, err
	}
	hash, err := spec.Hash(want)
	if err != nil {
		return provider.Plan{}, err
	}
	client, err := p.client(ctx, target)
	if err != nil {
		return provider.Plan{}, err
	}
	project := target.Project
	if project == "" {
		project = "default"
	}
	plan := provider.Plan{Target: target.ID, App: provider.AppRef{Project: project, App: want.App}, Revision: want.Revision, SpecHash: hash}
	live, err := p.functions(ctx, client, plan.App)
	if err != nil {
		return provider.Plan{}, err
	}
	desired := map[string]bool{}
	for _, s := range want.Services {
		desired[s.Name] = true
		serviceHash, err := spec.HashService(s)
		if err != nil {
			return provider.Plan{}, err
		}
		kind, reason := provider.OpCreate, "function is not deployed"
		if _, found := live[s.Name]; !found {
			if err := p.available(ctx, client, Identity{App: plan.App, Service: s.Name}); err != nil {
				return provider.Plan{}, err
			}
		}
		if found, ok := live[s.Name]; ok {
			kind, reason = provider.OpUpdate, "native image, configuration, readiness or desired hash differs"
			if found.Matches(target, s) && found.Live.Tags["dev.delma.heimdall.service-hash"] == serviceHash {
				kind, reason = provider.OpNoop, ""
			}
		}
		plan.Operations = append(plan.Operations, provider.Operation{Kind: kind, Service: s.Name, Wave: s.Wave, Reason: reason})
	}
	for name := range live {
		if !desired[name] {
			plan.Operations = append(plan.Operations, provider.Operation{Kind: provider.OpDelete, Service: name, Wave: 1 << 30, Prune: true, Reason: "function removed from desired state"})
		}
	}
	sort.Slice(plan.Operations, func(i, j int) bool {
		if plan.Operations[i].Wave != plan.Operations[j].Wave {
			return plan.Operations[i].Wave < plan.Operations[j].Wave
		}
		return plan.Operations[i].Service < plan.Operations[j].Service
	})
	return plan, nil
}

func (p *Provider) available(ctx context.Context, client *awslambda.Client, identity Identity) error {
	_, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(identity.Name())})
	var missing *types.ResourceNotFoundException
	if errors.As(err, &missing) {
		return nil
	}
	if err != nil {
		return err
	}
	return (Diagnostic{}).Format("HD0414: desired function name already exists outside the verified application inventory; inspect ownership and re-plan")
}

func (p *Provider) functions(ctx context.Context, client *awslambda.Client, app provider.AppRef) (map[string]FunctionState, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	result := map[string]FunctionState{}
	var marker *string
	for page := 0; page < 100; page++ {
		listed, err := client.ListFunctions(ctx, &awslambda.ListFunctionsInput{MaxItems: aws.Int32(50), Marker: marker})
		if err != nil {
			return nil, err
		}
		for _, entry := range listed.Functions {
			name := aws.ToString(entry.FunctionName)
			if !strings.HasPrefix(name, "hd-") {
				continue
			}
			found, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: entry.FunctionName})
			if err != nil {
				return nil, err
			}
			if found.TagsError != nil {
				return nil, (Diagnostic{}).Format("HD0412: Lambda ownership metadata is unreadable")
			}
			if !(Identity{App: app}).Owns(found.Tags) {
				continue
			}
			service := found.Tags["dev.delma.heimdall.service"]
			if service == "" || name != (Identity{App: app, Service: service}).Name() {
				return nil, (Diagnostic{}).Format("HD0412: Lambda function identity does not match its resource name")
			}
			if _, duplicate := result[service]; duplicate {
				return nil, (Diagnostic{}).Format("HD0412: duplicate Lambda service identity")
			}
			state := FunctionState{Live: found}
			version := found.Tags["dev.delma.heimdall.function-version"]
			if number, err := strconv.Atoi(version); err == nil && number > 0 {
				published, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: entry.FunctionName, Qualifier: aws.String(version)})
				var missing *types.ResourceNotFoundException
				if err != nil && !errors.As(err, &missing) {
					return nil, err
				}
				if err == nil {
					state.Published = published
				}
			}
			result[service] = state
		}
		if aws.ToString(listed.NextMarker) == "" {
			return result, nil
		}
		if aws.ToString(marker) == aws.ToString(listed.NextMarker) {
			return nil, (Diagnostic{}).Format("HD0412: Lambda pagination did not advance")
		}
		marker = listed.NextMarker
	}
	return nil, (Diagnostic{}).Format("HD0412: Lambda inventory exceeds 100 pages")
}

func (p *Provider) Apply(ctx context.Context, target provider.Target, plan provider.Plan) (provider.Result, error) {
	return p.apply(ctx, target, plan)
}

func (p *Provider) apply(ctx context.Context, target provider.Target, plan provider.Plan) (provider.Result, error) {
	opts, ok := ctx.Value(applyKey{}).(ApplyOptions)
	if !ok {
		return provider.Result{}, (Diagnostic{}).Format("HD0413: Lambda apply requires ApplyOptions.WithContext")
	}
	hash, err := spec.Hash(opts.Spec)
	if err != nil {
		return provider.Result{}, err
	}
	project := target.Project
	if project == "" {
		project = "default"
	}
	if hash != plan.SpecHash || plan.Target != target.ID || plan.App.App != opts.Spec.App || plan.App.Project != project {
		return provider.Result{}, (Diagnostic{}).Format("HD0413: Lambda plan and apply target/spec differ; re-plan")
	}
	if err := (RuntimeContract{Target: target, Spec: opts.Spec}).Validate(); err != nil {
		return provider.Result{}, err
	}
	client, err := p.client(ctx, target)
	if err != nil {
		return provider.Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	result := provider.Result{}
	for _, operation := range plan.Operations {
		if operation.Kind == provider.OpNoop || operation.Prune && !opts.Prune {
			continue
		}
		if err := (execution{adapter: p, client: client, request: executionRequest{Target: target, Plan: plan, Desired: opts.Spec, Operation: operation}}).Run(ctx); err != nil {
			result.Failures = map[string]string{operation.Service: err.Error()}
			return result, nil
		}
		result.Applied = append(result.Applied, operation)
	}
	return result, nil
}

func (p *Provider) waitMetadata(ctx context.Context, client *awslambda.Client, id Identity, tags map[string]string) error {
	for {
		live, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(id.Name())})
		if err != nil {
			return err
		}
		if live.TagsError != nil {
			return (Diagnostic{}).Format("HD0415: Lambda revision metadata cannot be verified")
		}
		matches := true
		for key, value := range tags {
			if live.Tags[key] != value {
				matches = false
				break
			}
		}
		if matches {
			return nil
		}
		if err := p.pause(ctx); err != nil {
			return err
		}
	}
}

func (p *Provider) environment(ctx context.Context, s spec.Service) (*types.Environment, error) {
	env := &types.Environment{Variables: map[string]string{}}
	bytes := 0
	for _, setting := range s.Env {
		value := setting.Value
		if setting.Ref != "" {
			if p.SecretResolver == nil {
				return nil, (Diagnostic{}).Format("HD0416: function %s requires an environment secret resolver", s.Name)
			}
			resolved, err := p.SecretResolver(ctx, setting.Ref)
			if err != nil {
				return nil, (Diagnostic{}).Format("HD0416: function %s environment reference could not be resolved", s.Name)
			}
			value = resolved
		}
		bytes += len(setting.Key) + len(value)
		if bytes > 4096 {
			return nil, (Diagnostic{}).Format("HD0416: Lambda environment exceeds 4096 bytes")
		}
		env.Variables[setting.Key] = value
	}
	return env, nil
}

func (p *Provider) waitReady(ctx context.Context, client *awslambda.Client, name, version string) (*awslambda.GetFunctionOutput, error) {
	for {
		input := &awslambda.GetFunctionInput{FunctionName: aws.String(name)}
		if version != "" {
			input.Qualifier = aws.String(version)
		}
		live, err := client.GetFunction(ctx, input)
		if err != nil {
			return nil, err
		}
		c := live.Configuration
		if c != nil && (c.State == types.StateFailed || c.LastUpdateStatus == types.LastUpdateStatusFailed) {
			return nil, (Diagnostic{}).Format("HD0415: Lambda deployment failed; inspect native function state and update diagnostics")
		}
		if c != nil && c.State == types.StateActive && (version != "" || c.LastUpdateStatus == types.LastUpdateStatusSuccessful) {
			return live, nil
		}
		if err := p.pause(ctx); err != nil {
			return nil, err
		}
	}
}

func (p *Provider) waitDeleted(ctx context.Context, client *awslambda.Client, name string) error {
	for {
		_, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(name)})
		var missing *types.ResourceNotFoundException
		if errors.As(err, &missing) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := p.pause(ctx); err != nil {
			return err
		}
	}
}

func (p *Provider) pause(ctx context.Context) error {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p *Provider) Observe(ctx context.Context, target provider.Target, app provider.AppRef) (provider.LiveState, error) {
	return p.observe(ctx, target, app)
}

func (p *Provider) observe(ctx context.Context, target provider.Target, app provider.AppRef) (provider.LiveState, error) {
	client, err := p.client(ctx, target)
	if err != nil {
		return provider.LiveState{}, err
	}
	live, err := p.functions(ctx, client, app)
	if err != nil {
		return provider.LiveState{}, err
	}
	state := provider.LiveState{App: app, Target: target.ID, ReadAt: time.Now().UTC(), Services: map[string]provider.ServiceState{}}
	for name, function := range live {
		image := ""
		if function.Live.Code != nil {
			image = aws.ToString(function.Live.Code.ResolvedImageUri)
		}
		state.Services[name] = provider.ServiceState{Health: function.Health(), Image: image, Message: "deployment readiness; execution environment inventory is not exposed by Lambda"}
		if revision := function.Live.Tags["dev.delma.heimdall.revision"]; state.Revision == "" {
			state.Revision = revision
		} else if state.Revision != revision {
			state.Revision = "mixed"
		}
		if hash := function.Live.Tags["dev.delma.heimdall.spec-hash"]; state.SpecHash == "" {
			state.SpecHash = hash
		} else if state.SpecHash != hash {
			state.SpecHash = "mixed"
		}
	}
	return state, nil
}

func (p *Provider) Instances(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Instance, error) {
	return p.instances(ctx, target, app)
}

func (p *Provider) instances(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Instance, error) {
	_, err := p.Observe(ctx, target, app)
	return nil, err
}

func (p *Provider) Metrics(ctx context.Context, target provider.Target, instance provider.InstanceRef, window provider.Window) (provider.Series, error) {
	return provider.Series{}, (Diagnostic{}).Format("HD0417: Lambda exposes function/version invocation metrics, not per-container CPU or memory series")
}

func (p *Provider) FunctionMetrics(ctx context.Context, target provider.Target, ref provider.FunctionRef, window provider.Window) (provider.FunctionSeries, error) {
	return (functionObservation{adapter: p, target: target, ref: ref}).metrics(ctx, window)
}

func (p *Provider) FunctionLogs(ctx context.Context, target provider.Target, ref provider.FunctionRef, filter provider.LogFilter) (io.ReadCloser, error) {
	return (functionObservation{adapter: p, target: target, ref: ref}).logs(ctx, filter)
}

func (p *Provider) Logs(ctx context.Context, target provider.Target, instance provider.InstanceRef, filter provider.LogFilter) (io.ReadCloser, error) {
	return nil, (Diagnostic{}).Format("HD0417: Lambda logs require a native CloudWatch function/version stream; execution environments have no stable instance IDs")
}

func (p *Provider) Events(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Event, error) {
	_, err := p.Observe(ctx, target, app)
	return nil, err
}

func (p *Provider) Invoke(ctx context.Context, request provider.InvocationRequest) (provider.Invocation, error) {
	return p.invoke(ctx, request)
}

func (p *Provider) invoke(ctx context.Context, request provider.InvocationRequest) (provider.Invocation, error) {
	target, app, service, payload := request.Target, request.App, request.Service, request.Payload
	if len(payload) > 6*1024*1024 || !json.Valid(payload) {
		return provider.Invocation{}, (Diagnostic{}).Format("HD0418: direct Lambda payload must be valid JSON within 6 MiB")
	}
	client, err := p.client(ctx, target)
	if err != nil {
		return provider.Invocation{}, err
	}
	id := Identity{App: app, Service: service}
	live, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(id.Name())})
	if err != nil {
		return provider.Invocation{}, err
	}
	if live.TagsError != nil || !id.Owns(live.Tags) {
		return provider.Invocation{}, (Diagnostic{}).Format("HD0414: Lambda function belongs outside this application")
	}
	version := live.Tags["dev.delma.heimdall.function-version"]
	if number, err := strconv.Atoi(version); err != nil || number < 1 {
		return provider.Invocation{}, (Diagnostic{}).Format("HD0418: no converged immutable Lambda version is available")
	}
	ctx, cancel := context.WithTimeout(ctx, 905*time.Second)
	defer cancel()
	invoked, err := client.Invoke(ctx, &awslambda.InvokeInput{FunctionName: aws.String(id.Name()), Qualifier: aws.String(version), InvocationType: types.InvocationTypeRequestResponse, Payload: payload}, func(o *awslambda.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return provider.Invocation{}, err
	}
	if invoked.StatusCode != 200 || aws.ToString(invoked.ExecutedVersion) != version {
		return provider.Invocation{}, (Diagnostic{}).Format("HD0418: Lambda returned an unexpected invocation status or version")
	}
	if !json.Valid(invoked.Payload) {
		return provider.Invocation{}, (Diagnostic{}).Format("HD0418: direct Lambda response is not valid JSON")
	}
	requestID, _ := awsmiddleware.GetRequestIDMetadata(invoked.ResultMetadata)
	return provider.Invocation{RequestID: requestID, Version: version, Payload: invoked.Payload, FunctionError: aws.ToString(invoked.FunctionError)}, nil
}
