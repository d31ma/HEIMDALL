package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// ContractPath is where the generated contract is committed.
const ContractPath = "api/openapi.json"

// OpenAPI renders the public contract from the route table, so the two cannot
// disagree. It is generated output: `heimdall contract` writes it and a test
// fails the build when the committed copy drifts. Never hand-edit the file.
func OpenAPI(version string) ([]byte, error) {
	paths := map[string]map[string]any{}

	for _, route := range (&Server{}).Routes() {
		resource, err := route.Resource(pathValueSample(route.Pattern))
		if err != nil {
			return nil, fmt.Errorf("HD0030: describe %s %s: %w", route.Method, route.Pattern, err)
		}
		// Report the resource as a template rather than the sample values, so
		// a reader sees the shape a grant must cover. Each wildcard gets a
		// distinct placeholder, so a two-wildcard route round-trips
		// unambiguously.
		template := resource
		for _, name := range wildcards(route.Pattern) {
			template = strings.ReplaceAll(template, placeholder(name), "{"+name+"}")
		}

		operation := map[string]any{
			"summary":             route.Action.String() + " on " + template,
			"operationId":         operationID(route),
			"x-heimdall-action":   route.Action.String(),
			"x-heimdall-resource": template,
			"security":            []any{map[string]any{"sessionBearer": []string{}}},
			"responses":           authorizedResponses(),
			"x-heimdall-mutating": route.Method != http.MethodGet && route.Method != http.MethodHead,
		}
		parameters := append(pathParameters(route.Pattern), queryParameters(route)...)
		if len(parameters) > 0 {
			operation["parameters"] = parameters
		}

		if paths[route.Pattern] == nil {
			paths[route.Pattern] = map[string]any{}
		}
		paths[route.Pattern][strings.ToLower(route.Method)] = operation
	}

	for _, public := range []struct{ path, summary string }{
		{"/healthz", "Liveness. Unauthenticated."},
		{"/readyz", "Readiness, including the authorization engine. Unauthenticated."},
		{"/api/v1/version", "Build version. Unauthenticated."},
	} {
		paths[public.path] = map[string]any{"get": map[string]any{
			"summary":     public.summary,
			"operationId": strings.NewReplacer("/", "_", ".", "_").Replace(strings.TrimPrefix(public.path, "/")),
			"security":    []any{},
			"responses": map[string]any{
				"200": map[string]any{"description": "ok"},
				"503": map[string]any{"description": "Not ready."},
			},
		}}
	}

	document := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "HEIMDALL control plane API",
			"version": version,
			"description": "The control-plane API. It is the contract the CLI, CI, and Terraform use; " +
				"the Tachyon web tier is a thin proxy in front of it and adds no routes.\n\n" +
				"Every route except /healthz, /readyz, and /api/v1/version resolves to exactly one " +
				"SESAME authorization action, listed as x-heimdall-action, evaluated once in middleware " +
				"before any handler runs. A 403 carries SESAME's reason_code verbatim; a 503 means the " +
				"engine did not answer and is never a bypass.\n\n" +
				"Generated from the route table by `heimdall contract`. Do not hand-edit.",
			"license": map[string]any{"name": "Apache-2.0", "identifier": "Apache-2.0"},
		},
		"servers": []any{map[string]any{
			"url": "http://127.0.0.1:8080", "description": "Default local control plane",
		}},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"sessionBearer": map[string]any{
					"type":   "http",
					"scheme": "bearer",
					"description": "Authorization: Bearer <session-id>.<session-secret>, issued by SESAME. " +
						"HEIMDALL neither mints nor verifies it locally; it forwards it to the engine.",
				},
			},
			"schemas": map[string]any{
				"Error": map[string]any{
					"type":     "object",
					"required": []string{"code"},
					"properties": map[string]any{
						"code": map[string]any{
							"type": "string", "pattern": "^HD[0-9]{4}$",
							"description": "HEIMDALL diagnostic code.",
						},
						"message":     map[string]any{"type": "string"},
						"reason_code": map[string]any{"type": "string", "description": "SESAME's decision reason, present on 403."},
					},
				},
			},
		},
		"paths": paths,
	}

	// Indented and newline-terminated so the committed file diffs cleanly.
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("HD0031: encode contract: %w", err)
	}
	return buffer.Bytes(), nil
}

