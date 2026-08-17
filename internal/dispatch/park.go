package dispatch

import (
	"sync"
	"time"
)

// Parking: an agent being offline is normal operation, not an incident —
// agents are outbound-only, and a branch-office host reboots, roams, and
// sleeps. A sync against a disconnected target parks a reference here and its
// operation document stays Pending; when the agent next polls, the drain
// callback re-runs the sync against the live agent.
//
// What is parked is a reference — target, app, operation id — never a job. A
// job carries resolved secret values, and holding those for hours so they can
// be replayed later is exactly the standing exposure the refs-not-values rule
// exists to prevent. The re-run resolves everything fresh, and because it
// re-reads git it converges to the newest desired revision rather than
// marching through every commit the target missed.

// Parked is one deferred sync.
type Parked struct {
	TargetID    string
	AppID       string
	OperationID string
	// ExpiresAt bounds how long the intent survives. A sync requested on
	// Monday should not surprise a host that reconnects on Friday.
	ExpiresAt time.Time
}

// MaxParkedPerTarget bounds memory and honesty alike: past this many distinct
// applications waiting on one host, refusing is better than pretending.
const MaxParkedPerTarget = 16

// parkState is kept separate from the rendezvous maps: parked entries live
// for hours, queued jobs for seconds, and sharing a lock is all they need.
type parkState struct {
	mu sync.Mutex
	// entries per target, at most one per app — a newer sync for the same
	// app supersedes the older one, so a reconnecting host converges instead
	// of replaying a backlog.
	entries map[string]map[string]Parked
	// drain is called with a target's entries when its agent reappears.
	drain func([]Parked)
	// expire is called for entries whose TTL passed before any agent came.
	expire func([]Parked)
}

// SetDrain installs the reconnect callback. The dispatcher calls it from a
// fresh goroutine, so it may do slow work — a drain re-runs syncs.
func (d *Dispatcher) SetDrain(drain func([]Parked), expire func([]Parked)) {
	d.park.mu.Lock()
	defer d.park.mu.Unlock()
	d.park.drain = drain
	d.park.expire = expire
}

// Park defers a sync until the target's agent reconnects. It returns the
// entry it superseded, if any, so the caller can close that operation's
// record honestly. ok is false when the target's parking is full.
func (d *Dispatcher) Park(entry Parked) (superseded *Parked, ok bool) {
	d.park.mu.Lock()
	defer d.park.mu.Unlock()

	if d.park.entries == nil {
		d.park.entries = map[string]map[string]Parked{}
	}
	forTarget := d.park.entries[entry.TargetID]
	if forTarget == nil {
		forTarget = map[string]Parked{}
		d.park.entries[entry.TargetID] = forTarget
	}

	if previous, exists := forTarget[entry.AppID]; exists {
		forTarget[entry.AppID] = entry
		return &previous, true
	}
	if len(forTarget) >= MaxParkedPerTarget {
		return nil, false
	}
	forTarget[entry.AppID] = entry
	return nil, true
}

// ParkedFor lists a target's parked entries, for status displays.
func (d *Dispatcher) ParkedFor(targetID string) []Parked {
	d.park.mu.Lock()
	defer d.park.mu.Unlock()
	entries := make([]Parked, 0, len(d.park.entries[targetID]))
	for _, entry := range d.park.entries[targetID] {
		entries = append(entries, entry)
	}
	return entries
}

// wake is called from Poll when an agent for the target is present. It takes
// the target's entries — atomically, so two polls cannot drain twice — splits
// the expired from the live, and hands each set to its callback.
func (d *Dispatcher) wake(targetID string) {
	d.park.mu.Lock()
	forTarget := d.park.entries[targetID]
	if len(forTarget) == 0 {
		d.park.mu.Unlock()
		return
	}
	delete(d.park.entries, targetID)
	drain, expire := d.park.drain, d.park.expire
	d.park.mu.Unlock()

	now := d.now()
	var live, dead []Parked
	for _, entry := range forTarget {
		if now.After(entry.ExpiresAt) {
			dead = append(dead, entry)
			continue
		}
		live = append(live, entry)
	}

	// Fresh goroutines: Poll is an agent's long poll, and a drain re-runs
	// syncs that can take minutes. The agent should receive "no work yet"
	// now and the drained job on its next poll.
	if len(dead) > 0 && expire != nil {
		go expire(dead)
	}
	if len(live) > 0 && drain != nil {
		go drain(live)
	}
}
