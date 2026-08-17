package render_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d31ma/heimdall/internal/render"
	"github.com/d31ma/heimdall/internal/spec"
)

func file(name, body string) render.File {
	return render.File{Name: name, Data: []byte(body)}
}

func mustRender(t *testing.T, in render.Input) spec.DeploySpec {
	t.Helper()
	rendered, err := render.Render(in)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return rendered
}

// renderError asserts a failure and returns the message, so a test can check
// that it names the offending thing rather than merely failing.
func renderError(t *testing.T, in render.Input) string {
	t.Helper()
	rendered, err := render.Render(in)
	if err == nil {
		canonical, _ := spec.Canonical(rendered)
		t.Fatalf("expected a rejection, got a spec: %s", canonical)
	}
	return err.Error()
}

const base = `
services:
  api:
    image: ghcr.io/example/api:1.4.2
    command: ["/bin/api", "--listen", ":8000"]
    environment:
      LOG_LEVEL: info
      DATABASE_URL: "${secret:vault/checkout#database_url}"
    ports:
      - "8000"
    depends_on:
      - db
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-fsS", "http://localhost:8000/healthz"]
      interval: 10s
      timeout: 2s
      retries: 3
    deploy:
      replicas: 3
      resources:
        limits:
          cpus: "0.5"
          memory: 512M
  db:
    image: postgres:16.4-alpine
    environment:
      POSTGRES_PASSWORD: "${secret:vault/checkout#postgres_password}"
    volumes:
      - pgdata:/var/lib/postgresql/data
`

func TestRendersTheCorpusShape(t *testing.T) {
	rendered := mustRender(t, render.Input{App: "checkout", Revision: "a1b2c3d", Files: []render.File{file("compose.yaml", base)}})

	if len(rendered.Services) != 2 {
		t.Fatalf("rendered %d services, want 2", len(rendered.Services))
	}
	api, ok := rendered.Service("api")
	if !ok {
		t.Fatal("api service missing")
	}
	if api.Replicas != 3 {
		t.Errorf("replicas = %d, want 3", api.Replicas)
	}
	if api.Resources == nil || api.Resources.CPUMillis != 500 || api.Resources.MemoryMiB != 512 {
		t.Errorf("resources = %+v, want 500 millis / 512 MiB", api.Resources)
	}
	if api.Healthcheck == nil || api.Healthcheck.IntervalMS != 10000 || api.Healthcheck.TimeoutMS != 2000 {
		t.Errorf("healthcheck = %+v, want 10s interval and 2s timeout in millis", api.Healthcheck)
	}
	// "8000" with no host part publishes on the same port.
	if len(api.Ports) != 1 || api.Ports[0].Published != 8000 || api.Ports[0].Target != 8000 || api.Ports[0].Protocol != "tcp" {
		t.Errorf("ports = %+v", api.Ports)
	}
	if len(api.DependsOn) != 1 || api.DependsOn[0] != "db" {
		t.Errorf("depends_on = %v", api.DependsOn)
	}
}

// TestSecretsAreReferencesNeverValues is the property the whole secret model
// rests on. The rendered document must carry a reference and nothing that
// could be a value.
func TestSecretsAreReferencesNeverValues(t *testing.T) {
	rendered := mustRender(t, render.Input{
		App: "checkout", Revision: "a1b2c3d",
		Files:     []render.File{file("compose.yaml", base)},
		Variables: map[string]string{"DATABASE_URL": "postgres://real:credential@host/db"},
	})

	api, _ := rendered.Service("api")
	for _, env := range api.Env {
		if env.Key != "DATABASE_URL" {
			continue
		}
		if env.Value != "" {
			t.Fatalf("DATABASE_URL carries a value %q; it must carry only a reference", env.Value)
		}
		if env.Ref != "vault/checkout#database_url" {
			t.Fatalf("ref = %q", env.Ref)
		}
	}

	// Even with a same-named variable supplied, the resolved value must not
	// appear anywhere in the canonical document.
	canonical, err := spec.Canonical(rendered)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if strings.Contains(string(canonical), "real:credential") {
		t.Fatalf("a variable value leaked into the rendered spec: %s", canonical)
	}
}

