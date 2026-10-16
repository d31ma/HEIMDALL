package lambda

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
	"strconv"
	"strings"
)

type inventoryEntry struct {
	inventory *inventory
	entry     types.FunctionConfiguration
	live      *awslambda.GetFunctionOutput
	service   string
	published *awslambda.GetFunctionOutput
}

func (e *inventoryEntry) Read(ctx context.Context) error {
	steps := e.candidateSteps(ctx)
	return (sequence.Steps{Actions: steps}).Run()
}
func (e *inventoryEntry) candidateSteps(ctx context.Context) []func() error {
	if !strings.HasPrefix(aws.ToString(e.entry.FunctionName), (ownershipLabels{}).Prefix()) {
		return nil
	}
	return []func() error{func() error { return e.read(ctx) }, e.metadata, func() error { return e.owned(ctx) }}
}
func (e *inventoryEntry) read(ctx context.Context) error {
	live, err := e.inventory.client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: e.entry.FunctionName})
	e.live = live
	return err
}
func (e *inventoryEntry) metadata() error {
	if e.live.TagsError != nil {
		return (Diagnostic{}).Format((Diagnostic{}).InventoryCode(), "Lambda ownership metadata is unreadable")
	}
	return nil
}
func (e *inventoryEntry) owned(ctx context.Context) error {
	owned := (Identity{App: e.inventory.app}).Owns(e.live.Tags)
	steps := e.ownedSteps(ctx, owned)
	return (sequence.Steps{Actions: steps}).Run()
}
func (e *inventoryEntry) ownedSteps(ctx context.Context, owned bool) []func() error {
	if !owned {
		return nil
	}
	return []func() error{e.identity, e.duplicate, func() error { return e.version(ctx) }, e.store}
}
func (e *inventoryEntry) identity() error {
	e.service = e.live.Tags[(ownershipLabels{}).Service()]
	expected := (Identity{App: e.inventory.app, Service: e.service}).Name()
	return e.requireIdentity(expected)
}
func (e *inventoryEntry) requireIdentity(expected string) error {
	if e.service == "" || aws.ToString(e.entry.FunctionName) != expected {
		return (Diagnostic{}).Format((Diagnostic{}).InventoryCode(), "Lambda function identity does not match its resource name")
	}
	return nil
}
func (e *inventoryEntry) duplicate() error {
	_, duplicate := e.inventory.functions[e.service]
	if duplicate {
		return (Diagnostic{}).Format((Diagnostic{}).InventoryCode(), "duplicate Lambda service identity")
	}
	return nil
}
func (e *inventoryEntry) version(ctx context.Context) error {
	version := e.live.Tags[(ownershipLabels{}).Version()]
	number, parseErr := strconv.Atoi(version)
	steps := e.versionSteps(ctx, version, parseErr == nil && number > 0)
	return (sequence.Steps{Actions: steps}).Run()
}
func (e *inventoryEntry) versionSteps(ctx context.Context, version string, valid bool) []func() error {
	if !valid {
		return nil
	}
	return []func() error{func() error { return e.readVersion(ctx, version) }}
}
func (e *inventoryEntry) readVersion(ctx context.Context, version string) error {
	published, err := e.inventory.client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: e.entry.FunctionName, Qualifier: aws.String(version)})
	var missing *types.ResourceNotFoundException
	absent := errors.As(err, &missing)
	return e.versionResult(published, err, absent)
}
func (e *inventoryEntry) versionResult(published *awslambda.GetFunctionOutput, err error, absent bool) error {
	if absent {
		return nil
	}
	e.published = published
	return err
}
func (e *inventoryEntry) store() error {
	e.inventory.functions[e.service] = FunctionState{Live: e.live, Published: e.published}
	return nil
}
