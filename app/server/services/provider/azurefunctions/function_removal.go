package azurefunctions

import (
	"context"
	"net/http"

	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type functionRemoval struct {
	client   *armClient
	name     string
	live     *nativeApp
	context  context.Context
	response *http.Response
}

func (r functionRemoval) remove(ctx context.Context) error {
	r.context = ctx
	return r.removeExisting()
}
func (r *functionRemoval) removeExisting() error {
	if r.live == nil {
		return nil
	}
	return r.perform()
}
func (r *functionRemoval) perform() error {
	return (sequence.Steps{Actions: []func() error{r.request, r.status, r.close, r.wait}}).Run()
}
func (r *functionRemoval) request() error {
	response, err := r.client.request(r.context, http.MethodDelete, r.client.path(r.name), nil)
	r.response = response
	return err
}
func (r *functionRemoval) status() error {
	if r.response.StatusCode != http.StatusOK && r.response.StatusCode != http.StatusAccepted && r.response.StatusCode != http.StatusNoContent && r.response.StatusCode != http.StatusNotFound {
		return (armResponse{response: r.response}).failure()
	}
	return nil
}
func (r *functionRemoval) close() error { return r.response.Body.Close() }
func (r *functionRemoval) wait() error {
	return (functionConvergence{client: r.client, name: r.name}).wait(r.context)
}
