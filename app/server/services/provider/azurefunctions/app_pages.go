package azurefunctions

import (
	"context"

	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type appPages struct {
	client  *armClient
	context context.Context
	next    string
	apps    []*nativeApp
	page    listPage
	count   int
	err     error
}

func (p *appPages) read() ([]*nativeApp, error) {
	p.apps = []*nativeApp{}
	p.readPages()
	return p.result()
}
func (p *appPages) readPages() {
	for p.next != "" && p.err == nil {
		p.err = (sequence.Steps{Actions: []func() error{p.pageLimit, p.readPage, p.resourceLimit, p.follow}}).Run()
	}
}
func (p appPages) pageLimit() error {
	if p.count >= (functionContract{}).maximumPages() {
		return &AdapterError{Code: (functionContract{}).readCode(), Message: "ARM list exceeds the 100-page limit"}
	}
	return nil
}
func (p *appPages) readPage() error {
	p.page = listPage{}
	p.count++
	read := armRead{client: p.client, context: p.context, endpoint: p.next, destination: &p.page}
	return read.run()
}
func (p *appPages) resourceLimit() error {
	p.apps = append(p.apps, p.page.Value...)
	if len(p.apps) > (functionContract{}).maximumResources() {
		return &AdapterError{Code: (functionContract{}).readCode(), Message: "ARM list exceeds the 10000-resource limit"}
	}
	return nil
}
func (p *appPages) follow() error {
	next, err := (pageLink{}).resolve(p.client.path(""), p.page.NextLink)
	p.next = next
	return err
}
func (p appPages) result() ([]*nativeApp, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.apps, nil
}