func authorizedResponses() map[string]any {
	errorRef := map[string]any{"content": map[string]any{
		"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Error"}},
	}}
	with := func(description string) map[string]any {
		response := map[string]any{"description": description}
		for key, value := range errorRef {
			response[key] = value
		}
		return response
	}
	return map[string]any{
		"200": map[string]any{"description": "Authorized."},
		"400": with("Resource identifier is not well formed."),
		"401": with("No valid session."),
		"403": with("Denied by SESAME. Carries reason_code."),
		"404": with("No such application."),
		"409": with("The sync completed with per-service failures."),
		"422": with("The compose file uses something this provider cannot express; the message names the offending service."),
		"503": with("Authorization engine unavailable. Never a bypass."),
	}
}

func operationID(route Route) string {
	trimmed := strings.Trim(strings.NewReplacer("{", "", "}", "").Replace(route.Pattern), "/")
	return strings.ToLower(route.Method) + "_" + strings.ReplaceAll(trimmed, "/", "_")
}

// queryParameters describes the query strings the handlers read. Unlike path
// parameters these cannot be derived from the pattern, so they are listed —
// keyed by the route they belong to, with a test asserting every key still
// names a route, so a renamed route cannot leave a description behind.
//
// Two of them page: `before` and `revisions_before` carry opaque cursors a
// caller echoes back from a previous answer rather than constructs.
var routeQuery = map[string][]queryParameter{
	"GET /api/v1/projects/{project}/apps/{app}/history": {
		{"limit", "integer", "How many rows per page. Defaults to 50."},
		{"before", "string", "Opaque cursor from next_cursor: the activity page continues from there."},
		{"revisions_before", "string", "Opaque cursor from revisions_cursor: revisions page separately."},
	},
	"GET /api/v1/projects/{project}/apps/{app}/metrics": {
		{"instance", "string", "Required. The instance to read, from the instances route."},
		{"service", "string", "Narrows to one compose service."},
		{"window", "string", "How far back to read, as a Go duration such as 15m, 6h, or 24h. " +
			"Absent reads everything retained; longer than 14d is capped at 14d. " +
			"HD0282 when it is not a positive duration."},
	},
	"GET /api/v1/projects/{project}/apps/{app}/logs": {
		{"instance", "string", "Required. The instance to tail."},
		{"service", "string", "Narrows to one compose service."},
		{"tail", "integer", "How many trailing lines. Defaults to 200."},
		{"follow", "boolean", "true streams as lines arrive rather than returning the tail."},
	},
	"GET /api/v1/audit": {
		{"limit", "integer", "How many entries per page. Defaults to 100."},
		{"before", "string", "Opaque cursor from next_cursor."},
	},
}

type queryParameter struct{ name, kind, description string }

func queryParameters(route Route) []any {
	var parameters []any
	for _, parameter := range routeQuery[route.Method+" "+route.Pattern] {
		parameters = append(parameters, map[string]any{
			"name": parameter.name, "in": "query", "required": false,
			"description": parameter.description,
			"schema":      map[string]any{"type": parameter.kind},
		})
	}
	return parameters
}

func pathParameters(pattern string) []any {
	var parameters []any
	for _, name := range wildcards(pattern) {
		parameters = append(parameters, map[string]any{
			"name": name, "in": "path", "required": true,
			"schema": map[string]any{"type": "string", "pattern": "^[a-z0-9._-]+$"},
		})
	}
	return parameters
}

func wildcards(pattern string) []string {
	var names []string
	for _, segment := range strings.Split(pattern, "/") {
		if name, ok := strings.CutPrefix(segment, "{"); ok {
			names = append(names, strings.TrimSuffix(name, "}"))
		}
	}
	sort.Strings(names)
	return names
}

// placeholder is the stand-in value a wildcard takes while describing a
// route. It must satisfy auth.Resource's segment alphabet and must be unique
// per wildcard so the substitution back to {name} is unambiguous.
func placeholder(name string) string { return name + "-id" }

// pathValueSample builds a request carrying placeholder path values, so a
// route's own resolver reports the resource shape rather than this file
// re-deriving it and drifting.
func pathValueSample(pattern string) *http.Request {
	request, _ := http.NewRequest(http.MethodGet, "/", nil)
	for _, name := range wildcards(pattern) {
		request.SetPathValue(name, placeholder(name))
	}
	return request
}
