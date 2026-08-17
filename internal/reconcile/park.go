package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/d31ma/heimdall/internal/dispatch"
	"github.com/d31ma/heimdall/internal/store"
)

// PendingTTL bounds how long a parked sync stays an intent. A day covers a
// host that sleeps overnight; a sync requested on Monday should not surprise
// a host that reconnects on Friday.
const PendingTTL = 24 * time.Hour

// park defers a sync whose target's agent is offline. The operation document
// stays Pending — that is the durable record an operator sees — and a
// reference goes to the dispatcher, which calls back when the agent returns.
func (e *Engine) park(operation store.Operation, resolved resolved) (store.Operation, error) {
	superseded, ok := e.Dispatcher.Park(dispatch.Parked{
		TargetID:    resolved.target.ID,
		AppID:       resolved.app.ID,
		OperationID: operation.ID,
		ExpiresAt:   e.now().Add(PendingTTL),
	})
	if !ok {
		message := fmt.Sprintf("HD0262: target %s already has %d syncs parked; the agent has been away too long for one more",
			resolved.target.Name, dispatch.MaxParkedPerTarget)
		e.patchOperation(operation.ID, map[string]any{
			"phase": string(store.PhaseFailed), "message": message, "finished_at": e.now(),
		})
		operation.Phase = store.PhaseFailed
		operation.Message = message
		return operation, fmt.Errorf("%s", message)
	}

	if superseded != nil {
		// The older intent is closed pointing at its replacement. On
		// reconnect the host runs one sync, not a backlog.
		e.patchOperation(superseded.OperationID, map[string]any{
			"phase":       string(store.PhaseSuperseded),
			"message":     "superseded by operation " + operation.ID + " while the agent was offline",
			"finished_at": e.now(),
		})
	}

	message := fmt.Sprintf("parked: the agent for target %s is offline; the sync will run when it reconnects",
		resolved.target.Name)
	// The phase is set back to Pending explicitly: the sync had already
	// advanced the document to Planning before the offline answer arrived,
	// and Pending is both what an operator should read and what Repark scans
	// for after a restart.
	e.patchOperation(operation.ID, map[string]any{
		"message": message, "phase": string(store.PhasePending),
	})
	operation.Message = message
	operation.Phase = store.PhasePending
	return operation, nil
}

// Resume drains parked syncs after an agent reconnects. Each parked
// operation is closed pointing at a fresh sync, and the fresh sync re-reads
// git — so an hour of missed commits converges to the newest revision rather
// than marching through every intermediate one.
func (e *Engine) Resume(entries []dispatch.Parked) {
	for _, entry := range entries {
		// Bounded: one sync can pull images for minutes; a drain of many
		// apps must not hold a context forever.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		operation, err := e.Sync(ctx, Request{
			AppID:      entry.AppID,
			ReasonCode: "resumed_after_reconnect",
		})
		cancel()

		message := "drained on reconnect as operation " + operation.ID
		if err != nil && operation.ID == "" {
			// The re-sync could not even start (the app was deleted while
			// the target was away, say). The parked record carries the why.
			message = "could not drain on reconnect: " + err.Error()
			e.patchOperation(entry.OperationID, map[string]any{
				"phase": string(store.PhaseFailed), "message": message, "finished_at": e.now(),
			})
			continue
		}
		e.patchOperation(entry.OperationID, map[string]any{
			"phase": string(store.PhaseSuperseded), "message": message, "finished_at": e.now(),
		})
	}
}

// Expire closes parked operations whose TTL passed before any agent came.
func (e *Engine) Expire(entries []dispatch.Parked) {
	for _, entry := range entries {
		e.patchOperation(entry.OperationID, map[string]any{
			"phase":       string(store.PhaseFailed),
			"message":     "expired: the agent did not reconnect within " + PendingTTL.String(),
			"finished_at": e.now(),
		})
	}
}

// Repark rebuilds the in-memory parking from durable state after a restart.
// Only references were lost — the operation documents are in FYLO — so this
// is a scan, not a replay.
func (e *Engine) Repark() error {
	if e.Dispatcher == nil {
		return nil
	}
	pending, err := store.In[store.Operation](e.Store, store.Operations).
		Find(store.Equals("phase", string(store.PhasePending)))
	if err != nil {
		return err
	}

	targets := store.In[store.Target](e.Store, store.Targets)
	for _, operation := range pending {
		target, err := targets.Get(operation.TargetID)
		if err != nil || target.AgentID == "" {
			// Not an agent target: this Pending operation is a sync the
			// restart interrupted mid-flight, and nothing will finish it.
			e.patchOperation(operation.ID, map[string]any{
				"phase":       string(store.PhaseFailed),
				"message":     "interrupted by a control plane restart",
				"finished_at": e.now(),
			})
			continue
		}
		// TTL continues from when the sync was requested, not from the
		// restart — the intent is not made younger by the process bouncing.
		e.Dispatcher.Park(dispatch.Parked{
			TargetID:    target.ID,
			AppID:       operation.AppID,
			OperationID: operation.ID,
			ExpiresAt:   operation.StartedAt.Add(PendingTTL),
		})
	}
	return nil
}