func TestSecretReferenceMayNotBeInterpolatedIntoALargerString(t *testing.T) {
	message := renderError(t, render.Input{
		App: "checkout", Revision: "a1b2c3d",
		Files: []render.File{file("compose.yaml", `
services:
  api:
    image: nginx:1.27
    environment:
      DSN: "postgres://user:${secret:vault/db#password}@host/app"
`)},
	})
	if !strings.Contains(message, "entire value") {
		t.Fatalf("rejection does not explain the rule: %s", message)
	}
	if !strings.Contains(message, "DSN") {
		t.Fatalf("rejection does not name the offending key: %s", message)
	}
}

// TestUnmodelledDirectiveIsRejectedByName is the fail-closed guarantee: a
// Compose feature HEIMDALL does not model must never be dropped.
func TestUnmodelledDirectiveIsRejectedByName(t *testing.T) {
	for directive, body := range map[string]string{
		"networks": `
services:
  web:
    image: nginx:1.27
    networks: [frontend]
`,
		"build": `
services:
  web:
    image: nginx:1.27
    build: .
`,
		"privileged": `
services:
  web:
    image: nginx:1.27
    privileged: true
`,
	} {
		t.Run(directive, func(t *testing.T) {
			message := renderError(t, render.Input{
				App: "checkout", Revision: "a1b2c3d",
				Files: []render.File{file("compose.yaml", body)},
			})
			if !strings.Contains(message, directive) {
				t.Fatalf("rejection does not name %q: %s", directive, message)
			}
		})
	}
}

