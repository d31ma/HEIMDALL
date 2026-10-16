package provider

import "encoding/json"

type Invocation struct {
	RequestID     string          `json:"request_id"`
	Version       string          `json:"version"`
	Payload       json.RawMessage `json:"payload"`
	FunctionError string          `json:"function_error,omitempty"`
}
