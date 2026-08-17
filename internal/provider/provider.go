// Package provider is the boundary between HEIMDALL and a runtime. Adapters
// under provider/<name>/ are the only code in the repository that knows a
// cloud exists; a CI gate fails the build if a cloud SDK is imported anywhere
// else.
//
// The interface is deliberately small. Everything a runtime must be able to
// answer is here, and nothing that only one runtime happens to support.
package provider

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/d31ma/heimdall/internal/spec"
)

// Provider is one runtime adapter.
type Provider interface {
	// Name is the adapter's stable identifier, used in target documents.
	Name() string

	// Plan turns a desired spec plus the live state into the ordered
	// operations that would close the gap. It must not mutate anything.
	Plan(ctx context.Context, target Target, want spec.DeploySpec) (Plan, error)
	// Apply executes a plan. It is called with a plan Plan produced, never a
	// hand-built one.
	Apply(ctx context.Context, target Target, plan Plan) (Result, error)

	// Observe reads live state back, for drift and health.
	Observe(ctx context.Context, target Target, app AppRef) (LiveState, error)

	// Instances, Metrics, Logs, and Events are the observability half — the
	// reason to use HEIMDALL over a shell script.
	Instances(ctx context.Context, target Target, app AppRef) ([]Instance, error)
	Metrics(ctx context.Context, target Target, instance InstanceRef, window Window) (Series, error)
	Logs(ctx context.Context, target Target, instance InstanceRef, filter LogFilter) (io.ReadCloser, error)
	Events(ctx context.Context, target Target, app AppRef) ([]Event, error)

	// Capabilities states what this runtime can and cannot express. It is the
	// load-bearing method: it drives plan-time rejection, and the published
	// capability matrix is generated from it so it cannot go stale.
	Capabilities() Capabilities
}

// Target names one deployment destination. Credentials are referenced, never
// carried: a Target is stored in FYLO and appears in plans.
type Target struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	// Project scopes the workload's identity — it is half of every label the
	// adapters stamp. Region is the cloud's own placement notion and only
	// cloud adapters read it.
	Project string `json:"project,omitempty"`
	Region  string `json:"region,omitempty"`
	// Endpoint is provider-specific: a Docker Engine socket or host, an ECS
	// cluster ARN, an ACA environment, a Cloud Run project.
	Endpoint string `json:"endpoint,omitempty"`
	// CredentialRef names a secret, never a value.
	CredentialRef string `json:"credential_ref,omitempty"`
	// AgentID names the heimdall agent that reaches this target, when the
	// control plane cannot reach it directly.
	AgentID string `json:"agent_id,omitempty"`
	// Config carries provider-specific settings — subnets, an execution
	// role, a log group. Never credentials.
	Config map[string]string `json:"config,omitempty"`
}

// AppRef identifies an application on a target.
type AppRef struct {
	Project string `json:"project"`
	App     string `json:"app"`
}

// InstanceRef identifies one running instance.
type InstanceRef struct {
	AppRef
	Service  string `json:"service"`
	Instance string `json:"instance"`
}

// OperationKind is the closed set of things a plan can do. Adapters map these
// onto their own APIs; the reconciler and the UI only ever see these.
type OperationKind string

const (
	OpCreate  OperationKind = "create"
	OpUpdate  OperationKind = "update"
	OpDelete  OperationKind = "delete"
	OpNoop    OperationKind = "noop"
	OpRestart OperationKind = "restart"
	OpScale   OperationKind = "scale"
)

// Operation is one step. Reason is shown to the operator, so it says what
// changed rather than restating the kind.
type Operation struct {
	Kind    OperationKind `json:"kind"`
	Service string        `json:"service"`
	Wave    int           `json:"wave"`
	Reason  string        `json:"reason,omitempty"`
	// Prune marks a deletion of something the desired state no longer
	// mentions. It is separated from OpDelete-by-replacement because pruning
	// requires an explicit opt-in on the application.
	Prune bool `json:"prune,omitempty"`
}

// Plan is the full intent of one sync, ordered by wave then service.
type Plan struct {
	Target     string      `json:"target"`
	App        AppRef      `json:"app"`
	Revision   string      `json:"revision"`
	SpecHash   string      `json:"spec_hash"`
	Operations []Operation `json:"operations"`
}

// Changes reports whether the plan would do anything. A plan of pure noops is
// how "Synced" is decided.
func (p Plan) Changes() bool {
	for _, operation := range p.Operations {
		if operation.Kind != OpNoop {
			return true
		}
	}
	return false
}

