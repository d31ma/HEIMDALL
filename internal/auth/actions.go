package auth

import (
	"fmt"
	"strings"

	"github.com/d31ma/sesame/clients/go/sesame"
)

// Action is the verb half of an authorization question. The vocabulary is
// closed: a route names one of these constants, never a string it derived
// from its own path. Adding a verb is a deliberate edit here plus a role
// bundle decision, which is the point.
type Action string

const (
	AppRead     Action = "app:read"
	AppCreate   Action = "app:create"
	AppUpdate   Action = "app:update"
	AppDelete   Action = "app:delete"
	AppSync     Action = "app:sync"
	AppRollback Action = "app:rollback"
	AppPrune    Action = "app:prune"
	AppSuspend  Action = "app:suspend"

	TargetRead   Action = "target:read"
	TargetCreate Action = "target:create"
	TargetUpdate Action = "target:update"
	TargetDelete Action = "target:delete"

	RepoRead   Action = "repo:read"
	RepoCreate Action = "repo:create"
	RepoUpdate Action = "repo:update"
	RepoDelete Action = "repo:delete"

	ProjectRead   Action = "project:read"
	ProjectCreate Action = "project:create"
	ProjectUpdate Action = "project:update"
	ProjectDelete Action = "project:delete"
	ProjectGrant  Action = "project:grant"

	// SecretBind attaches a reference. There is deliberately no secret:read —
	// no route in HEIMDALL returns a secret value, so no verb permits it.
	SecretBind Action = "secret:bind"

	ObserveMetrics Action = "observe:metrics"
	// ObserveLogs is separate from AppRead on purpose: container logs
	// routinely contain data an operator who may deploy still should not see.
	ObserveLogs   Action = "observe:logs"
	ObserveEvents Action = "observe:events"

	AuditRead   Action = "audit:read"
	AuditExport Action = "audit:export"

	// The registry verbs guard ADR 0010's surface. Bind is the high one:
	// whoever binds the root repository decides what the registry declares.
	RegistryRead Action = "registry:read"
	RegistryBind Action = "registry:bind"
	RegistrySync Action = "registry:sync"
)

// Actions is every action HEIMDALL will ever ask SESAME about, in a stable
// order. Tests assert the route table only names members of this set.
var Actions = []Action{
	AppRead, AppCreate, AppUpdate, AppDelete, AppSync, AppRollback, AppPrune, AppSuspend,
	TargetRead, TargetCreate, TargetUpdate, TargetDelete,
	RepoRead, RepoCreate, RepoUpdate, RepoDelete,
	ProjectRead, ProjectCreate, ProjectUpdate, ProjectDelete, ProjectGrant,
	SecretBind,
	ObserveMetrics, ObserveLogs, ObserveEvents,
	AuditRead, AuditExport,
	RegistryRead, RegistryBind, RegistrySync,
}

var known = func() map[Action]bool {
	set := make(map[Action]bool, len(Actions))
	for _, a := range Actions {
		set[a] = true
	}
	return set
}()

// Valid reports whether a is in the closed vocabulary.
func (a Action) Valid() bool { return known[a] }

func (a Action) String() string { return string(a) }

// Resource builds a hierarchical resource string from ordered kind/id pairs,
// for example Resource("project", "alpha", "app", "checkout") =>
// "project:alpha:app:checkout".
//
// Colon is the only separator SESAME's pattern grammar accepts, and its
// wildcard is a trailing segment, so "project:alpha:*" is what covers every
// app in a project. Ordering is therefore a security decision: coarse to
// fine, always, or a prefix grant reaches further than intended.
//
// Identifiers are lowercased, because SESAME's alphabet has no uppercase and
// TTIDs are emitted in uppercase. That is lossless rather than lossy: TTID
// specifies identifiers as case-insensitive and canonically uppercase, so two
// TTIDs cannot differ by case alone. Every other identifier HEIMDALL puts in
// a resource — a project or application name — is lowercase by validation
// already.
func Resource(pairs ...string) (string, error) {
	if len(pairs) == 0 || len(pairs)%2 != 0 {
		return "", fmt.Errorf("resource needs kind/id pairs, got %d parts", len(pairs))
	}
	segments := make([]string, 0, len(pairs))
	for i := 0; i < len(pairs); i += 2 {
		kind, id := pairs[i], strings.ToLower(pairs[i+1])
		if err := validSegment(kind); err != nil {
			return "", fmt.Errorf("resource kind %q: %w", kind, err)
		}
		if err := validSegment(id); err != nil {
			return "", fmt.Errorf("resource id %q: %w", pairs[i+1], err)
		}
		segments = append(segments, kind, id)
	}
	return strings.Join(segments, ":"), nil
}

// validSegment mirrors SESAME's pattern alphabet. Rejecting here rather than
// letting the engine reject keeps a malformed identifier from ever becoming
// an authorization question, so it can never be the reason a decision was
// skipped.
func validSegment(segment string) error {
	if segment == "" {
		return fmt.Errorf("is empty")
	}
	for _, r := range segment {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("contains unsupported character %q", r)
		}
	}
	return nil
}

// RoleBundles are the four shipped roles. They are grant bundles over the
// action list above and nothing in HEIMDALL branches on a role name — the
// names exist so an administrator has something to grant, not so code can
// test for them.
//
// Order matters for seeding: it is deterministic so a re-run of heimdall init
// produces the same roles.
var RoleBundles = []struct {
	Name    string
	Actions []Action
}{
	{"viewer", []Action{
		AppRead, TargetRead, RepoRead, ProjectRead, ObserveMetrics, ObserveEvents,
	}},
	{"operator", []Action{
		AppRead, AppSync, AppRollback, AppSuspend,
		TargetRead, RepoRead, ProjectRead,
		ObserveMetrics, ObserveLogs, ObserveEvents,
	}},
	{"admin", []Action{
		AppRead, AppCreate, AppUpdate, AppDelete, AppSync, AppRollback, AppPrune, AppSuspend,
		TargetRead, TargetCreate, TargetUpdate, TargetDelete,
		RepoRead, RepoCreate, RepoUpdate, RepoDelete,
		ProjectRead, SecretBind,
		ObserveMetrics, ObserveLogs, ObserveEvents,
		AuditRead,
		RegistryRead, RegistrySync,
	}},
	{"owner", Actions},
}

// permissions renders a bundle as SESAME permissions over the whole tenant.
// A narrower grant is made by an administrator against a subtree resource;
// the shipped bundles are deliberately tenant-wide starting points.
func permissions(actions []Action) []sesame.Permission {
	out := make([]sesame.Permission, 0, len(actions))
	for _, a := range actions {
		out = append(out, sesame.Permission{Action: a.String(), Resource: "*"})
	}
	return out
}