func TestOverlayMergeFollowsComposeRules(t *testing.T) {
	overlay := `
services:
  api:
    image: ghcr.io/example/api:2.0.0
    environment:
      LOG_LEVEL: debug
      EXTRA: "1"
    ports:
      - "9000:9000"
    deploy:
      replicas: 5
`
	rendered := mustRender(t, render.Input{
		App: "checkout", Revision: "a1b2c3d",
		Files: []render.File{file("compose.yaml", base), file("compose.prod.yaml", overlay)},
	})
	api, _ := rendered.Service("api")

	if api.Image != "ghcr.io/example/api:2.0.0" {
		t.Errorf("scalar not replaced: image = %q", api.Image)
	}
	if api.Replicas != 5 {
		t.Errorf("nested scalar not replaced: replicas = %d", api.Replicas)
	}
	// Ports concatenate; environment merges key by key.
	if len(api.Ports) != 2 {
		t.Errorf("ports = %+v, want the base and overlay concatenated", api.Ports)
	}
	env := map[string]string{}
	for _, e := range api.Env {
		env[e.Key] = e.Value
	}
	if env["LOG_LEVEL"] != "debug" {
		t.Errorf("overlay did not override LOG_LEVEL: %q", env["LOG_LEVEL"])
	}
	if env["EXTRA"] != "1" {
		t.Errorf("overlay key missing")
	}
	// The base's command survives an overlay that does not mention it.
	if strings.Join(api.Command, " ") != "/bin/api --listen :8000" {
		t.Errorf("command = %v", api.Command)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	in := render.Input{
		App: "checkout", Revision: "a1b2c3d",
		Files: []render.File{file("compose.yaml", base)},
	}
	first, err := spec.Hash(mustRender(t, in))
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	// Map iteration order differs between runs; ten renders must still agree.
	for i := 0; i < 10; i++ {
		again, err := spec.Hash(mustRender(t, in))
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if again != first {
			t.Fatalf("render %d produced %s, first produced %s", i, again, first)
		}
	}
}

func TestInterpolation(t *testing.T) {
	rendered := mustRender(t, render.Input{
		App: "checkout", Revision: "a1b2c3d",
		Variables: map[string]string{"TAG": "1.9.0", "SET_BUT_EMPTY": ""},
		Files: []render.File{file("compose.yaml", `
services:
  web:
    image: "nginx:${TAG}"
    environment:
      WITH_DEFAULT: "${MISSING:-fallback}"
      EMPTY_TAKES_DEFAULT: "${SET_BUT_EMPTY:-used}"
      LITERAL_DOLLAR: "cost is $$5"
`)},
	})
	web, _ := rendered.Service("web")
	if web.Image != "nginx:1.9.0" {
		t.Errorf("image = %q", web.Image)
	}
	env := map[string]string{}
	for _, e := range web.Env {
		env[e.Key] = e.Value
	}
	if env["WITH_DEFAULT"] != "fallback" {
		t.Errorf("WITH_DEFAULT = %q", env["WITH_DEFAULT"])
	}
	if env["EMPTY_TAKES_DEFAULT"] != "used" {
		t.Errorf("EMPTY_TAKES_DEFAULT = %q", env["EMPTY_TAKES_DEFAULT"])
	}
	if env["LITERAL_DOLLAR"] != "cost is $5" {
		t.Errorf("LITERAL_DOLLAR = %q", env["LITERAL_DOLLAR"])
	}
}

// TestUndefinedVariableFails keeps a render from depending on the machine
// that ran it.
func TestUndefinedVariableFails(t *testing.T) {
	t.Setenv("TAG", "from-the-host")
	message := renderError(t, render.Input{
		App: "checkout", Revision: "a1b2c3d",
		Files: []render.File{file("compose.yaml", "services:\n  web:\n    image: \"nginx:${TAG}\"\n")},
	})
	if !strings.Contains(message, "host environment is not consulted") {
		t.Fatalf("rejection does not explain why: %s", message)
	}
}

// TestInterpolationIsSinglePass proves a variable's value is data, not a
// further instruction.
func TestInterpolationIsSinglePass(t *testing.T) {
	rendered := mustRender(t, render.Input{
		App: "checkout", Revision: "a1b2c3d",
		Variables: map[string]string{"OUTER": "${INNER}", "INNER": "should-not-appear"},
		Files: []render.File{file("compose.yaml", `
services:
  web:
    image: nginx:1.27
    environment:
      VALUE: "${OUTER}"
`)},
	})
	web, _ := rendered.Service("web")
	if web.Env[0].Value != "${INNER}" {
		t.Fatalf("expansion recursed: %q", web.Env[0].Value)
	}
}

func TestHostEnvironmentPassThroughIsRefused(t *testing.T) {
	message := renderError(t, render.Input{
		App: "checkout", Revision: "a1b2c3d",
		Files: []render.File{file("compose.yaml", `
services:
  web:
    image: nginx:1.27
    environment:
      - AWS_SECRET_ACCESS_KEY
`)},
	})
	if !strings.Contains(message, "host environment") {
		t.Fatalf("rejection does not explain the rule: %s", message)
	}
}

func TestDependencyErrors(t *testing.T) {
	t.Run("unknown service", func(t *testing.T) {
		message := renderError(t, render.Input{
			App: "checkout", Revision: "a1b2c3d",
			Files: []render.File{file("compose.yaml", `
services:
  web:
    image: nginx:1.27
    depends_on: [cache]
`)},
		})
		if !strings.Contains(message, "cache") {
			t.Fatalf("rejection does not name the missing service: %s", message)
		}
	})

	t.Run("cycle", func(t *testing.T) {
		message := renderError(t, render.Input{
			App: "checkout", Revision: "a1b2c3d",
			Files: []render.File{file("compose.yaml", `
services:
  a:
    image: nginx:1.27
    depends_on: [b]
  b:
    image: nginx:1.27
    depends_on: [c]
  c:
    image: nginx:1.27
    depends_on: [a]
`)},
		})
		if !strings.Contains(message, "cycle") {
			t.Fatalf("cycle not reported: %s", message)
		}
	})
}

func TestWaveLabelBecomesAFieldAndLeavesLabels(t *testing.T) {
	rendered := mustRender(t, render.Input{
		App: "checkout", Revision: "a1b2c3d",
		Files: []render.File{file("compose.yaml", `
services:
  migrate:
    image: ghcr.io/example/migrate:1
    labels:
      heimdall.wave: "1"
      team: payments
`)},
	})
	migrate, _ := rendered.Service("migrate")
	if migrate.Wave != 1 {
		t.Errorf("wave = %d, want 1", migrate.Wave)
	}
	for _, label := range migrate.Labels {
		if label.Key == "heimdall.wave" {
			t.Error("the wave label survived into labels, where it could drift from the field")
		}
	}
	if len(migrate.Labels) != 1 || migrate.Labels[0].Key != "team" {
		t.Errorf("labels = %+v", migrate.Labels)
	}
}

func TestMetricsLabelBecomesAFieldAndLeavesLabels(t *testing.T) {
	rendered := mustRender(t, render.Input{
		App: "checkout", Revision: "a1b2c3d",
		Files: []render.File{file("compose.yaml", `
services:
  api:
    image: ghcr.io/example/api:1
    labels:
      heimdall.metrics: "memory, cpu"
`)},
	})
	api, _ := rendered.Service("api")
	// Normalize sorts the selection, so the hash does not depend on the order
	// someone typed the label in.
	if len(api.Metrics) != 2 || api.Metrics[0] != "cpu" || api.Metrics[1] != "memory" {
		t.Errorf("metrics = %v, want [cpu memory]", api.Metrics)
	}
	for _, label := range api.Labels {
		if label.Key == "heimdall.metrics" {
			t.Error("the metrics label survived into labels, where it could drift from the field")
		}
	}
}

func TestMalformedValuesAreNamed(t *testing.T) {
	cases := map[string]struct{ body, wants string }{
		"port range":      {"services:\n  w:\n    image: n:1\n    ports: [\"8000-8010:80\"]\n", "ports"},
		"relative volume": {"services:\n  w:\n    image: n:1\n    volumes: [\"data:relative\"]\n", "absolute"},
		"bad restart":     {"services:\n  w:\n    image: n:1\n    restart: sometimes\n", "restart"},
		"bad duration":    {"services:\n  w:\n    image: n:1\n    healthcheck:\n      test: [\"CMD\", \"true\"]\n      interval: soon\n", "duration"},
		"bad memory":      {"services:\n  w:\n    image: n:1\n    deploy:\n      resources:\n        limits:\n          memory: lots\n", "byte size"},
		"missing image":   {"services:\n  w:\n    restart: always\n", "image"},
		"bad wave":        {"services:\n  w:\n    image: n:1\n    labels:\n      heimdall.wave: soon\n", "integer"},
		"bad metric":      {"services:\n  w:\n    image: n:1\n    labels:\n      heimdall.metrics: cpu,ram\n", "not a metric group"},
		"uppercase name":  {"services:\n  Web:\n    image: n:1\n", "service name"},
		"secret as image": {"services:\n  w:\n    image: \"${secret:vault/img}\"\n", "image"},
		"no services":     {"volumes:\n  data: {}\n", "no services"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			message := renderError(t, render.Input{
				App: "checkout", Revision: "a1b2c3d",
				Files: []render.File{file("compose.yaml", c.body)},
			})
			if !strings.Contains(message, c.wants) {
				t.Fatalf("rejection does not mention %q: %s", c.wants, message)
			}
		})
	}
}