// Waves returns the distinct waves in ascending order. Each wave must settle
// before the next begins.
func (p Plan) Waves() []int {
	seen := map[int]bool{}
	var waves []int
	for _, operation := range p.Operations {
		if !seen[operation.Wave] {
			seen[operation.Wave] = true
			waves = append(waves, operation.Wave)
		}
	}
	sort.Ints(waves)
	return waves
}

// Result reports what an apply actually did, per service.
type Result struct {
	OpID     string            `json:"op_id"`
	Applied  []Operation       `json:"applied"`
	Failures map[string]string `json:"failures,omitempty"`
}

// Health is the rolled-up state of a service or an application.
type Health string

const (
	Healthy     Health = "Healthy"
	Progressing Health = "Progressing"
	Degraded    Health = "Degraded"
	Suspended   Health = "Suspended"
	Missing     Health = "Missing"
)

// LiveState is what the runtime currently reports.
type LiveState struct {
	App      AppRef                  `json:"app"`
	Target   string                  `json:"target"`
	ReadAt   time.Time               `json:"read_at"`
	Services map[string]ServiceState `json:"services"`
	// Revision is the revision the runtime says put this here, read back from
	// the labels the adapter stamped at apply time. Empty means the workload
	// was not deployed by HEIMDALL.
	Revision string `json:"revision,omitempty"`
	SpecHash string `json:"spec_hash,omitempty"`
}

// ServiceState is one service as the runtime sees it.
type ServiceState struct {
	Health   Health `json:"health"`
	Replicas int    `json:"replicas"`
	Ready    int    `json:"ready"`
	Image    string `json:"image"`
	Message  string `json:"message,omitempty"`
}

// Rollup reduces per-service health to one application health, worst wins.
// Missing outranks Degraded because a service that is not there at all is a
// bigger statement than one that is unhealthy.
func (l LiveState) Rollup() Health {
	if len(l.Services) == 0 {
		return Missing
	}
	rank := map[Health]int{Healthy: 0, Suspended: 1, Progressing: 2, Degraded: 3, Missing: 4}
	worst := Healthy
	for _, service := range l.Services {
		if rank[service.Health] > rank[worst] {
			worst = service.Health
		}
	}
	return worst
}

// Instance is one running unit: a container, a task, a revision instance.
type Instance struct {
	Ref       InstanceRef `json:"ref"`
	Image     string      `json:"image"`
	Status    string      `json:"status"`
	Health    Health      `json:"health"`
	StartedAt time.Time   `json:"started_at"`
	Restarts  int         `json:"restarts"`
	// Revision links the instance back to the commit that put it there. This
	// is the product's headline claim, so an adapter that cannot supply it is
	// incomplete.
	Revision string `json:"revision,omitempty"`
	Node     string `json:"node,omitempty"`
}

// Window bounds a metric query.
type Window struct {
	From time.Time
	To   time.Time
	Step time.Duration
}

// Sample is one point.
type Sample struct {
	At    time.Time `json:"at"`
	Value float64   `json:"value"`
}

// Series is the metric set for one instance over a window. Absent metrics are
// nil rather than zero-filled: "no data" and "zero" mean different things
// during an incident.
type Series struct {
	Ref         InstanceRef `json:"ref"`
	CPUPercent  []Sample    `json:"cpu_percent,omitempty"`
	MemoryBytes []Sample    `json:"memory_bytes,omitempty"`
	MemoryLimit uint64      `json:"memory_limit,omitempty"`
	NetRxBytes  []Sample    `json:"net_rx_bytes,omitempty"`
	NetTxBytes  []Sample    `json:"net_tx_bytes,omitempty"`
	BlockRead   []Sample    `json:"block_read_bytes,omitempty"`
	BlockWrite  []Sample    `json:"block_write_bytes,omitempty"`
	// Pids is a gauge; CPUThrottled and NetErrors are cumulative counters at
	// scrape time that the rollup pipeline converts to per-minute deltas.
	// NetErrors folds rx/tx errors and drops into one series — the panel's
	// question is "is the network eating packets", not which direction.
	Pids         []Sample `json:"pids,omitempty"`
	CPUThrottled []Sample `json:"cpu_throttled,omitempty"`
	NetErrors    []Sample `json:"net_errors,omitempty"`
}

// LogFilter bounds a log read. Follow turns it into a stream.
type LogFilter struct {
	Since  time.Time
	Tail   int
	Follow bool
}

// Event is a runtime or HEIMDALL event, in one shape across providers.
type Event struct {
	At      time.Time `json:"at"`
	Type    string    `json:"type"`
	Service string    `json:"service,omitempty"`
	Message string    `json:"message"`
	Source  string    `json:"source"`
}

