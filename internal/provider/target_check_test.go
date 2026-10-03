package provider_test

import (
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/provider/docker"
	"github.com/d31ma/heimdall/internal/provider/lambda"
	"testing"
)

func TestFunctionTargetDefinitionIsPureAndAcceptsPreStorageIdentity(t *testing.T) {
	target := provider.Target{Provider: "lambda", Region: "us-east-1", Config: map[string]string{"execution_role_arn": "arn:aws:iam::123456789012:role/existing"}}
	adapter := &lambda.Provider{EndpointOverride: "https://unreachable.example.invalid"}
	if err := (provider.TargetCheck{Adapter: adapter, Target: target}).Definition(); err != nil {
		t.Fatal(err)
	}
	target.AgentID = "agent-existing"
	if err := (provider.TargetCheck{Adapter: adapter, Target: target}).Definition(); err == nil {
		t.Fatal("native direct target accepted agent routing")
	}
	if err := (provider.TargetCheck{Adapter: &docker.Provider{}, Target: provider.Target{Provider: "docker", AgentID: "agent-existing"}}).Definition(); err != nil {
		t.Fatalf("ordinary provider routing changed: %v", err)
	}
}
