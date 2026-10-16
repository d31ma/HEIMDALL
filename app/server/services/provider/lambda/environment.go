package lambda

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/app/server/models/spec"
)

type environment struct {
	resolver  func(context.Context, string) (string, error)
	service   spec.Service
	variables map[string]string
	bytes     int
}

func (e *environment) Build(ctx context.Context) (*types.Environment, error) {
	e.variables = map[string]string{}
	err := e.settings(ctx)
	return e.result(err)
}
func (e *environment) settings(ctx context.Context) error {
	var err error
	for index := 0; index < len(e.service.Env) && err == nil; index++ {
		err = (&environmentValue{environment: e, setting: e.service.Env[index]}).Resolve(ctx)
	}
	return err
}
func (e *environment) result(err error) (*types.Environment, error) {
	if err != nil {
		return nil, err
	}
	return &types.Environment{Variables: e.variables}, nil
}
