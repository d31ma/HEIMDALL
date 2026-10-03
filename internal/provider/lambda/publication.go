package lambda

import (
	"context"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

type publication struct {
	client   *awslambda.Client
	identity Identity
	ready    *awslambda.GetFunctionOutput
	target   provider.Target
	service  spec.Service
}

func (publication publication) Version(requestContext context.Context) (string, error) {
	return (versionPublication{publication: publication}).Version(requestContext)
}
