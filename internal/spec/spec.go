// Package spec defines DeploySpec, the provider-neutral canonical model every
// other package operates on. A DeploySpec is rendered once from a commit and
// stored immutably; diffs, rollbacks, audit, and drift all read it back.
//
// Two properties are load-bearing and both are enforced by Canonical:
//
//   - Determinism. The same commit renders to byte-identical JSON on any
//     machine, so the content hash is a stable revision identity.
//   - No maps. Every collection is a sorted slice keyed by an explicit field.
//     Go map iteration order is unstable, and CHEX cannot express a record
//     whose values are objects, so a map here would break both the hash and
//     the schema.
package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SchemaVersion is the DeploySpec wire version. Bump it only for a change
// that an older reader cannot interpret; the value is part of the content
// hash, so a bump invalidates every stored revision hash by design.
const SchemaVersion = "1"

// DeploySpec is the rendered desired state of one application at one revision.
type DeploySpec struct {
	SchemaVersion string    `json:"schema_version"`
	App           string    `json:"app"`
	Revision      string    `json:"revision"`
	Services      []Service `json:"services"`
}

// Service is one compose service, flattened to the subset every provider can
// be asked about. Fields a provider cannot express are rejected at plan time
// by Capabilities rather than silently dropped here.
type Service struct {
	Name        string       `json:"name"`
	Image       string       `json:"image"`
	Entrypoint  []string     `json:"entrypoint,omitempty"`
	Command     []string     `json:"command,omitempty"`
	Env         []EnvVar     `json:"env,omitempty"`
	Ports       []Port       `json:"ports,omitempty"`
	Volumes     []Volume     `json:"volumes,omitempty"`
	DependsOn   []string     `json:"depends_on,omitempty"`
	Healthcheck *Healthcheck `json:"healthcheck,omitempty"`
	Resources   *Resources   `json:"resources,omitempty"`
	Labels      []Label      `json:"labels,omitempty"`
	Replicas    int          `json:"replicas"`
	Restart     string       `json:"restart,omitempty"`
	Wave        int          `json:"wave"`
	// Metrics is the per-service metric selection from the heimdall.metrics
	// label. Empty means all: opting out of observability should require a
	// choice, never be a default someone forgot to change.
	Metrics []string `json:"metrics,omitempty"`
	// Secrets are file-backed secrets the runtime materialises inside the
	// container, Swarm-style. The value comes from Ref at apply time and is
	// never in this document.
	Secrets []SecretMount `json:"secrets,omitempty"`
}

// SecretMount is one file secret. Name is the compose secret name and the
// default file name; Ref is the ${secret:...} reference the value resolves
// from at apply time. ContentHint is a digest of the *ciphertext* for
// in-repo (sops) references, stamped at refresh — it puts value rotation
// into the service's content hash, so a re-encrypted secret plans as an
// update instead of a noop. It is a hash of encrypted bytes: nothing
// secret enters the document.
type SecretMount struct {
	Name        string `json:"name"`
	Ref         string `json:"ref"`
	Target      string `json:"target,omitempty"`
	ContentHint string `json:"content_hint,omitempty"`
}

// MetricGroups is the closed vocabulary of the heimdall.metrics label. One
// group can cover several series — "network" is both directions — because
// the choice a developer makes is "do I care about the network", not which
// counter implements it.
var MetricGroups = map[string]bool{
	"cpu": true, "memory": true, "network": true, "block": true,
	"pids": true, "throttling": true, "net_errors": true,
}

// EnvVar carries either a literal value or a secret reference. A resolved
// secret value never reaches this type: render rewrites ${secret:...} into
// Ref, and only the apply path resolves it, in process.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
	Ref   string `json:"ref,omitempty"`
}

type Port struct {
	Published int    `json:"published"`
	Target    int    `json:"target"`
	Protocol  string `json:"protocol"`
}

type Volume struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

type Healthcheck struct {
	Test        []string `json:"test"`
	IntervalMS  int      `json:"interval_ms,omitempty"`
	TimeoutMS   int      `json:"timeout_ms,omitempty"`
	StartPerdMS int      `json:"start_period_ms,omitempty"`
	Retries     int      `json:"retries,omitempty"`
}

// Resources are absolute requests. Providers with discrete tiers snap upward
// and report the snap in the plan; they never snap down.
type Resources struct {
	CPUMillis int `json:"cpu_millis,omitempty"`
	MemoryMiB int `json:"memory_mib,omitempty"`
}

type Label struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Normalize sorts every collection into canonical order and fills defaults.
// It is idempotent, and Canonical calls it, so callers rarely need it
// directly.
func (d *DeploySpec) Normalize() {
	if d.SchemaVersion == "" {
		d.SchemaVersion = SchemaVersion
	}
	sort.Slice(d.Services, func(i, j int) bool { return d.Services[i].Name < d.Services[j].Name })
	for i := range d.Services {
		s := &d.Services[i]
		if s.Replicas == 0 {
			s.Replicas = 1
		}
		sort.Slice(s.Env, func(a, b int) bool { return s.Env[a].Key < s.Env[b].Key })
		sort.Slice(s.Labels, func(a, b int) bool { return s.Labels[a].Key < s.Labels[b].Key })
		sort.Slice(s.Ports, func(a, b int) bool {
			if s.Ports[a].Target != s.Ports[b].Target {
				return s.Ports[a].Target < s.Ports[b].Target
			}
			return s.Ports[a].Protocol < s.Ports[b].Protocol
		})
		sort.Slice(s.Volumes, func(a, b int) bool { return s.Volumes[a].Target < s.Volumes[b].Target })
		sort.Strings(s.DependsOn)
		sort.Strings(s.Metrics)
		sort.Slice(s.Secrets, func(a, b int) bool { return s.Secrets[a].Name < s.Secrets[b].Name })
		// Entrypoint and Command are argv: order is meaning, never sorted.
	}
}

// Canonical renders the spec to its one legal byte sequence. HTML escaping is
// off because it would rewrite bytes inside otherwise-valid image references
// and label values, making the hash depend on an encoder setting.
func Canonical(d DeploySpec) ([]byte, error) {
	d.Normalize()
	var b strings.Builder
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(d); err != nil {
		return nil, fmt.Errorf("canonicalize deployspec: %w", err)
	}
	return []byte(strings.TrimSuffix(b.String(), "\n")), nil
}

// Hash is the content address of a spec, and the identity of a revision.
// HashService content-addresses one service, so any adapter's plan can
// decide whether a running workload matches without inspecting it field by
// field. Every adapter must use this one function: two hashes of the same
// service must never differ across runtimes.
func HashService(service Service) (string, error) {
	return Hash(DeploySpec{
		SchemaVersion: SchemaVersion,
		App:           service.Name,
		Revision:      "service",
		Services:      []Service{service},
	})
}

func Hash(d DeploySpec) (string, error) {
	canonical, err := Canonical(d)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Service returns the named service, so callers do not re-scan the slice.
func (d DeploySpec) Service(name string) (Service, bool) {
	for _, s := range d.Services {
		if s.Name == name {
			return s, true
		}
	}
	return Service{}, false
}