// RegistryCredential is what a runtime needs to pull a private image. It
// exists only in memory, only during an apply, and is never returned by any
// route or written to any document.
type RegistryCredential struct {
	Server   string
	Username string
	Password string
}

// RegistryResolver supplies pull credentials for an image reference, or nil
// when the image is public. Every adapter takes one; each maps it onto its
// own mechanism — a Docker X-Registry-Auth header, an ECS repository
// credential, a Cloud Run image-pull service account.
//
// An error is a hard failure: pulling anonymously after a credential lookup
// failed would turn a misconfiguration into a confusing 404 from the
// registry.
type RegistryResolver func(ctx context.Context, image string) (*RegistryCredential, error)

// RegistryOf extracts the registry host from an image reference, applying
// Docker's own defaulting: a reference whose first path segment has no dot,
// no colon, and is not "localhost" belongs to Docker Hub.
//
// It lives here rather than in the store so the agent can use it without
// importing a FYLO dependency onto a customer host.
func RegistryOf(image string) string {
	first, _, found := strings.Cut(image, "/")
	if !found {
		return "docker.io"
	}
	if !strings.ContainsAny(first, ".:") && first != "localhost" {
		return "docker.io"
	}
	return NormalizeRegistry(first)
}

// NormalizeRegistry folds the several spellings of Docker Hub onto one, and
// strips a scheme an operator may have pasted.
func NormalizeRegistry(server string) string {
	server = strings.TrimPrefix(strings.TrimPrefix(server, "https://"), "http://")
	server = strings.TrimSuffix(server, "/")
	switch server {
	case "", "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com":
		return "docker.io"
	}
	return server
}

// RegistryMatches reports whether a credential's server serves an image.
func RegistryMatches(server, image string) bool {
	return RegistryOf(image) == NormalizeRegistry(server)
}

// Support is how well a provider expresses one compose feature. These are
// enum values rather than prose precisely so the published matrix is
// generated and cannot drift from the code.
type Support string

const (
	// Full means the feature works as Compose describes it.
	Full Support = "full"
	// Partial means it works with a documented caveat, named in Caveats.
	Partial Support = "partial"
	// Rejected means a plan using it fails, with the offending service named.
	Rejected Support = "rejected"
)

// Feature is one thing a compose file can ask for.
type Feature string

const (
	FeatureMultiService Feature = "multiple services per app"
	FeatureSidecars     Feature = "sidecars in one unit"
	FeaturePorts        Feature = "published ports"
	FeatureMultiPort    Feature = "more than one published port"
	FeatureDependsOn    Feature = "depends_on ordering"
	FeatureHealthcheck  Feature = "healthcheck"
	FeatureNamedVolume  Feature = "named volumes"
	FeatureBindMount    Feature = "bind mounts"
	FeatureReplicas     Feature = "deploy.replicas"
	FeatureResources    Feature = "deploy.resources"
	FeatureRestart      Feature = "restart policy"
	FeatureScaleToZero  Feature = "scale to zero"
	FeatureSecretRef    Feature = "secret references"
	FeatureFileSecret   Feature = "file secrets"
)

// Features is every feature in a stable order, so the generated matrix has
// stable rows.
var Features = []Feature{
	FeatureMultiService, FeatureSidecars, FeaturePorts, FeatureMultiPort,
	FeatureDependsOn, FeatureHealthcheck, FeatureNamedVolume, FeatureBindMount,
	FeatureReplicas, FeatureResources, FeatureRestart, FeatureScaleToZero,
	FeatureFileSecret,
	FeatureSecretRef,
}

// Capabilities is one adapter's answer for every feature.
type Capabilities struct {
	Provider string              `json:"provider"`
	Support  map[Feature]Support `json:"support"`
	Caveats  map[Feature]string  `json:"caveats,omitempty"`
	// ResourceTiers, when set, means the provider snaps resource requests to
	// discrete sizes. Plan reports the snap; it never snaps downward.
	ResourceTiers []Resource `json:"resource_tiers,omitempty"`
}

// Resource is one discrete size a provider offers.
type Resource struct {
	CPUMillis int `json:"cpu_millis"`
	MemoryMiB int `json:"memory_mib"`
}

// Of returns the support level, defaulting to Rejected. An adapter that
// forgets to answer for a feature rejects it, which is the safe default: a
// missing entry must never read as "supported".
func (c Capabilities) Of(feature Feature) Support {
	if support, ok := c.Support[feature]; ok {
		return support
	}
	return Rejected
}

// RejectionError names every unsupported thing in one spec, all at once. An
// operator fixing a compose file should learn about all four problems in one
// round trip, not one per attempt.
type RejectionError struct {
	Provider   string
	Rejections []Rejection
}

