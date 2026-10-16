package lambda

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
	"net/http"
)

type clientFactory struct {
	adapter       *Provider
	target        provider.Target
	configuration aws.Config
	options       []func(*awsconfig.LoadOptions) error
}

func (f *clientFactory) Configuration(ctx context.Context) (aws.Config, error) {
	f.options = []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(f.target.Region), awsconfig.WithHTTPClient(httpClient{delegate: f.transport()})}
	err := (sequence.Steps{Actions: []func() error{f.validate, func() error { return f.credentials(ctx) }, func() error { return f.load(ctx) }}}).Run()
	return f.configuration, err
}
func (f *clientFactory) Client(ctx context.Context) (*awslambda.Client, error) {
	configuration, err := f.Configuration(ctx)
	return f.client(configuration, err)
}
func (f *clientFactory) validate() error { return (TargetContract{Target: f.target}).Validate() }
func (f *clientFactory) transport() aws.HTTPClient {
	if f.adapter.HTTPClient == nil {
		return &http.Client{Transport: http.DefaultTransport, Timeout: (functionLimits{}).TransportTimeout()}
	}
	return f.adapter.HTTPClient
}
func (f *clientFactory) credentials(ctx context.Context) error {
	steps := f.credentialSteps(ctx)
	return (sequence.Steps{Actions: steps}).Run()
}
func (f *clientFactory) credentialSteps(ctx context.Context) []func() error {
	switch {
	case f.adapter.StaticCredentials != nil:
		return []func() error{f.staticCredentials}
	case f.target.CredentialRef != "":
		return []func() error{func() error { return f.referencedCredentials(ctx) }}
	}
	return nil
}
func (f *clientFactory) staticCredentials() error {
	f.options = append(f.options, awsconfig.WithCredentialsProvider(f.adapter.StaticCredentials))
	return nil
}
func (f *clientFactory) referencedCredentials(ctx context.Context) error {
	credential, err := (&credentialReference{resolver: f.adapter.SecretResolver, reference: f.target.CredentialRef}).Resolve(ctx)
	return f.appendCredential(credential, err)
}
func (f *clientFactory) appendCredential(credential aws.CredentialsProvider, err error) error {
	if err != nil {
		return err
	}
	f.options = append(f.options, awsconfig.WithCredentialsProvider(credential))
	return nil
}
func (f *clientFactory) load(ctx context.Context) error {
	configuration, err := awsconfig.LoadDefaultConfig(ctx, f.options...)
	f.configuration = configuration
	return err
}
func (f *clientFactory) client(configuration aws.Config, err error) (*awslambda.Client, error) {
	if err != nil {
		return nil, err
	}
	return awslambda.NewFromConfig(configuration, f.endpoint), nil
}
func (f *clientFactory) endpoint(options *awslambda.Options) {
	if f.adapter.EndpointOverride != "" {
		options.BaseEndpoint = aws.String(f.adapter.EndpointOverride)
	}
}
