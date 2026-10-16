import "encoding/json"

type GatewayCall struct {
	Method        string          `json:"method"`
	Path          string          `json:"path"`
	Body          json.RawMessage `json:"body"`
	TimeoutMillis int64           `json:"timeout_ms"`
}
