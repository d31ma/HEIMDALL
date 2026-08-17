package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/d31ma/heimdall/internal/diff"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

// Project is the RBAC scope. HEIMDALL is self-hosted, so a project is a
// permission boundary rather than a hard isolation boundary — the distinction
// is deliberate and documented, not an oversight.
type Project struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// ManagedBy is "registry" when ADR 0010's root repository declares this
	// document. The mutating API routes refuse to edit a managed document;
	// the change is a commit to the root repository, like every other change
	// to truth.
	ManagedBy string    `json:"managed_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Repository is a git remote. It carries a credential *reference*; the raw
// credential never enters a document.
type Repository struct {
	ID            string `json:"id,omitempty"`
	Project       string `json:"project"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	DefaultRef    string `json:"default_ref,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
	// RequireSignature refuses to deploy a commit without a good signature.
	RequireSignature bool `json:"require_signature,omitempty"`
	// WebhookSecretRef names the shared secret a forge signs its webhook
	// bodies with. A reference, like every other credential — the value is
	// resolved for the length of one comparison and never stored.
	WebhookSecretRef string `json:"webhook_secret_ref,omitempty"`
	// ManagedBy is "registry" when ADR 0010's root repository declares this
	// document. The mutating API routes refuse to edit a managed document;
	// the change is a commit to the root repository, like every other change
	// to truth.
	ManagedBy string    `json:"managed_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ManagedByRegistry marks a document ADR 0010's registry loop owns.
const ManagedByRegistry = "registry"

// RootBinding is ADR 0010's one interactive act: it names the repository
// whose manifest declares the registry. At most one exists. Everything it
// carries about credentials is a reference, like every other document.
type RootBinding struct {
	ID   string `json:"id,omitempty"`
	URL  string `json:"url"`
	Ref  string `json:"ref,omitempty"`
	Path string `json:"path,omitempty"`
	// CredentialRef and RequireSignature mean what they mean on Repository.
	CredentialRef    string `json:"credential_ref,omitempty"`
	RequireSignature bool   `json:"require_signature,omitempty"`
	// Prune permits the registry sync to delete managed documents the
	// manifest no longer declares. Off by default, exactly like workload
	// pruning: deletion is never a surprise.
	Prune   bool      `json:"prune,omitempty"`
	BoundBy string    `json:"bound_by,omitempty"`
	BoundAt time.Time `json:"bound_at"`
}

