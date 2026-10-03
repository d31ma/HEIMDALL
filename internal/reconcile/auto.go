package reconcile

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/d31ma/heimdall/internal/diff"
	"github.com/d31ma/heimdall/internal/store"
)

// Auto is the loop that syncs without a human.
//
// Auto-sync and self-heal are the same mechanism, not two: `Status` refreshes
// from git and reads live state, and anything that makes those disagree comes
// back as OutOfSync. A new commit and a container killed by hand are the same
// observation, so the only thing the two policy flags decide is which of them
// this application consented to.
type Auto struct {
	Engine   *Engine
	Interval time.Duration
	Logger   *slog.Logger
	// RegistrySync runs ADR 0010's registry pass before each application
	// pass, when a root repository is bound. It runs here, in the loop's own
	// goroutine, for the same reason nudges do: one goroutine, no overlap.
	RegistrySync func(ctx context.Context)

	// nudges carries repository ids from the webhook receiver into the one
	// loop that runs passes, so a push and the ticker can never sync the
	// same application concurrently. Bounded: a full channel drops the
	// nudge, and the next tick covers whatever was dropped.
	initialize sync.Once
	nudges     chan string
}

func (a *Auto) channel() chan string {
	a.initialize.Do(func() { a.nudges = make(chan string, 64) })
	return a.nudges
}

// Nudge asks for a pass over one repository's applications soon. It never
// blocks: the caller is a webhook answering a forge with a timeout.
func (a *Auto) Nudge(repoID string) {
	select {
	case a.channel() <- repoID:
	default:
		// Full means a storm, and a storm is exactly when one more pass
		// helps least. The regular tick is the backstop.
	}
}

// Run ticks until the context ends, and runs a scoped pass whenever a
// webhook nudges — both from this one goroutine, so passes never overlap.
func (a *Auto) Run(ctx context.Context) {
	interval := a.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if a.RegistrySync != nil {
				a.RegistrySync(ctx)
			}
			a.Once(ctx)
		case first := <-a.channel():
			// Drain whatever arrived while the last pass ran: ten pushes
			// coalesce into one pass over the union of their repositories.
			repos := map[string]bool{first: true}
			for drained := false; !drained; {
				select {
				case id := <-a.channel():
					repos[id] = true
				default:
					drained = true
				}
			}
			a.pass(ctx, repos)
		}
	}
}

// Once runs a single unscoped pass. Run calls it on each tick; a test calls
// it directly rather than waiting on wall-clock time.
func (a *Auto) Once(ctx context.Context) {
	a.pass(ctx, nil)
}

// pass considers every application, or only those on the given repositories
// when a webhook scoped it.
func (a *Auto) pass(ctx context.Context, repos map[string]bool) {
	apps, err := store.In[store.Application](a.Engine.Store, store.Applications).Find(nil)
	if err != nil {
		a.Logger.Error("auto-sync could not list applications", "error", err)
		return
	}

	for _, app := range apps {
		if repos != nil && !repos[app.RepoID] {
			continue
		}
		if app.Suspended || (!app.SyncPolicy.Automated && !app.SyncPolicy.SelfHeal) {
			continue
		}
		// ponytail: group applications are not auto-synced yet — Status is
		// single-target, and fanning out on every tick needs a per-target
		// drift read first. Add when a fleet asks for self-heal.
		if app.GroupID != "" {
			continue
		}

		summary, err := a.Engine.Status(ctx, app.ID)
		if err != nil {
			// A target that is down is normal — an agent host reboots, a
			// cloud API rate-limits. Log and try again next tick rather than
			// stopping the loop for every other application.
			a.Logger.Warn("auto-sync could not read status",
				"app", app.Name, "project", app.Project, "error", err)
			continue
		}
		if summary.SyncStatus != diff.OutOfSync {
			continue
		}

		// Which policy consented: a moved revision is auto-sync, the same
		// revision drifting underneath is self-heal.
		automated := summary.Live != summary.Desired && app.SyncPolicy.Automated
		healing := summary.Live == summary.Desired && app.SyncPolicy.SelfHeal
		if !automated && !healing {
			continue
		}

		reason := "auto_sync"
		if healing {
			reason = "self_heal"
		}

		// PrincipalID is empty because no principal asked. The operation
		// document records the reason instead, so an audit trail says "the
		// policy did this" rather than attributing it to whoever created the
		// application last.
		if _, err := a.Engine.Sync(ctx, Request{
			AppID:      app.ID,
			ReasonCode: reason,
		}); err != nil {
			a.Logger.Error("auto-sync failed",
				"app", app.Name, "project", app.Project, "reason", reason, "error", err)
			continue
		}
		a.Logger.Info("auto-synced", "app", app.Name, "project", app.Project, "reason", reason)
	}
}
