package render_test

import (
	"strings"
	"testing"

	"github.com/d31ma/heimdall/internal/render"
)

func TestLambdaFunctionDeclarationRenders(t *testing.T) {
	in := render.Input{App: "events", Revision: "abcdef12", Files: []render.File{{Name: "compose.yaml", Data: []byte(`
services:
  handler:
    image: 123456789012.dkr.ecr.us-east-1.amazonaws.com/handler@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    x-heimdall-function:
      runtime_contract: lambda-runtime-api
      invocation: json
      timeout: 30s
      architecture: amd64
`)}}}
	got, err := render.Render(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Services[0].Function == nil {
		t.Fatal("function declaration was dropped")
	}
	if got.Services[0].Function.RuntimeContract != "lambda-runtime-api" || got.Services[0].Function.Invocation != "json" {
		t.Fatalf("function = %+v", got.Services[0].Function)
	}
	if got.Services[0].Function.TimeoutMS != 30000 || got.Services[0].Function.Architecture != "amd64" {
		t.Fatalf("runtime configuration = %+v", got.Services[0].Function)
	}
	if got.SchemaVersion != "2" {
		t.Fatalf("function wire schema = %q, want 2", got.SchemaVersion)
	}
}

func TestAzureHTTPFunctionDeclarationRenders(t *testing.T) {
	in := render.Input{App: "http", Revision: "abcdef12", Files: []render.File{{Name: "compose.yaml", Data: []byte(`
services:
  handler:
    image: registry.example/functions@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    x-heimdall-function:
      runtime_contract: azure-functions-v4
      invocation: http
`)}}}
	got, err := render.Render(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Services[0].Function == nil {
		t.Fatal("function declaration was dropped")
	}
	if got.Services[0].Function.RuntimeContract != "azure-functions-v4" || got.Services[0].Function.Invocation != "http" {
		t.Fatalf("function = %+v", got.Services[0].Function)
	}
}

func TestFunctionRejectsContainerPorts(t *testing.T) {
	in := render.Input{App: "events", Revision: "abcdef12", Files: []render.File{{Name: "compose.yaml", Data: []byte(`
services:
  handler:
    image: registry.example/handler:1
    ports: ["8080"]
    x-heimdall-function:
      runtime_contract: lambda-runtime-api
      invocation: json
`)}}}
	err := renderError(t, in)
	if !strings.Contains(err, "handler") || !strings.Contains(err, "ports") {
		t.Fatalf("rejection must locate the conflicting directive: %s", err)
	}
}

func TestFunctionRejectsSchedule(t *testing.T) {
	in := render.Input{App: "events", Revision: "abcdef12", Files: []render.File{{Name: "compose.yaml", Data: []byte(`
services:
  handler:
    image: registry.example/handler:1
    x-heimdall-function:
      runtime_contract: lambda-runtime-api
      invocation: json
    x-heimdall-schedule:
      cron: "0 3 * * *"
`)}}}
	err := renderError(t, in)
	if !strings.Contains(err, "handler") || !strings.Contains(err, "schedule") {
		t.Fatalf("rejection must locate the conflicting directive: %s", err)
	}
}

func TestFunctionRejectsUnknownRuntime(t *testing.T) {
	in := render.Input{App: "events", Revision: "abcdef12", Files: []render.File{{Name: "compose.yaml", Data: []byte(`
services:
  handler:
    image: registry.example/handler:1
    x-heimdall-function:
      runtime_contract: ordinary-container
      invocation: json
`)}}}
	err := renderError(t, in)
	if !strings.Contains(err, "runtime_contract") {
		t.Fatalf("rejection does not locate the unsupported runtime: %s", err)
	}
}

func TestFunctionOverlayReplacesDeclaration(t *testing.T) {
	in := render.Input{App: "events", Revision: "abcdef12", Files: []render.File{
		{Name: "compose.yaml", Data: []byte(`
services:
  handler:
    image: registry.example/handler:1
    x-heimdall-function:
      runtime_contract: lambda-runtime-api
      invocation: json
      timeout: 30s
      architecture: arm64
`)},
		{Name: "overlay.yaml", Data: []byte(`
services:
  handler:
    x-heimdall-function:
      runtime_contract: azure-functions-v4
      invocation: http
`)},
	}}
	got, err := render.Render(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Services[0].Function == nil {
		t.Fatal("function declaration was dropped")
	}
	if got.Services[0].Function.TimeoutMS != 0 || got.Services[0].Function.Architecture != "" {
		t.Fatalf("overlay inherited stale Lambda configuration: %+v", got.Services[0].Function)
	}
}

func TestLambdaHTTPInvocationIsNotSilentlyChangedToJSON(t *testing.T) {
	in := render.Input{App: "events", Revision: "abcdef12", Files: []render.File{{Name: "compose.yaml", Data: []byte(`
services:
  handler:
    image: registry.example/handler:1
    x-heimdall-function:
      runtime_contract: lambda-runtime-api
      invocation: http
`)}}}
	err := renderError(t, in)
	if !strings.Contains(err, "invocation") {
		t.Fatalf("rejection must locate the incompatible invocation contract: %s", err)
	}
}

func TestFunctionRejectsSubsecondTimeout(t *testing.T) {
	in := render.Input{App: "events", Revision: "abcdef12", Files: []render.File{{Name: "compose.yaml", Data: []byte(`
services:
  handler:
    image: registry.example/handler:1
    x-heimdall-function:
      runtime_contract: lambda-runtime-api
      invocation: json
      timeout: 500ms
`)}}}
	err := renderError(t, in)
	if !strings.Contains(err, "timeout") {
		t.Fatalf("rejection must locate the unsupported timeout: %s", err)
	}
}

func TestFunctionRejectsUnsupportedArchitecture(t *testing.T) {
	in := render.Input{App: "events", Revision: "abcdef12", Files: []render.File{{Name: "compose.yaml", Data: []byte(`
services:
  handler:
    image: registry.example/handler:1
    x-heimdall-function:
      runtime_contract: lambda-runtime-api
      invocation: json
      architecture: windows
`)}}}
	err := renderError(t, in)
	if !strings.Contains(err, "architecture") {
		t.Fatalf("rejection must locate the unsupported architecture: %s", err)
	}
}