// Target is a deployment destination.
type Target struct {
	ID            string `json:"id,omitempty"`
	Project       string `json:"project"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Region        string `json:"region,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	// Tags are what groups select on. A project scopes authorization; tags
	// organise a fleet. Keeping them separate is deliberate — overloading one
	// onto the other means a fleet reorganisation silently changes who may
	// deploy where.
	Tags map[string]string `json:"tags,omitempty"`
	// Config carries provider-specific settings a cloud adapter needs —
	// subnets, an execution role, a log group. Opaque to everything outside
	// the adapter, and never a place for credentials: those stay in
	// CredentialRef as a reference.
	Config map[string]string `json:"config,omitempty"`
	// ManagedBy is "registry" when ADR 0010's root repository declares this
	// document. The mutating API routes refuse to edit a managed document;
	// the change is a commit to the root repository, like every other change
	// to truth.
	ManagedBy string    `json:"managed_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// TargetGroup is a named selector over target tags. Membership is derived on
// every read, never stored, so retagging a host moves it between groups with
// no edit to either group.
type TargetGroup struct {
	ID      string `json:"id,omitempty"`
	Project string `json:"project"`
	Name    string `json:"name"`
	// Selector matches a target when every pair here is present and equal on
	// the target's tags. An empty selector matches nothing rather than
	// everything: a group that accidentally covers the whole fleet is a worse
	// failure than one that covers none of it.
	//
	// ponytail: equality only, no expression language. Add operators when a
	// real group needs "region in (eu-west-1, eu-west-2)".
	Selector  map[string]string `json:"selector"`
	CreatedAt time.Time         `json:"created_at"`
}

// Matches reports whether a target belongs to this group.
func (g TargetGroup) Matches(target Target) bool {
	if len(g.Selector) == 0 {
		return false
	}
	for key, want := range g.Selector {
		if target.Tags[key] != want {
			return false
		}
	}
	return true
}

// Ref converts a stored target into the provider-facing value.
func (t Target) Ref() provider.Target {
	return provider.Target{
		ID: t.ID, Provider: t.Provider, Project: t.Project, Region: t.Region,
		Endpoint: t.Endpoint, CredentialRef: t.CredentialRef, AgentID: t.AgentID,
		Config: t.Config,
	}
}

// SyncPolicy is what happens without a human. Every field defaults to the
// conservative answer, so an application created with no policy does nothing
// on its own.
type SyncPolicy struct {
	Automated bool `json:"automated,omitempty"`
	SelfHeal  bool `json:"self_heal,omitempty"`
	// Prune deletes resources the desired state no longer declares. Opt-in
	// per application: the failure mode is deleting something real.
	Prune bool `json:"prune,omitempty"`
	// MaxParallel bounds a fan-out rollout. Zero means 4: fast enough for a
	// fleet, small enough that a bad image does not reach fifty hosts before
	// the first failure reports.
	MaxParallel int `json:"max_parallel,omitempty"`
	// FailureThreshold halts a fan-out after this many target failures.
	// Zero means 1 — the first failure stops the wave.
	FailureThreshold int `json:"failure_threshold,omitempty"`
}

// Application is the unit an operator manages — the ArgoCD `Application`
// analogue.
type Application struct {
	ID      string `json:"id,omitempty"`
	Project string `json:"project"`
	Name    string `json:"name"`
	RepoID  string `json:"repo_id"`
	// Path is the directory in the repository holding compose.yaml.
	Path string `json:"path"`
	// Overlays are additional file names inside Path, merged in order after
	// the base compose file.
	Overlays []string `json:"overlays,omitempty"`
	// Ref is the branch, tag, or commit to deploy. Empty uses the
	// repository's default.
	Ref      string `json:"ref,omitempty"`
	TargetID string `json:"target_id"`
	// Variables satisfy ${VAR} interpolation. Secrets belong in
	// ${secret:...} references, never here — a value in this map is stored in
	// plain text, and a CI gate checks fixtures for credential-shaped strings.
	Variables map[string]string `json:"variables,omitempty"`
	// GroupID deploys this application to every target the group selects
	// instead of one TargetID — the fleet case. Exactly one of the two is
	// set.
	GroupID    string     `json:"group_id,omitempty"`
	SyncPolicy SyncPolicy `json:"sync_policy"`
	Suspended  bool       `json:"suspended,omitempty"`
	// ManagedBy is "registry" when ADR 0010's root repository declares this
	// document. The mutating API routes refuse to edit a managed document;
	// the change is a commit to the root repository, like every other change
	// to truth.
	ManagedBy string    `json:"managed_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// AppRef is the provider-facing identity of this application. It is not
// named Ref because Application already has a Ref field naming the git ref,
// and two different meanings of "ref" one line apart is how a bug hides.
func (a Application) AppRef() provider.AppRef {
	return provider.AppRef{Project: a.Project, App: a.Name}
}

// ComposeFiles is the ordered list of file names to render, base first.
func (a Application) ComposeFiles() []string {
	base := "compose.yaml"
	files := append([]string{base}, a.Overlays...)
	return files
}

// Revision is a commit rendered to a DeploySpec. It is immutable and it is
// the audit spine: a rollback re-applies one of these rather than rewriting
// git.
type Revision struct {
	ID       string          `json:"id,omitempty"`
	AppID    string          `json:"app_id"`
	Commit   string          `json:"commit"`
	Ref      string          `json:"ref,omitempty"`
	SpecHash string          `json:"spec_hash"`
	Spec     spec.DeploySpec `json:"spec"`
	Author   string          `json:"author,omitempty"`
	Message  string          `json:"message,omitempty"`
	// Signed records whether signature verification passed, when the
	// repository required it.
	Signed     bool      `json:"signed,omitempty"`
	RenderedAt time.Time `json:"rendered_at"`
}

// OperationPhase is where a sync has got to.
type OperationPhase string

const (
	PhasePending   OperationPhase = "pending"
	PhasePlanning  OperationPhase = "planning"
	PhaseApplying  OperationPhase = "applying"
	PhaseSucceeded OperationPhase = "succeeded"
	PhaseFailed    OperationPhase = "failed"
	// PhaseSuperseded closes a parked operation that a newer sync replaced,
	// or that was drained as a fresh operation on reconnect. It points at its
	// replacement in Message; it is neither success nor failure, because the
	// work was neither done nor refused — it was overtaken.
	PhaseSuperseded OperationPhase = "superseded"
)

// Operation is one sync, whole, in one document.
//
// This is the shape FYLO's lack of cross-collection transactions forces, and
// it is a better design for it: every write during a sync patches this single
// document, so a crash leaves one record in a known state rather than four
// collections disagreeing. hd-livestate and hd-events are folded from these.
type Operation struct {
	ID       string `json:"id,omitempty"`
	AppID    string `json:"app_id"`
	Project  string `json:"project"`
	App      string `json:"app"`
	TargetID string `json:"target_id"`

	Phase      OperationPhase `json:"phase"`
	Revision   string         `json:"revision"`
	RevisionID string         `json:"revision_id,omitempty"`
	SpecHash   string         `json:"spec_hash,omitempty"`
	// PreviousRevision is what was deployed before, so a failed sync can be
	// reasoned about and a rollback target is always at hand.
	PreviousRevision string `json:"previous_revision,omitempty"`

	// DryRun means the plan was produced and nothing was applied.
	DryRun bool `json:"dry_run,omitempty"`
	// Rollback marks a sync that re-applied a stored revision rather than the
	// repository's current head.
	Rollback bool `json:"rollback,omitempty"`

	Operations []provider.Operation `json:"operations,omitempty"`
	Applied    []provider.Operation `json:"applied,omitempty"`
	Failures   map[string]string    `json:"failures,omitempty"`
	Message    string               `json:"message,omitempty"`

	// Who and under what policy. Both come from SESAME and are never
	// inferred here.
	PrincipalID   string `json:"principal_id"`
	PolicyVersion int64  `json:"policy_version"`
	ReasonCode    string `json:"reason_code,omitempty"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// Succeeded reports whether the sync finished cleanly.
func (o Operation) Succeeded() bool { return o.Phase == PhaseSucceeded }

// LiveStateDoc is a projection of the most recent Observe for one
// application. It is a cache: dropping the collection loses nothing that
// cannot be read back from the runtime.
type LiveStateDoc struct {
	ID      string             `json:"id,omitempty"`
	AppID   string             `json:"app_id"`
	Summary diff.Summary       `json:"summary"`
	State   provider.LiveState `json:"state"`
	ReadAt  time.Time          `json:"read_at"`
}

// AuditRecord is one mutating API call. Append-only: nothing updates or
// deletes one, and unlike hd-livestate it is authoritative rather than a
// projection, because an audit log you can regenerate is one you can rewrite.
type AuditRecord struct {
	ID            string    `json:"id,omitempty"`
	At            time.Time `json:"at"`
	PrincipalID   string    `json:"principal_id"`
	Action        string    `json:"action"`
	Resource      string    `json:"resource"`
	Outcome       string    `json:"outcome"`
	ReasonCode    string    `json:"reason_code,omitempty"`
	PolicyVersion int64     `json:"policy_version"`
	DecisionID    string    `json:"decision_id,omitempty"`
	Method        string    `json:"method"`
	Path          string    `json:"path"`
	// Previous is the id of the record before this one, making the log
	// hash-chainable and gaps detectable.
	Previous string `json:"previous,omitempty"`
}

// ---------------------------------------------------------------------------
// Typed access
// ---------------------------------------------------------------------------

// Collection is a typed view over one FYLO collection. It exists because the
// alternative is the same four marshal/unmarshal methods written six times,
// each with its own opportunity to get the id handling subtly wrong.
type Collection[T any] struct {
	store *Store
	name  string
}

// In returns a typed view. The type parameter is the document, the argument
// is the collection name from the constants above.
func In[T any](store *Store, name string) Collection[T] {
	return Collection[T]{store: store, name: name}
}

// Put writes a document and returns its id.
func (c Collection[T]) Put(document T) (string, error) {
	encoded, err := toMap(document)
	if err != nil {
		return "", err
	}
	// FYLO assigns the id, so an id field carried in from a caller would only
	// be a second, divergent source of truth.
	delete(encoded, "id")

	result, err := c.store.db.PutData(c.name, encoded)
	if err != nil {
		return "", fmt.Errorf("HD0040: write %s: %w", c.name, err)
	}
	id, ok := result.(string)
	if !ok {
		return "", fmt.Errorf("HD0040: %s returned id of type %T", c.name, result)
	}
	return id, nil
}

// Patch merges changes into an existing document. This is how an operation's
// state machine advances: one atomic write to one document.
func (c Collection[T]) Patch(id string, changes map[string]any) error {
	// Normalise through JSON first: a caller hands typed Go values —
	// []provider.Operation, time.Time — and encodeNested can only recognise
	// the shapes json.Unmarshal produces. Without this round-trip a typed
	// slice of structs sailed past the nested-array encoding straight into
	// FYLO's EARRAYOFOBJECTS refusal, and every sync's final phase patch was
	// silently lost — the UI audit found weeks of operations stuck at
	// "planning" while the API returned "succeeded".
	raw, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("HD0041: encode patch %s/%s: %w", c.name, id, err)
	}
	var plain map[string]any
	if err := json.Unmarshal(raw, &plain); err != nil {
		return fmt.Errorf("HD0041: normalise patch %s/%s: %w", c.name, id, err)
	}
	encoded, err := encodeNested(plain)
	if err != nil {
		return err
	}
	plain, _ = encoded.(map[string]any)
	if _, err := c.store.db.PatchDoc(c.name, id, plain); err != nil {
		return fmt.Errorf("HD0041: patch %s/%s: %w", c.name, id, err)
	}
	return nil
}

// Get reads one document by id.
func (c Collection[T]) Get(id string) (T, error) {
	var document T
	if id == "" {
		return document, fmt.Errorf("HD0042: read %s: an id is required", c.name)
	}
	raw, err := c.store.db.GetLatest(c.name, id)
	if err != nil {
		return document, fmt.Errorf("HD0042: read %s/%s: %w", c.name, id, err)
	}
	// FYLO answers a single read with an id-keyed envelope, the same shape a
	// find returns, rather than the bare document. Unwrapping it here is the
	// difference between a populated struct and a silently zero one.
	envelope, ok := raw.(map[string]any)
	if !ok {
		return document, fmt.Errorf("HD0042: %s/%s is %T, not an object", c.name, id, raw)
	}
	body, found := envelope[id]
	if !found {
		return document, fmt.Errorf("HD0047: no %s with id %s", c.name, id)
	}
	return decodeInto[T](body, id)
}

// Find returns every matching document, id-tagged, sorted by id. FYLO
// answers a find as an id-keyed object; sorting makes the order stable, and
// because ids are TTIDs that order is chronological.
func (c Collection[T]) Find(query map[string]any) ([]T, error) {
	if query == nil {
		query = map[string]any{}
	}
	// FYLO's grammar wants {"$ops": [{field: {"$eq": value}}, ...]}. A plain
	// {field: value} map is not an error to FYLO — it is ignored, which turns
	// "find mine" into "find everything". That is how a routing bug becomes a
	// cross-project disclosure, so plain maps are normalised here instead of
	// trusting every call site to remember the grammar.
	if _, hasOps := query["$ops"]; !hasOps && len(query) > 0 {
		// One $ops entry with every field: entries OR together in FYLO's
		// grammar, fields within an entry AND together.
		condition := make(map[string]any, len(query))
		for field, value := range query {
			condition[field] = map[string]any{"$eq": value}
		}
		query = map[string]any{"$ops": []any{condition}}
	}
	raw, err := c.store.db.FindDocs(c.name, query)
	if err != nil {
		return nil, fmt.Errorf("HD0043: query %s: %w", c.name, err)
	}
	rows, ok := raw.(map[string]any)
	if !ok {
		// An empty collection can come back as something other than a map.
		return nil, nil
	}

	ids := make([]string, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	documents := make([]T, 0, len(ids))
	for _, id := range ids {
		document, err := decodeInto[T](rows[id], id)
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, nil
}

// Delete removes a document. It is deliberately absent from the audit and
// revision collections' usage: those are append-only by policy, not by API.
func (c Collection[T]) Delete(id string) error {
	if _, err := c.store.db.DelDoc(c.name, id); err != nil {
		return fmt.Errorf("HD0044: delete %s/%s: %w", c.name, id, err)
	}
	return nil
}

// Equals builds the single-field equality query these collections need. FYLO's
// query language is richer; wrapping the one shape in use keeps call sites
// readable and the operator syntax in one place.
func Equals(field string, value any) map[string]any {
	return map[string]any{"$ops": []any{map[string]any{field: map[string]any{"$eq": value}}}}
}

// nestedJSON marks a field the store encoded on the way in. FYLO refuses to
// index an array of objects inside a document — EARRAYOFOBJECTS — and asks
// for a separate collection instead. A DeploySpec's services and an
// operation's steps are neither independently addressable nor independently
// queryable, so a second collection would buy nothing and cost a join.
//
// Instead the store encodes those arrays to a marked string on write and
// decodes them on read. Scalar top-level fields stay untouched, so app_id,
// commit, and phase remain queryable, which is all Find needs. Nothing above
// the store knows this happened.
const nestedJSON = "\x00hd-json:"

func toMap(document any) (map[string]any, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("HD0045: encode document: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, fmt.Errorf("HD0045: normalize document: %w", err)
	}
	encoded2, err := encodeNested(out)
	if err != nil {
		return nil, err
	}
	body, ok := encoded2.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("HD0045: document is %T, not an object", encoded2)
	}
	return body, nil
}

// encodeNested replaces every array-of-objects with a marked JSON string.
func encodeNested(value any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			converted, err := encodeNested(nested)
			if err != nil {
				return nil, err
			}
			out[key] = converted
		}
		return out, nil

	case []any:
		if !containsObject(typed) {
			// An array of scalars indexes fine and stays readable in the
			// on-disk document.
			return typed, nil
		}
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, fmt.Errorf("HD0045: encode nested array: %w", err)
		}
		return nestedJSON + string(encoded), nil

	default:
		return value, nil
	}
}

// decodeNested is encodeNested's inverse.
func decodeNested(value any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			converted, err := decodeNested(nested)
			if err != nil {
				return nil, err
			}
			out[key] = converted
		}
		return out, nil

	case []any:
		out := make([]any, 0, len(typed))
		for _, nested := range typed {
			converted, err := decodeNested(nested)
			if err != nil {
				return nil, err
			}
			out = append(out, converted)
		}
		return out, nil

	case string:
		body, marked := strings.CutPrefix(typed, nestedJSON)
		if !marked {
			return typed, nil
		}
		var decoded any
		if err := json.Unmarshal([]byte(body), &decoded); err != nil {
			return nil, fmt.Errorf("HD0046: decode nested array: %w", err)
		}
		return decodeNested(decoded)

	default:
		return value, nil
	}
}

func containsObject(values []any) bool {
	for _, value := range values {
		switch value.(type) {
		case map[string]any, []any:
			return true
		}
	}
	return false
}

// decodeInto re-attaches the FYLO id, which lives outside the document body,
// and reverses the nested-array encoding toMap applied on the way in.
func decodeInto[T any](raw any, id string) (T, error) {
	var document T
	decoded, err := decodeNested(raw)
	if err != nil {
		return document, err
	}
	body, ok := decoded.(map[string]any)
	if !ok {
		return document, fmt.Errorf("HD0046: document %s is %T, not an object", id, raw)
	}
	body["id"] = id

	encoded, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		return document, fmt.Errorf("HD0046: re-encode %s: %w", id, marshalErr)
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		return document, fmt.Errorf("HD0046: decode %s: %w", id, err)
	}
	return document, nil
}

// Registry is a private image registry HEIMDALL may pull from. It holds a
// credential *reference* and never a credential value — the same rule as
// ${secret:...}, resolved in process at apply time only.
//
// Without this the product cannot deploy a private image, which is most real
// workloads.
type Registry struct {
	ID      string `json:"id,omitempty"`
	Project string `json:"project"`
	Name    string `json:"name"`
	// Server is the registry host, matched against an image reference's
	// registry component. "docker.io" also matches the implicit Docker Hub
	// prefix that a bare `nginx:1.27` carries.
	Server   string `json:"server"`
	Username string `json:"username"`
	// PasswordRef names a secret. A value here would be a credential in a
	// persisted document, which the CI gate exists to prevent.
	PasswordRef string `json:"password_ref"`
	// TargetID scopes the registry to one target. Empty means it applies to
	// every target in the project, which is the common case.
	TargetID  string    `json:"target_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Matches reports whether this registry serves the given image reference.
// The host comparison lives in the provider package so the agent can use it
// without importing a FYLO dependency onto a customer host.
func (r Registry) Matches(image string) bool {
	return provider.RegistryMatches(r.Server, image)
}
