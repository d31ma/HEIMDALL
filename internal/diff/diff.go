// Package diff answers the two questions an operator opens the tool to ask:
// what is different, and is this application in sync.
//
// The field-level diff compares two DeploySpecs — the desired revision
// against the revision that is actually deployed, both read from the
// immutable revision store. Live state contributes presence and health, which
// a spec cannot know.
//
// Nothing here can print a secret, because nothing here is ever given one: a
// secret is a reference in both specs, and the reference is what is shown.
package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

// SyncStatus is agreement between desired and live.
type SyncStatus string

const (
	Synced    SyncStatus = "Synced"
	OutOfSync SyncStatus = "OutOfSync"
	// Unknown means live state could not be read. It is deliberately distinct
	// from Synced: "we could not look" must never render as "all is well".
	Unknown SyncStatus = "Unknown"
)

// ChangeKind is what happened to a field.
type ChangeKind string

const (
	Added    ChangeKind = "added"
	Removed  ChangeKind = "removed"
	Modified ChangeKind = "modified"
)

// Change is one field-level difference. Desired and Live are rendered
// strings, not raw values, because this is display output.
type Change struct {
	Field   string     `json:"field"`
	Kind    ChangeKind `json:"kind"`
	Desired string     `json:"desired,omitempty"`
	Live    string     `json:"live,omitempty"`
	// Secret marks a field whose value is a reference to a secret. The
	// reference is shown; there is no value to show, and there never will be.
	Secret bool `json:"secret,omitempty"`
}

// ServiceDiff is every difference for one service.
type ServiceDiff struct {
	Service string `json:"service"`
	// Kind is the service-level verdict: added means the desired state
	// declares it and the deployed revision does not.
	Kind    ChangeKind      `json:"kind,omitempty"`
	Changes []Change        `json:"changes,omitempty"`
	Health  provider.Health `json:"health,omitempty"`
	Message string          `json:"message,omitempty"`
}

// Summary is the whole answer for one application.
type Summary struct {
	App        provider.AppRef `json:"app"`
	Target     string          `json:"target"`
	Desired    string          `json:"desired_revision"`
	Live       string          `json:"live_revision,omitempty"`
	SyncStatus SyncStatus      `json:"sync_status"`
	Health     provider.Health `json:"health"`
	Services   []ServiceDiff   `json:"services"`
}

// Specs compares two rendered specs field by field. Both are normalized
// first, so the comparison is between canonical forms and cannot report
// ordering as a change.
func Specs(desired, deployed spec.DeploySpec) []ServiceDiff {
	desired.Normalize()
	deployed.Normalize()

	names := map[string]bool{}
	for _, service := range desired.Services {
		names[service.Name] = true
	}
	for _, service := range deployed.Services {
		names[service.Name] = true
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	diffs := make([]ServiceDiff, 0, len(ordered))
	for _, name := range ordered {
		want, inDesired := desired.Service(name)
		have, inDeployed := deployed.Service(name)

		switch {
		case inDesired && !inDeployed:
			diffs = append(diffs, ServiceDiff{Service: name, Kind: Added})
		case !inDesired && inDeployed:
			diffs = append(diffs, ServiceDiff{Service: name, Kind: Removed})
		default:
			if changes := compareService(want, have); len(changes) > 0 {
				diffs = append(diffs, ServiceDiff{Service: name, Kind: Modified, Changes: changes})
			}
		}
	}
	return diffs
}

func compareService(want, have spec.Service) []Change {
	var changes []Change

	scalar := func(field, desired, live string) {
		if desired != live {
			changes = append(changes, Change{Field: field, Kind: Modified, Desired: desired, Live: live})
		}
	}
	scalar("image", want.Image, have.Image)
	scalar("restart", want.Restart, have.Restart)
	scalar("command", strings.Join(want.Command, " "), strings.Join(have.Command, " "))
	scalar("entrypoint", strings.Join(want.Entrypoint, " "), strings.Join(have.Entrypoint, " "))
	if want.Replicas != have.Replicas {
		scalar("replicas", fmt.Sprint(want.Replicas), fmt.Sprint(have.Replicas))
	}
	if want.Wave != have.Wave {
		scalar("wave", fmt.Sprint(want.Wave), fmt.Sprint(have.Wave))
	}

	changes = append(changes, compareEnv(want.Env, have.Env)...)
	changes = append(changes, compareKeyed("ports", renderPorts(want.Ports), renderPorts(have.Ports))...)
	changes = append(changes, compareKeyed("volumes", renderVolumes(want.Volumes), renderVolumes(have.Volumes))...)
	changes = append(changes, compareKeyed("labels", renderLabels(want.Labels), renderLabels(have.Labels))...)
	changes = append(changes, compareKeyed("depends_on", renderList(want.DependsOn), renderList(have.DependsOn))...)

	scalar("healthcheck", renderHealthcheck(want.Healthcheck), renderHealthcheck(have.Healthcheck))
	scalar("resources", renderResources(want.Resources), renderResources(have.Resources))

	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Field < changes[j].Field })
	return changes
}

