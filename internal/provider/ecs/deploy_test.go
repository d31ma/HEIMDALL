package ecs_test

import (
	"os"
	"testing"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/provider/ecs"
	"github.com/d31ma/heimdall/internal/render"
)

// TestWebsiteComposeDeploysToECS keeps the dogfood honest: the repository
// ships its own website as a HEIMDALL-deployable declaration
// (website/deploy/compose.yaml), and this asserts it renders and clears the
// ECS adapter's capability matrix — so an edit to either can never quietly
// break the product's own deployment.
func TestWebsiteComposeDeploysToECS(t *testing.T) {
	body, err := os.ReadFile("../../../website/deploy/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := render.Render(render.Input{
		App: "website", Revision: "a1b2c3d",
		Files: []render.File{{Name: "compose.yaml", Data: body}},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := provider.Validate((&ecs.Provider{}).Capabilities(), rendered); err != nil {
		t.Fatalf("the ECS adapter rejects the website's own declaration: %v", err)
	}
	service := rendered.Services[0]
	if service.Replicas < 2 || service.Healthcheck == nil || service.Resources == nil {
		t.Fatalf("the website declaration lost its production shape: %+v", service)
	}
}
