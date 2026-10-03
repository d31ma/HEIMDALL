package auth_test

import (
	"context"
	"strings"
	"testing"

	"github.com/d31ma/heimdall/internal/auth"
)

func TestResourceIsColonSeparatedCoarseToFine(t *testing.T) {
	resource, err := auth.Resource("project", "alpha", "app", "checkout")
	if err != nil {
		t.Fatalf("build resource: %v", err)
	}
	// Colon is SESAME's only separator, and a trailing-segment wildcard is its
	// only wildcard, so "project:alpha:*" must be what covers this string.
	if resource != "project:alpha:app:checkout" {
		t.Fatalf("resource = %q", resource)
	}
}

func TestResourceRejectsMalformedSegments(t *testing.T) {
	cases := map[string][]string{
		"odd pair count":  {"project"},
		"empty id":        {"project", ""},
		"embedded colon":  {"project", "al:pha"},
		"embedded slash":  {"project", "al/pha"},
		"uppercase kind":  {"Project", "alpha"},
		"space in id":     {"project", "al pha"},
		"no pairs at all": {},
	}
	for name, pairs := range cases {
		t.Run(name, func(t *testing.T) {
			if resource, err := auth.Resource(pairs...); err == nil {
				t.Fatalf("built %q from malformed input; SESAME would reject it later, "+
					"turning a bad identifier into a skipped decision", resource)
			}
		})
	}
}

func TestActionVocabularyIsClosed(t *testing.T) {
	if !auth.AppSync.Valid() {
		t.Error("app:sync is not in the vocabulary")
	}
	if auth.Action("app:destroy").Valid() {
		t.Error("an invented action passed validation")
	}
	// A duplicated constant would silently shrink a role bundle.
	seen := map[auth.Action]bool{}
	for _, action := range auth.Actions {
		if seen[action] {
			t.Errorf("action %q is listed twice", action)
		}
		seen[action] = true
	}
}

// TestNoSecretReadAction guards a design decision that is easy to erode: no
// route returns a secret value, so no verb may permit reading one.
func TestNoSecretReadAction(t *testing.T) {
	for _, action := range auth.Actions {
		if strings.HasPrefix(action.String(), "secret:") && action != auth.SecretBind {
			t.Fatalf("secret vocabulary gained %q; only secret:bind may exist", action)
		}
	}
}

func TestRoleBundlesOnlyNameKnownActions(t *testing.T) {
	for _, bundle := range auth.RoleBundles {
		if len(bundle.Actions) == 0 {
			t.Errorf("role %q grants nothing", bundle.Name)
		}
		for _, action := range bundle.Actions {
			if !action.Valid() {
				t.Errorf("role %q names unknown action %q", bundle.Name, action)
			}
		}
	}
}

// TestViewerCannotDeployOrReadLogs pins the two separations the plan calls
// out: a viewer may not sync, and log access is not implied by read access.
func TestViewerCannotDeployOrReadLogs(t *testing.T) {
	var viewer []auth.Action
	for _, bundle := range auth.RoleBundles {
		if bundle.Name == "viewer" {
			viewer = bundle.Actions
		}
	}
	if viewer == nil {
		t.Fatal("no viewer role bundle")
	}
	for _, forbidden := range []auth.Action{auth.AppSync, auth.AppRollback, auth.ObserveLogs, auth.AuditRead} {
		for _, granted := range viewer {
			if granted == forbidden {
				t.Errorf("viewer grants %q", forbidden)
			}
		}
	}
}

// TestClosedEngineIsUnavailableNotAllow is the fail-closed unit test. An
// engine that is gone must never produce an Allow, and must be
// distinguishable from a denial so the boundary can answer 503 rather than
// 403.
func TestClosedEngineIsUnavailableNotAllow(t *testing.T) {
	engine := auth.Adopt(nil, "tenant-1")

	decision := engine.Decide(context.Background(), "principal-1", auth.AppRead, "project:alpha")
	if decision.Outcome == auth.Allow {
		t.Fatal("a closed engine allowed a request")
	}
	if decision.Outcome != auth.Unavailable {
		t.Fatalf("outcome = %s, want unavailable so the boundary answers 503", decision.Outcome)
	}
}

func TestUnknownActionAndEmptyInputsDeny(t *testing.T) {
	engine := auth.Adopt(nil, "tenant-1")

	if decision := engine.Decide(context.Background(), "p", auth.Action("app:destroy"), "project:alpha"); decision.Outcome != auth.Deny {
		t.Errorf("unknown action gave %s, want deny", decision.Outcome)
	}
	if decision := engine.Decide(context.Background(), "", auth.AppRead, "project:alpha"); decision.Outcome != auth.Deny {
		t.Errorf("empty principal gave %s, want deny", decision.Outcome)
	}
	if decision := engine.Decide(context.Background(), "p", auth.AppRead, ""); decision.Outcome != auth.Deny {
		t.Errorf("empty resource gave %s, want deny", decision.Outcome)
	}
}

// TestResourceAcceptsTTIDs matters because every stored document is keyed by
// one, and TTIDs are emitted in uppercase while SESAME's pattern alphabet has
// no uppercase at all. Lowercasing is lossless: TTID specifies identifiers as
// case-insensitive and canonically uppercase, so two cannot differ by case.
func TestResourceAcceptsTTIDs(t *testing.T) {
	resource, err := auth.Resource("target", "4VXTUING0MY")
	if err != nil {
		t.Fatalf("a TTID was rejected as a resource id: %v", err)
	}
	if resource != "target:4vxtuing0my" {
		t.Fatalf("resource = %q", resource)
	}

	// The same identifier in either case must produce the same resource, or a
	// grant would match one spelling and not the other.
	lower, err := auth.Resource("target", "4vxtuing0my")
	if err != nil {
		t.Fatalf("lowercase form rejected: %v", err)
	}
	if lower != resource {
		t.Fatalf("case changed the resource: %q vs %q", lower, resource)
	}
}
