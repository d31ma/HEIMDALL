package lambda

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type versionInventory struct {
	publication publication
	result      *awslambda.ListVersionsByFunctionOutput
	pagination  nativePagination
	version     string
}

func (inventory versionInventory) Version(requestContext context.Context) (string, error) {
	inventory.pagination = nativePagination{code: (Diagnostic{}).ReadinessCode(), noun: "version"}
	failure := inventory.pages(requestContext)
	return inventory.output(failure)
}
func (inventory *versionInventory) pages(requestContext context.Context) error {
	var failure error
	for page := 0; page < (functionLimits{}).InventoryPages() && !inventory.pagination.done && inventory.version == "" && failure == nil; page++ {
		failure = inventory.page(requestContext)
	}
	return failure
}
func (inventory *versionInventory) page(requestContext context.Context) error {
	return (sequence.Steps{Actions: []func() error{func() error { return inventory.fetch(requestContext) }, func() error { return inventory.candidates(requestContext) }, inventory.advance}}).Run()
}
func (inventory *versionInventory) fetch(requestContext context.Context) error {
	var failure error
	inventory.result, failure = inventory.publication.client.ListVersionsByFunction(requestContext, &awslambda.ListVersionsByFunctionInput{FunctionName: aws.String(inventory.publication.identity.Name()), Marker: inventory.pagination.token, MaxItems: aws.Int32((functionLimits{}).PageItems())})
	return failure
}
func (inventory *versionInventory) candidates(requestContext context.Context) error {
	var failure error
	for index := 0; index < len(inventory.result.Versions) && inventory.version == "" && failure == nil; index++ {
		inventory.version, failure = (versionCandidate{publication: inventory.publication, configuration: inventory.result.Versions[index]}).Version(requestContext)
	}
	return failure
}
func (inventory *versionInventory) advance() error {
	return inventory.pagination.Advance(inventory.result.NextMarker, inventory.version != "" || aws.ToString(inventory.result.NextMarker) == "")
}
func (inventory versionInventory) output(failure error) (string, error) {
	switch {
	case failure != nil:
		return "", failure
	case inventory.version != "":
		return inventory.version, nil
	case !inventory.pagination.done:
		return "", (Diagnostic{}).Format((Diagnostic{}).ReadinessCode(), "native version inventory exceeded 100 pages")
	}
	return "", (Diagnostic{}).Format((Diagnostic{}).ReadinessCode(), "publication conflicted and no matching immutable version exists")
}
