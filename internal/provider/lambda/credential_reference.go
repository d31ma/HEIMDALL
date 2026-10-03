package lambda

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/d31ma/heimdall/internal/sequence"
)

type credentialReference struct {
	resolver  func(context.Context, string) (string, error)
	reference string
	raw       string
	parsed    awsCredential
}

func (c *credentialReference) Resolve(ctx context.Context) (aws.CredentialsProvider, error) {
	err := (sequence.Steps{Actions: []func() error{c.available, func() error { return c.read(ctx) }, c.parse}}).Run()
	return c.result(err)
}
func (c *credentialReference) available() error {
	if c.resolver == nil {
		return (Diagnostic{}).Format((Diagnostic{}).CredentialCode(), "Lambda credential reference requires a resolver")
	}
	return nil
}
func (c *credentialReference) read(ctx context.Context) error {
	raw, err := c.resolver(ctx, c.reference)
	c.raw = raw
	if err != nil {
		return (Diagnostic{}).Format((Diagnostic{}).CredentialCode(), "Lambda credential reference could not be resolved")
	}
	return nil
}
func (c *credentialReference) parse() error {
	if json.Unmarshal([]byte(c.raw), &c.parsed) != nil || c.parsed.AccessKeyID == "" || c.parsed.SecretAccessKey == "" {
		return (Diagnostic{}).Format((Diagnostic{}).CredentialCode(), "Lambda credential reference requires access_key_id and secret_access_key JSON")
	}
	return nil
}
func (c *credentialReference) result(err error) (aws.CredentialsProvider, error) {
	if err != nil {
		return nil, err
	}
	return credentials.NewStaticCredentialsProvider(c.parsed.AccessKeyID, c.parsed.SecretAccessKey, c.parsed.SessionToken), nil
}
