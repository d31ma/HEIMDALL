package api

import "encoding/json"

type invocationInput struct {
	Payload json.RawMessage `json:"payload"`
}
