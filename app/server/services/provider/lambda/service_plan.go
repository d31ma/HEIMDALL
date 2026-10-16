package lambda

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type servicePlan struct {
	planner   *planner
	service   spec.Service
	hash      string
	operation provider.Operation
}

func (s *servicePlan) Build(ctx context.Context) error {
	err := (sequence.Steps{Actions: []func() error{s.hashService, func() error { return s.available(ctx) }, s.compare, s.append}}).Run()
	return err
}
func (s *servicePlan) hashService() error {
	hash, err := spec.HashService(s.service)
	s.hash = hash
	return err
}
func (s *servicePlan) available(ctx context.Context) error {
	_, found := s.planner.live[s.service.Name]
	steps := s.availabilitySteps(ctx, found)
	return (sequence.Steps{Actions: steps}).Run()
}
func (s *servicePlan) availabilitySteps(ctx context.Context, found bool) []func() error {
	if found {
		return nil
	}
	return []func() error{func() error { return s.readName(ctx) }}
}
func (s *servicePlan) readName(ctx context.Context) error {
	identity := Identity{App: s.planner.plan.App, Service: s.service.Name}
	_, err := s.planner.client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(identity.Name())})
	var missing *types.ResourceNotFoundException
	absent := errors.As(err, &missing)
	return s.nameResult(err, absent)
}
func (s *servicePlan) nameResult(err error, absent bool) error {
	switch {
	case absent:
		return nil
	case err != nil:
		return err
	}
	return (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), "desired function name already exists outside the verified application inventory; inspect ownership and re-plan")
}
func (s *servicePlan) compare() error {
	live, found := s.planner.live[s.service.Name]
	matches := live.Matches(s.planner.target, s.service)
	hash := s.actualHash(live)
	valid := (FunctionState{}).all([]bool{found, matches, hash == s.hash})
	s.operation = s.operationFor(found, valid)
	return nil
}
func (servicePlan) actualHash(live FunctionState) string {
	if live.Live == nil {
		return ""
	}
	return live.Live.Tags[(ownershipLabels{}).ServiceHash()]
}
func (s *servicePlan) operationFor(found, matches bool) provider.Operation {
	operation := provider.Operation{Kind: provider.OpCreate, Service: s.service.Name, Wave: s.service.Wave, Reason: "function is not deployed"}
	switch {
	case matches:
		operation.Kind = provider.OpNoop
		operation.Reason = ""
	case found:
		operation.Kind = provider.OpUpdate
		operation.Reason = "native image, configuration, readiness or desired hash differs"
	}
	return operation
}
func (s *servicePlan) append() error {
	s.planner.plan.Operations = append(s.planner.plan.Operations, s.operation)
	return nil
}
