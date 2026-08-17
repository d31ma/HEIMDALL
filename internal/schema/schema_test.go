package schema_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d31ma/heimdall/internal/schema"
	"github.com/d31ma/heimdall/internal/spec"
	"gopkg.in/yaml.v3"
)

func validator(t *testing.T) *schema.Validator {
	t.Helper()
	binary := os.Getenv("CHEX_BINARY")
	if binary == "" {
		binary = "chex"
	}
	if _, err := exec.LookPath(binary); err != nil {
		t.Skipf("chex binary not on PATH: %v", err)
	}
	v := schema.New()
	t.Cleanup(func() { _ = v.Close() })
	return v
}

func corpusDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "compose"))
	if err != nil {
		t.Fatalf("resolve corpus: %v", err)
	}
	return dir
}

// services reads one compose file and returns its services as JSON-shaped
// maps. YAML is decoded then round-tripped through JSON so the data CHEX sees
// is exactly the data the renderer will see.
func services(t *testing.T, path string) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var document struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	encoded, err := json.Marshal(document.Services)
	if err != nil {
		t.Fatalf("re-encode %s: %v", path, err)
	}
	var normalized map[string]map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		t.Fatalf("normalize %s: %v", path, err)
	}
	return normalized
}

// TestComposeCorpusValidates is half of the Phase 0 schema exit criterion:
// every supported fixture passes.
func TestComposeCorpusValidates(t *testing.T) {
	v := validator(t)
	for _, name := range []string{"single-service.yaml", "multi-service.yaml", "cloud-hostile.yaml"} {
		t.Run(name, func(t *testing.T) {
			all := services(t, filepath.Join(corpusDir(t), name))
			if len(all) == 0 {
				t.Fatal("fixture declares no services")
			}
			for service, body := range all {
				if err := v.Validate(schema.ComposeService, body); err != nil {
					t.Errorf("service %q: %v", service, err)
				}
			}
		})
	}
}

// TestUnsupportedDirectiveIsRejectedByName is the other half, and the more
// important one. A directive HEIMDALL cannot express must fail loudly and say
// which one, because a silent drop is how a deploy quietly loses its network.
func TestUnsupportedDirectiveIsRejectedByName(t *testing.T) {
	v := validator(t)
	all := services(t, filepath.Join(corpusDir(t), "unsupported-directive.yaml"))

	err := v.Validate(schema.ComposeService, all["web"])
	if err == nil {
		t.Fatal("an unmodelled compose directive validated successfully")
	}
	if !strings.Contains(err.Error(), "networks") {
		t.Fatalf("rejection does not name the offending property: %v", err)
	}
}

// TestDeploySpecValidates closes the loop: the type the renderer produces
// satisfies the schema that the stored revision is checked against.
func TestDeploySpecValidates(t *testing.T) {
	v := validator(t)

	rendered := spec.DeploySpec{
		App:      "checkout",
		Revision: "a1b2c3d4e5f6a1b2",
		Services: []spec.Service{
			{
				Name:      "api",
				Image:     "ghcr.io/example/api:1.4.2",
				Command:   []string{"/bin/api"},
				Env:       []spec.EnvVar{{Key: "DATABASE_URL", Ref: "vault/checkout#database_url"}},
				Ports:     []spec.Port{{Published: 8000, Target: 8000, Protocol: "tcp"}},
				DependsOn: []string{"db"},
				Restart:   "unless-stopped",
				Healthcheck: &spec.Healthcheck{
					Test: []string{"CMD", "curl", "-fsS", "http://localhost:8000/healthz"}, Retries: 3,
				},
				Resources: &spec.Resources{CPUMillis: 500, MemoryMiB: 512},
				Labels:    []spec.Label{{Key: "heimdall.wave", Value: "1"}},
				Replicas:  3,
			},
			{
				Name:    "db",
				Image:   "postgres:16.4-alpine",
				Volumes: []spec.Volume{{Source: "pgdata", Target: "/var/lib/postgresql/data"}},
			},
		},
	}

	canonical, err := spec.Canonical(rendered)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(canonical, &asMap); err != nil {
		t.Fatalf("decode canonical form: %v", err)
	}
	if err := v.Validate(schema.DeploySpec, asMap); err != nil {
		t.Fatalf("canonical DeploySpec fails its own schema: %v", err)
	}
}

// TestSecretRefsNeverCarryValues is the persisted-secret CI gate in test
// form: the corpus uses ${secret:...} references, and no fixture may contain
// something that looks like a resolved credential.
func TestSecretRefsNeverCarryValues(t *testing.T) {
	entries, err := os.ReadDir(corpusDir(t))
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(corpusDir(t), entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		body := string(raw)
		for _, needle := range []string{"PASSWORD: \"p", "AKIA", "-----BEGIN"} {
			if strings.Contains(body, needle) {
				t.Errorf("%s contains what looks like a literal secret (%q)", entry.Name(), needle)
			}
		}
	}
}
