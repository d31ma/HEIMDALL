package api

import "net/http"

type invocationContract struct{}

func (c invocationContract) Apply(operation map[string]any, route Route) {
	action := c.action(operation, route)
	action()
}
func (c invocationContract) action(operation map[string]any, route Route) func() {
	if route.Method != http.MethodPost || route.Pattern != c.Pattern() {
		return func() {}
	}
	return func() { c.describe(operation) }
}
func (c invocationContract) describe(operation map[string]any) {
	operation["description"] = "Invoke the declared JSON function once using the stored application target and immutable published version. Input is transient. The API waits at most 45 seconds; a timeout does not cancel native execution and must not trigger automatic retry. A native handler error is returned in function_error with HTTP 200."
	operation["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "required": []string{"payload"}, "additionalProperties": false, "properties": map[string]any{"payload": map[string]any{"description": "Any JSON value, including null. Its UTF-8 JSON representation must not exceed 262144 bytes.", "x-heimdall-max-json-bytes": (functionLimits{}).PayloadBytes()}}}}}}
	operation["responses"] = c.responses()
}
func (invocationContract) responses() map[string]any {
	responses := authorizedResponses()
	responses["200"] = map[string]any{"description": "Native invocation response: version, request_id, payload, and optional function_error. A handler error does not imply transport failure."}
	responses["400"] = map[string]any{"description": "Invalid payload, undeclared function, suspended application, or unsupported target routing."}
	responses["409"] = map[string]any{"description": "Native resource ownership or immutable-version conflict."}
	responses["501"] = map[string]any{"description": "This provider has no qualified direct JSON invocation contract."}
	responses["504"] = map[string]any{"description": "The wait ended. Native execution may continue; do not retry automatically."}
	return responses
}
func (invocationContract) Pattern() string {
	return "/api/v1/projects/{project}/apps/{app}/services/{service}/invoke"
}
