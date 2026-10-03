package azurefunctions

import (
	"context"
	"net/url"
	"regexp"
	"strings"

	"github.com/d31ma/heimdall/internal/sequence"
)

type revisionResource struct {
	client       *armClient
	context      context.Context
	name         string
	revisionName string
	endpoint     *url.URL
	revision     nativeRevision
	missing      bool
}

func (r revisionResource) read(ctx context.Context) (*nativeRevision, error) {
	bounded, cancel := context.WithTimeout(ctx, (functionContract{}).readTimeout())
	defer cancel()
	r.context = bounded
	err := (sequence.Steps{Actions: []func() error{r.validateNames, r.parseEndpoint, r.fetch, r.identify}}).Run()
	return r.result(err)
}
func (r revisionResource) validateNames() error {
	valid := regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,255}$`)
	if !valid.MatchString(r.name) || !valid.MatchString(r.revisionName) {
		return &AdapterError{Code: (functionContract{}).readCode(), Message: "ARM returned an invalid native function or ready revision name"}
	}
	return nil
}
func (r *revisionResource) parseEndpoint() error {
	endpoint, err := url.Parse(r.client.path(r.name))
	r.endpoint = endpoint
	return err
}
func (r *revisionResource) fetch() error {
	r.endpoint.Path += "/revisions/" + r.revisionName
	r.endpoint.RawPath = ""
	read := armRead{client: r.client, context: r.context, endpoint: r.endpoint.String(), destination: &r.revision, allowMissing: true}
	err := read.run()
	r.missing = read.missing
	return err
}
func (r *revisionResource) identify() error {
	name := (optionalValue[string]{}).read(r.revision.Name)
	id := (optionalValue[string]{}).read(r.revision.ID)
	return r.compareIdentity(name, id)
}
func (r *revisionResource) compareIdentity(name, id string) error {
	if !r.missing && (name != r.revisionName || !strings.EqualFold(id, r.endpoint.Path)) {
		return &AdapterError{Code: (functionContract{}).readCode(), Message: "ARM ready revision response does not identify the requested resource"}
	}
	return nil
}
func (r revisionResource) result(err error) (*nativeRevision, error) {
	if err != nil || r.missing {
		return nil, err
	}
	return &r.revision, nil
}
