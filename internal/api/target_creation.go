package api

import (
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
	"github.com/d31ma/heimdall/internal/store"
	"net/http"
	"time"
)

type targetCreation struct {
	server  *Server
	writer  http.ResponseWriter
	request *http.Request
	target  store.Target
	adapter provider.Provider
}

func (c *targetCreation) Run() {
	err := (sequence.Steps{Actions: []func() error{c.available, c.decode, c.required, c.registered, c.definition, c.store}}).Run()
	action := c.responseAction(err)
	action()
}
func (c *targetCreation) available() error {
	if c.server.Store == nil {
		return &boundaryError{Code: (boundaryError{}).UnavailableCode(), Message: "target storage is unavailable"}
	}
	return nil
}
func (c *targetCreation) decode() error {
	return (&strictBody{request: c.request, writer: c.writer, until: time.Now().Add((functionLimits{}).WaitDuration()), limit: c.BodyBytes()}).Decode(&c.target)
}
func (c *targetCreation) required() error {
	if c.target.Name == "" || c.target.Provider == "" {
		return &boundaryError{Code: (boundaryError{}).BadRequestCode(), Message: "name and provider are required"}
	}
	return nil
}
func (c *targetCreation) registered() error {
	adapter, known := c.server.Providers[c.target.Provider]
	c.adapter = adapter
	return c.requireProvider(known)
}
func (c *targetCreation) requireProvider(known bool) error {
	if !known {
		return &boundaryError{Code: (boundaryError{}).BadRequestCode(), Message: "no adapter for provider " + c.target.Provider}
	}
	return nil
}
func (c *targetCreation) definition() error {
	return (provider.TargetCheck{Adapter: c.adapter, Target: c.target.Ref()}).Definition()
}
func (c *targetCreation) store() error {
	c.target.CreatedAt = c.server.now()
	id, err := store.In[store.Target](c.server.Store, store.Targets).Put(c.target)
	c.target.ID = id
	return err
}
func (c *targetCreation) responseAction(err error) func() {
	if err != nil {
		return func() { (functionError{}).Write(c.writer, err) }
	}
	return func() { writeJSON(c.writer, http.StatusCreated, c.target) }
}
func (targetCreation) BodyBytes() int { return 1 << 20 }
