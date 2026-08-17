// Package store is HEIMDALL's only door to FYLO. Two constraints from FYLO's
// own documentation shape everything here:
//
//   - Writes are atomic within one collection, never across collections. A
//     sync therefore lives in one hd-operations document holding its whole
//     state machine, and hd-livestate/hd-events/hd-audit are projections
//     folded from it, never independently authoritative.
//   - The root must be a local POSIX or NTFS filesystem. Not EFS, not Azure
//     Files, not NFS, and not a Dropbox or iCloud tree during development.
//     Open refuses the obvious offenders rather than corrupting a root.
//
// A third constraint is enforced by FYLO itself: one root has exactly one
// live engine (EROOTLOCKED). HEIMDALL therefore keeps its own root beside
// SESAME's rather than sharing one, and the deployment directory — not the
// root — is the backup unit.
package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/d31ma/heimdall/internal/store/fylo"
)

// Collections are HEIMDALL's documents. Every name carries the hd- prefix,
// matching SESAME's sesame-* convention, so the two remain distinguishable in
// a combined export or if a future FYLO permits one shared root.
//
// The separator is a hyphen, not the underscore the plan drafted: FYLO's
// validate_collection_name accepts lowercase alphanumerics and '-' only, so
// hd_projects is rejected by the engine outright.
const (
	Projects         = "hd-projects"
	Repos            = "hd-repos"
	Targets          = "hd-targets"
	TargetGroups     = "hd-target-groups"
	GroupMappings    = "hd-group-mappings"
	OutboundWebhooks = "hd-outbound-webhooks"
	Registries       = "hd-registries"
	RootRepo         = "hd-root-repo"
	Applications     = "hd-applications"
	Revisions        = "hd-revisions"
	Operations       = "hd-operations"
	LiveState        = "hd-livestate"
	Events           = "hd-events"
	Rollups          = "hd-rollups"
	Audit            = "hd-audit"
)

// Authoritative collections hold state nothing else can reconstruct.
var Authoritative = []string{
	Projects, Repos, Targets, TargetGroups, GroupMappings, OutboundWebhooks, Registries, RootRepo, Applications, Revisions, Operations, Audit,
}

// Projections are folded from Operations and can be dropped and rebuilt at
// any time. Nothing may treat one as a source of truth.
var Projections = []string{LiveState, Events, Rollups}

// Collections is every collection Bootstrap creates.
var Collections = append(append([]string{}, Authoritative...), Projections...)

// Queue topics. FYLO's brokerless durable queue is the reconciler's work
// queue — there is no Redis, NATS, or SQS in this design.
const (
	TopicRepoPoll     = "repo.poll"
	TopicAppRender    = "app.render"
	TopicAppReconcile = "app.reconcile"
	TopicAppObserve   = "app.observe"
	TopicMetricRollup = "metric.rollup"
)

var Topics = []string{TopicRepoPoll, TopicAppRender, TopicAppReconcile, TopicAppObserve, TopicMetricRollup}

// Store is an open FYLO engine.
type Store struct {
	db   *fylo.Fylo
	root string
}

// Open starts the engine and creates every collection. It is idempotent, so
// it runs on every boot rather than only at init.
func Open(root, binary string) (*Store, error) {
	if root == "" {
		return nil, errors.New("HD0010: fylo root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("HD0010: resolve fylo root: %w", err)
	}
	if err := rejectSyncedFilesystem(absolute); err != nil {
		return nil, err
	}

	db, err := fylo.Open(absolute, binary)
	if err != nil {
		return nil, fmt.Errorf("HD0011: open fylo root %s: %w", absolute, err)
	}
	store := &Store{db: db, root: absolute}
	if err := store.Bootstrap(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// rejectSyncedFilesystem refuses roots under a known file-sync tree. FYLO's
// atomicity depends on local rename semantics a sync client does not provide,
// and the failure mode is silent corruption discovered much later, so this is
// worth being rude about.
//
// ponytail: path matching, not a statfs probe. It catches the development
// mistake this project will actually make; upgrade to a filesystem-type check
// if operators start reporting false positives on a real deployment.
func rejectSyncedFilesystem(path string) error {
	lowered := strings.ToLower(path)
	for _, marker := range []string{"/dropbox/", "/cloudstorage/", "/google drive/", "/onedrive", "/library/mobile documents/"} {
		if strings.Contains(lowered, marker) {
			return fmt.Errorf(
				"HD0012: refusing a FYLO root under a file-sync tree (%s); use a local path such as /tmp/heimdall or ~/.heimdall",
				strings.Trim(marker, "/"),
			)
		}
	}
	return nil
}

// Bootstrap creates every collection, ignoring the ones that already exist.
func (s *Store) Bootstrap() error {
	for _, name := range Collections {
		if _, err := s.db.CreateCollection(name, "document"); err != nil && !alreadyExists(err) {
			return fmt.Errorf("HD0013: create collection %s: %w", name, err)
		}
	}
	return nil
}

// alreadyExists treats a create-on-existing as success. FYLO reports it as an
// operation failure, and matching on the message is the only signal the
// machine protocol gives; the containment is that a genuinely different
// failure still propagates.
func alreadyExists(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "exists") || strings.Contains(message, "already")
}

// DB exposes the raw client for packages that need an operation this wrapper
// has not earned a method for yet.
func (s *Store) DB() *fylo.Fylo { return s.db }

// Root is the absolute FYLO root.
func (s *Store) Root() string { return s.root }

func (s *Store) Close() error { return s.db.Close() }

// Fold turns one hd-operations document into the projection documents it
// implies. Phase 1 supplies the real implementation once an operation has a
// shape; the contract is fixed here because the crash-safety test depends on
// rebuild being byte-reproducible.
type Fold func(id string, operation map[string]any) (collection string, documents []map[string]any, err error)

// Rebuild drops every projection and replays hd-operations through fold. It
// is the recovery path for a crash mid-sync: the authoritative document is
// intact, so the derived views are regenerated rather than repaired.
//
// Operations are folded in ascending id order. TTIDs are time-ordered, so
// that is chronological order, and it is what makes two rebuilds of the same
// history produce the same projection rather than merely the same set.
func (s *Store) Rebuild(fold Fold) error {
	if fold == nil {
		return errors.New("HD0014: rebuild requires a fold")
	}
	for _, name := range Projections {
		if _, err := s.db.DropCollection(name); err != nil {
			return fmt.Errorf("HD0015: drop projection %s: %w", name, err)
		}
		if _, err := s.db.CreateCollection(name, "document"); err != nil {
			return fmt.Errorf("HD0015: recreate projection %s: %w", name, err)
		}
	}

	found, err := s.db.FindDocs(Operations, map[string]any{})
	if err != nil {
		return fmt.Errorf("HD0016: read operations: %w", err)
	}
	// FYLO returns a find as an id-keyed object, not a list.
	rows, ok := found.(map[string]any)
	if !ok {
		return fmt.Errorf("HD0016: unexpected operations result of type %T", found)
	}

	ids := make([]string, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		operation, ok := rows[id].(map[string]any)
		if !ok {
			return fmt.Errorf("HD0017: operation %s is not a document", id)
		}
		collection, documents, err := fold(id, operation)
		if err != nil {
			return fmt.Errorf("HD0017: fold operation %s: %w", id, err)
		}
		for _, document := range documents {
			if _, err := s.db.PutData(collection, document); err != nil {
				return fmt.Errorf("HD0018: write projection %s: %w", collection, err)
			}
		}
	}
	return nil
}
