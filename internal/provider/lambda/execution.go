package lambda

import (
	"context"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
)

type execution struct {
	adapter *Provider
	client  *awslambda.Client
	request executionRequest
}

func (e execution) Run(ctx context.Context) error {
	return (&functionChange{adapter: e.adapter, client: e.client, request: e.request, identity: Identity{App: e.request.Plan.App, Service: e.request.Operation.Service}}).Run(ctx)
}
