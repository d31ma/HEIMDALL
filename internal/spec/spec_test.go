package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

// sample is deliberately built out of order: unsorted services, unsorted env,
// unsorted ports. Canonical must produce the same bytes as the sorted form.
func sample() DeploySpec {
	return DeploySpec{
		App:      "checkout",
		Revision: "a1b2c3d4e5f6",
		Services: []Service{
			{
				Name:  "web",
				Image: "nginx:1.27.3",
				Env: []EnvVar{
					{Key: "LOG_LEVEL", Value: "info"},
					{Key: "API_URL", Value: "http://api:8000"},
				},
				Ports: []Port{
					{Published: 8443, Target: 443, Protocol: "tcp"},
					{Published: 8080, Target: 80, Protocol: "tcp"},
				},
				Command:   []string{"nginx", "-g", "daemon off;"},
				DependsOn: []string{"api"},
			},
			{
				Name:     "api",
				Image:    "ghcr.io/example/api:1.4.2",
				Replicas: 3,
				Env:      []EnvVar{{Key: "DATABASE_URL", Ref: "vault/checkout#database_url"}},
			},
		},
	}
}

func TestCanonicalIsOrderIndependent(t *testing.T) {
	unsorted := sample()

	sorted := sample()
	sorted.Services[0], sorted.Services[1] = sorted.Services[1], sorted.Services[0]

	a, err := Canonical(unsorted)
	if err != nil {
		t.Fatalf("canonicalize unsorted: %v", err)
	}
	b, err := Canonical(sorted)
	if err != nil {
		t.Fatalf("canonicalize sorted: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("canonical form depends on input order:\n%s\n%s", a, b)
	}
}

// TestHashIsPinned is the guard the whole revision model rests on. If this
// value changes, every stored revision hash in every deployment changes with
// it, so the failure must be a deliberate SchemaVersion bump and never a
// drive-by struct edit.
func TestHashIsPinned(t *testing.T) {
	const want = "sha256:c2777fc3b0c40107189a58c9d9857a1e0475fec9ca18d77df1ae5df2bb1f225f"

	got, err := Hash(sample())
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if got != want {
		canonical, _ := Canonical(sample())
		t.Fatalf("content hash changed\n  want %s\n  got  %s\n  canonical: %s\n"+
			"If this was intentional, bump SchemaVersion and update this constant.",
			want, got, canonical)
	}
}

func TestCanonicalDoesNotEscapeHTML(t *testing.T) {
	spec := DeploySpec{App: "checkout", Revision: "a1b2c3d", Services: []Service{
		{Name: "web", Image: "registry.example/app:1", Labels: []Label{{Key: "note", Value: "a<b&c>d"}}},
	}}
	canonical, err := Canonical(spec)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if strings.Contains(string(canonical), `\u003c`) {
		t.Fatalf("canonical form escaped HTML, making the hash depend on an encoder setting: %s", canonical)
	}
}

func TestNormalizeIsIdempotent(t *testing.T) {
	spec := sample()
	spec.Normalize()
	first, _ := json.Marshal(spec)
	spec.Normalize()
	second, _ := json.Marshal(spec)
	if string(first) != string(second) {
		t.Fatalf("Normalize is not idempotent:\n%s\n%s", first, second)
	}
}

func TestNormalizeDefaultsReplicasButKeepsArgvOrder(t *testing.T) {
	spec := sample()
	spec.Normalize()

	web, ok := spec.Service("web")
	if !ok {
		t.Fatal("web service missing after normalize")
	}
	if web.Replicas != 1 {
		t.Fatalf("replicas defaulted to %d, want 1", web.Replicas)
	}
	// Sorting argv would change what the container runs.
	if got := strings.Join(web.Command, " "); got != "nginx -g daemon off;" {
		t.Fatalf("command reordered: %q", got)
	}
}
