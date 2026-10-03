package lambda

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
)

type inventory struct {
	client    *awslambda.Client
	app       provider.AppRef
	functions map[string]FunctionState
	marker    *string
	listed    *awslambda.ListFunctionsOutput
	done      bool
}

func (i *inventory) Read(ctx context.Context) (map[string]FunctionState, error) {
	ctx, cancel := context.WithTimeout(ctx, (functionLimits{}).InventoryTimeout())
	defer cancel()
	i.functions = map[string]FunctionState{}
	err := i.pages(ctx)
	return i.result(err)
}
func (i *inventory) pages(ctx context.Context) error {
	var err error
	for page := 0; page < (functionLimits{}).InventoryPages() && !i.done && err == nil; page++ {
		err = i.page(ctx)
	}
	return i.complete(err)
}
func (i *inventory) page(ctx context.Context) error {
	return (sequence.Steps{Actions: []func() error{func() error { return i.readPage(ctx) }, func() error { return i.entries(ctx) }, i.advance}}).Run()
}
func (i *inventory) readPage(ctx context.Context) error {
	listed, err := i.client.ListFunctions(ctx, &awslambda.ListFunctionsInput{MaxItems: aws.Int32((functionLimits{}).PageItems()), Marker: i.marker})
	i.listed = listed
	return err
}
func (i *inventory) entries(ctx context.Context) error {
	var err error
	for index := 0; index < len(i.listed.Functions) && err == nil; index++ {
		err = (&inventoryEntry{inventory: i, entry: i.listed.Functions[index]}).Read(ctx)
	}
	return err
}
func (i *inventory) advance() error {
	next := aws.ToString(i.listed.NextMarker)
	i.done = next == ""
	err := i.progress(next)
	i.marker = i.listed.NextMarker
	return err
}
func (i *inventory) progress(next string) error {
	if next != "" && next == aws.ToString(i.marker) {
		return (Diagnostic{}).Format((Diagnostic{}).InventoryCode(), "Lambda pagination did not advance")
	}
	return nil
}
func (i *inventory) complete(err error) error {
	if err == nil && !i.done {
		return (Diagnostic{}).Format((Diagnostic{}).InventoryCode(), "Lambda inventory exceeds 100 pages")
	}
	return err
}
func (i *inventory) result(err error) (map[string]FunctionState, error) {
	if err != nil {
		return nil, err
	}
	return i.functions, nil
}
