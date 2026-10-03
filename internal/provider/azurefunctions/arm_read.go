package azurefunctions

import (
	"context"
	"net/http"

	"github.com/d31ma/heimdall/internal/sequence"
)

type armRead struct {
	client       *armClient
	context      context.Context
	endpoint     string
	destination  any
	allowMissing bool
	response     *http.Response
	missing      bool
}

func (r *armRead) run() error {
	return (sequence.Steps{Actions: []func() error{r.request, r.status, r.closeMissing, r.decode}}).Run()
}
func (r *armRead) request() error {
	response, err := r.client.request(r.context, http.MethodGet, r.endpoint, nil)
	r.response = response
	return err
}
func (r *armRead) status() error {
	r.missing = r.isMissing()
	return r.requireSuccess()
}
func (r *armRead) requireSuccess() error {
	if r.response.StatusCode != http.StatusOK && !r.missing {
		return (armResponse{response: r.response}).failure()
	}
	return nil
}
func (r *armRead) closeMissing() error {
	if r.missing {
		return r.response.Body.Close()
	}
	return nil
}
func (r *armRead) decode() error {
	if r.missing {
		return nil
	}
	return (armResponse{response: r.response}).decode(r.destination)
}

func (r armRead) isMissing() bool {
	return r.allowMissing && r.response.StatusCode == http.StatusNotFound
}