func TestOversizedFileIsRefused(t *testing.T) {
	message := renderError(t, render.Input{
		App: "checkout", Revision: "a1b2c3d",
		Files: []render.File{file("compose.yaml", "# "+strings.Repeat("x", 1<<20))},
	})
	if !strings.Contains(message, "limit") {
		t.Fatalf("rejection does not mention the limit: %s", message)
	}
}

// TestCorpusRendersAndValidates closes the loop from Phase 0: every supported
// fixture renders, and what it renders to satisfies the DeploySpec schema.
func TestCorpusRendersAndValidates(t *testing.T) {
	corpus, err := filepath.Abs(filepath.Join("..", "..", "testdata", "compose"))
	if err != nil {
		t.Fatalf("resolve corpus: %v", err)
	}
	for _, name := range []string{"single-service.yaml", "multi-service.yaml", "cloud-hostile.yaml"} {
		t.Run(name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(corpus, name))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			rendered := mustRender(t, render.Input{
				App: "checkout", Revision: "a1b2c3d",
				Files: []render.File{{Name: name, Data: body}},
			})
			canonical, err := spec.Canonical(rendered)
			if err != nil {
				t.Fatalf("canonicalize: %v", err)
			}
			var asMap map[string]any
			if err := json.Unmarshal(canonical, &asMap); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(rendered.Services) == 0 {
				t.Fatal("rendered no services")
			}
		})
	}

	// And the negative fixture must still be refused by render, not only by
	// the schema.
	body, err := os.ReadFile(filepath.Join(corpus, "unsupported-directive.yaml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	message := renderError(t, render.Input{
		App: "checkout", Revision: "a1b2c3d",
		Files: []render.File{{Name: "unsupported-directive.yaml", Data: body}},
	})
	if !strings.Contains(message, "networks") {
		t.Fatalf("rejection does not name the directive: %s", message)
	}
}

