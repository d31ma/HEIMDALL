package reconcile

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/d31ma/heimdall/internal/store"
)

// Fan-out: one application, N targets selected by a group, each with its own
// operation document, bounded concurrency, and a failure threshold that halts
// the rollout. Edge and branch fleets are the primary case, not an advanced
// one — fifty shops running the same compose file is the product working as
// intended, and a bad image reaching seven of them instead of fifty is the
// difference bounded rollout makes.

// fanOut deploys a group application to every member target. It returns an
// umbrella operation summarising the rollout; each member sync writes its own
// operation document, so per-target history and drift stay first-class.
func (e *Engine) fanOut(ctx context.Context, request Request, app store.Application) (store.Operation, error) {
	group, err := store.In[store.TargetGroup](e.Store, store.TargetGroups).Get(app.GroupID)
	if err != nil {
		return store.Operation{}, fmt.Errorf("HD0264: application %s names group %s: %w", app.Name, app.GroupID, err)
	}
	candidates, err := store.In[store.Target](e.Store, store.Targets).
		Find(map[string]any{"project": group.Project})
	if err != nil {
		return store.Operation{}, err
	}
	members := make([]store.Target, 0, len(candidates))
	for _, target := range candidates {
		if group.Matches(target) {
			members = append(members, target)
		}
	}
	// Stable order: a halted rollout must be describable as "hosts 1–6
	// deployed, host 7 failed, hosts 8–50 untouched", which needs an order.
	sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })

	operations := store.In[store.Operation](e.Store, store.Operations)
	umbrella := store.Operation{
		AppID: app.ID, Project: app.Project, App: app.Name,
		TargetID: "group:" + group.ID,
		Phase:    store.PhaseApplying, DryRun: request.DryRun,
		PrincipalID: request.PrincipalID, PolicyVersion: request.PolicyVersion,
		ReasonCode: request.ReasonCode, StartedAt: e.now(),
	}
	umbrellaID, err := operations.Put(umbrella)
	if err != nil {
		return store.Operation{}, err
	}
	umbrella.ID = umbrellaID

	if len(members) == 0 {
		umbrella.Phase = store.PhaseFailed
		umbrella.Message = fmt.Sprintf("group %s selects no targets", group.Name)
		umbrella.FinishedAt = e.now()
		e.patchOperation(umbrellaID, map[string]any{
			"phase": string(umbrella.Phase), "message": umbrella.Message, "finished_at": umbrella.FinishedAt,
		})
		return umbrella, fmt.Errorf("HD0265: %s", umbrella.Message)
	}

	parallel := app.SyncPolicy.MaxParallel
	if parallel <= 0 {
		parallel = 4
	}
	threshold := app.SyncPolicy.FailureThreshold
	if threshold <= 0 {
		threshold = 1
	}

	// Waves of at most `parallel`, halting between waves once the threshold
	// is crossed. Inside a wave, syncs already in flight run to completion —
	// halting means not starting new hosts, not killing half-applied ones.
	var mu sync.Mutex
	succeeded, failed := 0, 0
	var firstFailure string
	untouched := len(members)

	for start := 0; start < len(members); start += parallel {
		mu.Lock()
		halted := failed >= threshold
		mu.Unlock()
		if halted {
			break
		}

		end := start + parallel
		if end > len(members) {
			end = len(members)
		}
		var wave sync.WaitGroup
		for _, target := range members[start:end] {
			wave.Add(1)
			go func(target store.Target) {
				defer wave.Done()
				child := request
				child.TargetOverride = target.ID
				child.ReasonCode = request.ReasonCode
				operation, err := e.Sync(ctx, child)
				mu.Lock()
				defer mu.Unlock()
				untouched--
				if err != nil || operation.Phase == store.PhaseFailed {
					failed++
					if firstFailure == "" {
						message := operation.Message
						if err != nil {
							message = err.Error()
						}
						firstFailure = fmt.Sprintf("%s: %s", target.Name, message)
					}
					return
				}
				succeeded++
			}(target)
		}
		wave.Wait()
	}

	umbrella.FinishedAt = e.now()
	switch {
	case failed >= threshold:
		umbrella.Phase = store.PhaseFailed
		umbrella.Message = fmt.Sprintf(
			"halted: %d deployed, %d failed (first: %s), %d of %d hosts untouched",
			succeeded, failed, firstFailure, untouched, len(members))
	case failed > 0:
		umbrella.Phase = store.PhaseFailed
		umbrella.Message = fmt.Sprintf("%d deployed, %d failed (first: %s)", succeeded, failed, firstFailure)
	default:
		umbrella.Phase = store.PhaseSucceeded
		umbrella.Message = fmt.Sprintf("deployed to all %d targets", len(members))
	}
	e.patchOperation(umbrellaID, map[string]any{
		"phase": string(umbrella.Phase), "message": umbrella.Message, "finished_at": umbrella.FinishedAt,
	})

	e.Notify(umbrella)
	if umbrella.Phase == store.PhaseFailed {
		return umbrella, fmt.Errorf("HD0266: rollout of %s: %s", app.Name, umbrella.Message)
	}
	return umbrella, nil
}
