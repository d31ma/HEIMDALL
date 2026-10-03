package spec_test

import (
	"testing"

	"github.com/d31ma/heimdall/internal/spec"
)

func TestFunctionSettingsChangeServiceHash(t *testing.T) {
	service := spec.Service{Name: "handler", Image: "registry.example/handler:1", Replicas: 1,
		Function: &spec.Function{RuntimeContract: "lambda-runtime-api", Invocation: "json", TimeoutMS: 30000}}
	before, err := spec.HashService(service)
	if err != nil {
		t.Fatal(err)
	}
	service.Function.TimeoutMS = 60000
	after, err := spec.HashService(service)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("function timeout changed without changing desired-state hash")
	}
}

func TestFunctionSelectsDistinctWireSchema(t *testing.T) {
	d := spec.DeploySpec{App: "events", Revision: "abcdef12", Services: []spec.Service{{Name: "handler", Image: "registry.example/handler:1",
		Function: &spec.Function{RuntimeContract: "lambda-runtime-api", Invocation: "json"}}}}
	d.Normalize()
	if d.SchemaVersion != "2" {
		t.Fatalf("function wire schema = %q, want 2", d.SchemaVersion)
	}
}

func TestUnknownWireSchemaIsRefused(t *testing.T) {
	d := spec.DeploySpec{SchemaVersion: "999", App: "events", Revision: "abcdef12", Services: []spec.Service{{Name: "handler", Image: "registry.example/handler:1"}}}
	if _, err := spec.Canonical(d); err == nil {
		t.Fatal("an unsupported wire schema was silently accepted")
	}
}

func TestInvalidFunctionInvocationIsRefusedBeforeHashing(t *testing.T) {
	d := spec.DeploySpec{App: "events", Revision: "abcdef12", Services: []spec.Service{{Name: "handler", Image: "registry.example/handler:1",
		Function: &spec.Function{RuntimeContract: "lambda-runtime-api", Invocation: "http"}}}}
	if _, err := spec.Hash(d); err == nil {
		t.Fatal("an undeclared Lambda HTTP event contract entered desired state")
	}
}