// compareEnv treats a literal and a reference as different kinds of thing. A
// variable that changed from a literal to a secret reference is a meaningful
// change and must be visible, but the diff still only ever prints the
// reference.
func compareEnv(want, have []spec.EnvVar) []Change {
	desired := map[string]spec.EnvVar{}
	for _, env := range want {
		desired[env.Key] = env
	}
	live := map[string]spec.EnvVar{}
	for _, env := range have {
		live[env.Key] = env
	}

	keys := map[string]bool{}
	for key := range desired {
		keys[key] = true
	}
	for key := range live {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)

	var changes []Change
	for _, key := range ordered {
		desiredEnv, inDesired := desired[key]
		liveEnv, inLive := live[key]
		field := "env." + key

		switch {
		case inDesired && !inLive:
			changes = append(changes, Change{
				Field: field, Kind: Added,
				Desired: renderEnvValue(desiredEnv), Secret: desiredEnv.Ref != "",
			})
		case !inDesired && inLive:
			changes = append(changes, Change{
				Field: field, Kind: Removed,
				Live: renderEnvValue(liveEnv), Secret: liveEnv.Ref != "",
			})
		case desiredEnv != liveEnv:
			changes = append(changes, Change{
				Field: field, Kind: Modified,
				Desired: renderEnvValue(desiredEnv), Live: renderEnvValue(liveEnv),
				Secret: desiredEnv.Ref != "" || liveEnv.Ref != "",
			})
		}
	}
	return changes
}

// renderEnvValue prints a reference as a reference. There is no branch that
// prints a resolved secret, because render never produced one.
func renderEnvValue(env spec.EnvVar) string {
	if env.Ref != "" {
		return "${secret:" + env.Ref + "}"
	}
	return env.Value
}

// compareKeyed diffs two rendered string sets as one field, so a list change
// reads as one entry rather than a positional shuffle.
func compareKeyed(field string, want, have []string) []Change {
	desired := strings.Join(want, ", ")
	live := strings.Join(have, ", ")
	if desired == live {
		return nil
	}
	switch {
	case live == "":
		return []Change{{Field: field, Kind: Added, Desired: desired}}
	case desired == "":
		return []Change{{Field: field, Kind: Removed, Live: live}}
	default:
		return []Change{{Field: field, Kind: Modified, Desired: desired, Live: live}}
	}
}

func renderPorts(ports []spec.Port) []string {
	out := make([]string, 0, len(ports))
	for _, port := range ports {
		out = append(out, fmt.Sprintf("%d:%d/%s", port.Published, port.Target, port.Protocol))
	}
	return out
}

func renderVolumes(volumes []spec.Volume) []string {
	out := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		entry := volume.Source + ":" + volume.Target
		if volume.ReadOnly {
			entry += ":ro"
		}
		out = append(out, entry)
	}
	return out
}

