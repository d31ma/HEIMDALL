package azurefunctions

import (
	"context"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/d31ma/heimdall/internal/sequence"
)

type armRequest struct {
	client   *armClient
	context  context.Context
	method   string
	endpoint string
	body     any
	request  *policy.Request
	response *http.Response
}

func (r *armRequest) run() (*http.Response, error) {
	err := (sequence.Steps{Actions: []func() error{r.create, r.marshal, r.send}}).Run()
	return r.result(err)
}
func (r *armRequest) create() error {
	request, err := runtime.NewRequest(r.context, r.method, r.endpoint)
	r.request = request
	return err
}
func (r *armRequest) marshal() error {
	runtime.SkipBodyDownload(r.request)
	return r.bodyJSON()
}
func (r armRequest) bodyJSON() error {
	if r.body == nil {
		return nil
	}
	return runtime.MarshalAsJSON(r.request, r.body)
}
func (r *armRequest) send() error {
	response, err := r.client.sdk.Pipeline().Do(r.request)
	r.response = response
	return err
}
func (r armRequest) result(err error) (*http.Response, error) {
	if err != nil {
		return nil, err
	}
	return r.response, nil
}