func TestFileSecretsRenderFromDeclaredRefs(t *testing.T) {
	rendered := mustRender(t, render.Input{
		App: "checkout", Revision: "a1b2c3d",
		Files: []render.File{file("compose.yaml", `
services:
  api:
    image: ghcr.io/example/api:1
    secrets:
      - db_password
      - source: tls_cert
        target: cert.pem
secrets:
  db_password:
    x-heimdall-ref: sops/secrets.enc.yaml#db_password
  tls_cert:
    x-heimdall-ref: local/tls-cert
`)},
	})
	api, _ := rendered.Service("api")
	if len(api.Secrets) != 2 {
		t.Fatalf("secrets = %+v, want 2", api.Secrets)
	}
	// Normalize sorts by name.
	if api.Secrets[0].Name != "db_password" || api.Secrets[0].Ref != "sops/secrets.enc.yaml#db_password" {
		t.Fatalf("first mount: %+v", api.Secrets[0])
	}
	if api.Secrets[1].Name != "tls_cert" || api.Secrets[1].Target != "cert.pem" {
		t.Fatalf("second mount: %+v", api.Secrets[1])
	}
}

func TestFileSecretValueSourcesAreRefusedByName(t *testing.T) {
	cases := map[string]struct{ body, wants string }{
		"plaintext file": {"services:\n  w:\n    image: n:1\n    secrets: [s]\nsecrets:\n  s:\n    file: ./secret.txt\n", "plaintext"},
		"host env":       {"services:\n  w:\n    image: n:1\n    secrets: [s]\nsecrets:\n  s:\n    environment: HOST_SECRET\n", "machine"},
		"external":       {"services:\n  w:\n    image: n:1\n    secrets: [s]\nsecrets:\n  s:\n    external: true\n", "nothing declared"},
		"no source":      {"services:\n  w:\n    image: n:1\n    secrets: [s]\nsecrets:\n  s: {}\n", "x-heimdall-ref"},
		"undeclared":     {"services:\n  w:\n    image: n:1\n    secrets: [ghost]\n", "not declared"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			message := renderError(t, render.Input{
				App: "checkout", Revision: "a1b2c3d",
				Files: []render.File{file("compose.yaml", c.body)},
			})
			if !strings.Contains(message, c.wants) {
				t.Fatalf("rejection does not mention %q: %s", c.wants, message)
			}
		})
	}
}
