package azurefunctions

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type armResponse struct {
	response    *http.Response
	data        []byte
	destination any
}

func (r armResponse) decode(into any) error {
	r.destination = into
	defer r.response.Body.Close()
	return (sequence.Steps{Actions: []func() error{r.read, r.limit, r.unmarshal}}).Run()
}
func (r *armResponse) read() error {
	data, err := io.ReadAll(io.LimitReader(r.response.Body, (functionContract{}).responseBytes()+1))
	r.data = data
	return err
}
func (r *armResponse) limit() error {
	if int64(len(r.data)) > (functionContract{}).responseBytes() {
		return &AdapterError{Code: (functionContract{}).readCode(), Message: "ARM response exceeds the 1MiB read limit"}
	}
	return nil
}
func (r *armResponse) unmarshal() error { return json.Unmarshal(r.data, r.destination) }
func (r armResponse) failure() error {
	defer r.response.Body.Close()
	return &AdapterError{Code: (functionContract{}).responseCode(), Message: fmt.Sprintf("ARM returned HTTP %d; inspect provider request %q", r.response.StatusCode, r.response.Header.Get("x-ms-request-id"))}
}
