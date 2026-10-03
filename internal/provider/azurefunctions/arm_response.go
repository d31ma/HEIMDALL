package azurefunctions

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type armResponse struct{ response *http.Response }

func (r armResponse) decode(into any) error {
	response := r.response
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1048577))
	if err != nil {
		return err
	}
	if len(data) > 1048576 {
		return (&AdapterError{Code: "HD0472", Message: "ARM response exceeds the 1MiB read limit"})
	}
	return json.Unmarshal(data, into)
}

func (r armResponse) failure() error {
	response := r.response
	defer response.Body.Close()
	return (&AdapterError{Code: "HD0473", Message: fmt.Sprintf("ARM returned HTTP %d; inspect provider request %q", response.StatusCode, response.Header.Get("x-ms-request-id"))})
}