// Rejection is one unsupported directive, located.
type Rejection struct {
	Service string
	Feature Feature
	Detail  string
}

func (e *RejectionError) Error() string {
	lines := make([]string, 0, len(e.Rejections)+1)
	lines = append(lines, fmt.Sprintf(
		"HD0300: the %s provider cannot express %d thing(s) in this compose file",
		e.Provider, len(e.Rejections)))
	for _, rejection := range e.Rejections {
		lines = append(lines, fmt.Sprintf("  service %q: %s — %s",
			rejection.Service, rejection.Feature, rejection.Detail))
	}
	return strings.Join(lines, "\n")
}

// Validate checks a spec against a provider's capabilities and returns every
// rejection at once. It runs at plan time, before anything is applied, which
// is the difference between a clear message and a half-applied deploy.
func Validate(capabilities Capabilities, want spec.DeploySpec) error {
	var rejections []Rejection

	reject := func(service string, feature Feature, detail string) {
		if capabilities.Of(feature) == Rejected {
			caveat := capabilities.Caveats[feature]
			if caveat != "" {
				detail = detail + "; " + caveat
			}
			rejections = append(rejections, Rejection{Service: service, Feature: feature, Detail: detail})
		}
	}

	if len(want.Services) > 1 {
		reject(want.Services[0].Name, FeatureMultiService,
			fmt.Sprintf("this application declares %d services", len(want.Services)))
	}

	for _, service := range want.Services {
		if len(service.Secrets) > 0 {
			reject(service.Name, FeatureFileSecret,
				fmt.Sprintf("mounts %d file secrets", len(service.Secrets)))
		}
		if len(service.Ports) > 0 {
			reject(service.Name, FeaturePorts, "publishes a port")
		}
		if len(service.Ports) > 1 {
			reject(service.Name, FeatureMultiPort,
				fmt.Sprintf("publishes %d ports", len(service.Ports)))
		}
		if len(service.DependsOn) > 0 {
			reject(service.Name, FeatureDependsOn,
				"depends on "+strings.Join(service.DependsOn, ", "))
		}
		if service.Healthcheck != nil {
			reject(service.Name, FeatureHealthcheck, "declares a healthcheck")
		}
		for _, volume := range service.Volumes {
			if strings.HasPrefix(volume.Source, "/") || strings.HasPrefix(volume.Source, ".") {
				reject(service.Name, FeatureBindMount, "bind-mounts "+volume.Source)
				continue
			}
			reject(service.Name, FeatureNamedVolume, "mounts named volume "+volume.Source)
		}
		if service.Replicas > 1 {
			reject(service.Name, FeatureReplicas,
				fmt.Sprintf("asks for %d replicas", service.Replicas))
		}
		if service.Replicas == 0 {
			reject(service.Name, FeatureScaleToZero, "asks to scale to zero")
		}
		if service.Resources != nil {
			reject(service.Name, FeatureResources, "sets resource limits")
		}
		if service.Restart != "" {
			reject(service.Name, FeatureRestart, "sets restart: "+service.Restart)
		}
		for _, env := range service.Env {
			if env.Ref != "" {
				reject(service.Name, FeatureSecretRef, "reads secret "+env.Ref)
				break
			}
		}
	}

	if len(rejections) == 0 {
		return nil
	}
	sort.Slice(rejections, func(i, j int) bool {
		if rejections[i].Service != rejections[j].Service {
			return rejections[i].Service < rejections[j].Service
		}
		return rejections[i].Feature < rejections[j].Feature
	})
	return &RejectionError{Provider: capabilities.Provider, Rejections: rejections}
}

// SnapResources raises a request to the smallest tier that satisfies it. It
// never snaps downward: silently giving a service less memory than it asked
// for is how an OOM at 3am starts.
func SnapResources(tiers []Resource, want spec.Resources) (Resource, bool) {
	if len(tiers) == 0 {
		return Resource{CPUMillis: want.CPUMillis, MemoryMiB: want.MemoryMiB}, false
	}
	ordered := append([]Resource{}, tiers...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].CPUMillis != ordered[j].CPUMillis {
			return ordered[i].CPUMillis < ordered[j].CPUMillis
		}
		return ordered[i].MemoryMiB < ordered[j].MemoryMiB
	})
	for _, tier := range ordered {
		if tier.CPUMillis >= want.CPUMillis && tier.MemoryMiB >= want.MemoryMiB {
			return tier, tier.CPUMillis != want.CPUMillis || tier.MemoryMiB != want.MemoryMiB
		}
	}
	return ordered[len(ordered)-1], true
}