func renderLabels(labels []spec.Label) []string {
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		out = append(out, label.Key+"="+label.Value)
	}
	return out
}

func renderList(values []string) []string { return values }

func renderHealthcheck(check *spec.Healthcheck) string {
	if check == nil {
		return ""
	}
	return fmt.Sprintf("%s every %dms, timeout %dms, retries %d",
		strings.Join(check.Test, " "), check.IntervalMS, check.TimeoutMS, check.Retries)
}

func renderResources(resources *spec.Resources) string {
	if resources == nil {
		return ""
	}
	return fmt.Sprintf("%dm cpu, %dMiB memory", resources.CPUMillis, resources.MemoryMiB)
}

// Report combines a plan and live state into the answer the UI renders.
//
// Sync status comes from the plan, not from the spec comparison: the plan is
// what the provider says it would have to do, which is the only definition of
// "in sync" that survives a provider expressing something differently than
// the spec wrote it.
func Report(
	app provider.AppRef,
	target string,
	desiredRevision string,
	plan provider.Plan,
	live provider.LiveState,
	liveReadFailed bool,
	services []ServiceDiff,
) Summary {
	summary := Summary{
		App:      app,
		Target:   target,
		Desired:  desiredRevision,
		Live:     live.Revision,
		Services: services,
		Health:   live.Rollup(),
	}

	switch {
	case liveReadFailed:
		summary.SyncStatus = Unknown
		summary.Health = provider.Missing
	case plan.Changes():
		summary.SyncStatus = OutOfSync
	default:
		summary.SyncStatus = Synced
	}

	// Fold per-service health from live state onto the diff entries, so one
	// row carries both "what changed" and "how is it doing".
	byName := map[string]int{}
	for i, service := range summary.Services {
		byName[service.Service] = i
	}
	for name, state := range live.Services {
		index, ok := byName[name]
		if !ok {
			summary.Services = append(summary.Services, ServiceDiff{
				Service: name, Health: state.Health, Message: state.Message,
			})
			byName[name] = len(summary.Services) - 1
			continue
		}
		summary.Services[index].Health = state.Health
		summary.Services[index].Message = state.Message
	}

	// A service the desired state declares but the runtime has no trace of is
	// Missing. Observe cannot report this on its own — it can only describe
	// what exists — so the plan is the evidence: a create operation for a
	// service means nothing is running it.
	for _, operation := range plan.Operations {
		if operation.Kind != provider.OpCreate || operation.Prune {
			continue
		}
		if _, running := live.Services[operation.Service]; running {
			continue
		}
		if index, ok := byName[operation.Service]; ok {
			summary.Services[index].Health = provider.Missing
			if summary.Services[index].Message == "" {
				summary.Services[index].Message = operation.Reason
			}
			continue
		}
		summary.Services = append(summary.Services, ServiceDiff{
			Service: operation.Service, Kind: Added,
			Health: provider.Missing, Message: operation.Reason,
		})
		byName[operation.Service] = len(summary.Services) - 1
	}

	// Re-roll the application health over the merged set, so a missing
	// service drags the application down rather than being invisible.
	if !liveReadFailed {
		summary.Health = rollup(summary.Services)
	}

	sort.Slice(summary.Services, func(i, j int) bool {
		return summary.Services[i].Service < summary.Services[j].Service
	})
	return summary
}

// rollup reduces per-service health to one answer, worst wins. It mirrors
// provider.LiveState.Rollup but runs over the merged view, which is the only
// one that knows about services that are not there at all.
func rollup(services []ServiceDiff) provider.Health {
	rank := map[provider.Health]int{
		provider.Healthy: 0, provider.Suspended: 1, provider.Progressing: 2,
		provider.Degraded: 3, provider.Missing: 4,
	}
	worst := provider.Healthy
	seen := false
	for _, service := range services {
		if service.Health == "" {
			continue
		}
		seen = true
		if rank[service.Health] > rank[worst] {
			worst = service.Health
		}
	}
	if !seen {
		return provider.Missing
	}
	return worst
}
