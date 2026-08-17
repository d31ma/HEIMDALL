// Package dispatch hands work to agents that the control plane cannot dial.
//
// An agent opens no port: it connects outbound over mTLS and asks for work.
// That inverts the usual direction, so the control plane cannot push. Instead
// a sync parks a job here and waits, and the agent's long poll picks it up.
//
// The rendezvous is in memory and bounded. An agent that is not connected
// makes a sync fail immediately with a message saying so, which is honest for
// Phase 1: durable pending actions for offline targets — with a TTL, a
// bounded depth per target, and superseded jobs collapsing to the newest —
// are a Phase 3 deliverable, and building half of one now would be a queue
// with none of the guarantees.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

// Kind is what an agent is being asked to do. The set is deliberately small:
// an agent runs the same adapter the control plane would have run, so the
// protocol carries intent rather than instructions.
type Kind string

const (
	// KindPlan asks the agent to plan and change nothing.
	KindPlan Kind = "plan"
	// KindSync asks it to plan and apply.
	KindSync Kind = "sync"
	// KindObserve asks it to read live state back.
	KindObserve Kind = "observe"
	// KindInstances lists running units.
	KindInstances Kind = "instances"
	// KindMetrics reads one live stats sample for an instance. History comes
	// from the rollups the agent ships on its own; this is the freshest edge.
	KindMetrics Kind = "metrics"
	// KindLogs reads a bounded tail. Deliberately not a stream: a follow over
	// a long-poll rendezvous would hold the rendezvous hostage, and the UI
	// polls a tail anyway.
	KindLogs Kind = "logs"
	// KindEvents reads the runtime's recent events.
	KindEvents Kind = "events"
)

// Job is one unit of work for an agent.
//
// A sync job carries resolved secret values and registry credentials. That is
// the one place values leave the control plane, and it is deliberate: the
// agent is the process that will create the container, and the alternative —
// every host holding secret-manager credentials of its own — is a far larger
// surface. The transport is mTLS, both ends hold the values in memory only,
// and nothing writes them down. A Job is never persisted.
type Job struct {
	ID       string          `json:"id"`
	TargetID string          `json:"target_id"`
	Kind     Kind            `json:"kind"`
	App      provider.AppRef `json:"app"`
	// Service and Instance address one container, for the observability
	// kinds.
	Service  string `json:"service,omitempty"`
	Instance string `json:"instance,omitempty"`
	// Tail bounds a log read.
	Tail  int             `json:"tail,omitempty"`
	Spec  spec.DeploySpec `json:"spec,omitempty"`
	Prune bool            `json:"prune,omitempty"`
	// Secrets maps a ${secret:...} reference to its value.
	Secrets map[string]string `json:"secrets,omitempty"`
	// Registries are pull credentials, keyed by registry server.
	Registries map[string]provider.RegistryCredential `json:"registries,omitempty"`
}

// Outcome is what the agent reports back. Which fields are set depends on the
// job's Kind.
type Outcome struct {
	JobID     string              `json:"job_id"`
	Plan      provider.Plan       `json:"plan,omitempty"`
	Result    provider.Result     `json:"result,omitempty"`
	Live      provider.LiveState  `json:"live,omitempty"`
	Instances []provider.Instance `json:"instances,omitempty"`
	Series    provider.Series     `json:"series,omitempty"`
	// Logs is a bounded tail, capped agent-side so a misbehaving container
	// cannot push an arbitrary payload through the rendezvous.
	Logs   []byte           `json:"logs,omitempty"`
	Events []provider.Event `json:"events,omitempty"`
	// Error is set when the agent could not run the job at all, as opposed to
	// running it and having individual services fail.
	Error string `json:"error,omitempty"`
}

// ErrNoAgent means no agent for the target is currently polling.
var ErrNoAgent = errors.New("HD0360: no agent is connected for this target")

// ErrAgentTimeout means an agent took the job and never reported.
var ErrAgentTimeout = errors.New("HD0361: the agent took the job and did not report a result")

// pending is one job waiting for an agent, and the channel its result comes
// back on.
type pending struct {
	job     Job
	outcome chan Outcome
	// claimed guards against two agents on one target both running the job.
	claimed bool
}

// waiter is one agent long-polling for work.
type waiter struct {
	jobs chan Job
}

// Dispatcher is the rendezvous. Its zero value is not usable; call New.
type Dispatcher struct {
	// resultTimeout bounds how long a claimed job may run before the sync
	// gives up. It must exceed a realistic image pull.
	resultTimeout time.Duration

	mu sync.Mutex
	// queued holds jobs no agent has claimed yet, per target.
	queued map[string][]*pending
	// inflight holds claimed jobs by job id, so a result can find its waiter.
	inflight map[string]*pending
	// waiting holds agents currently polling, per target.
	waiting map[string][]*waiter
	// lastSeen records when an agent for a target last polled, so an operator
	// can be told "the agent was last seen 4 minutes ago" instead of "no
	// agent".
	lastSeen map[string]time.Time

	// park holds syncs deferred until their target's agent reconnects.
	park parkState

	now func() time.Time
}

// New returns a dispatcher. resultTimeout zero uses ten minutes, which
// comfortably exceeds a cold image pull on a slow link.
func New(resultTimeout time.Duration) *Dispatcher {
	if resultTimeout <= 0 {
		resultTimeout = 10 * time.Minute
	}
	return &Dispatcher{
		resultTimeout: resultTimeout,
		queued:        map[string][]*pending{},
		inflight:      map[string]*pending{},
		waiting:       map[string][]*waiter{},
		lastSeen:      map[string]time.Time{},
		now:           func() time.Time { return time.Now().UTC() },
	}
}

// maxQueuedPerTarget bounds the rendezvous. Reaching it means syncs are being
// submitted faster than one agent can run them, and refusing is better than
// growing without limit.
const maxQueuedPerTarget = 16

// Submit parks a job and blocks until the agent reports, the context is done,
// or the result timeout passes.
func (d *Dispatcher) Submit(ctx context.Context, job Job) (Outcome, error) {
	if job.TargetID == "" || job.ID == "" {
		return Outcome{}, errors.New("HD0362: a job needs a target and an id")
	}

	entry := &pending{job: job, outcome: make(chan Outcome, 1)}

	d.mu.Lock()
	if len(d.queued[job.TargetID]) >= maxQueuedPerTarget {
		d.mu.Unlock()
		return Outcome{}, fmt.Errorf(
			"HD0363: %d jobs are already queued for this target; the agent is not keeping up",
			maxQueuedPerTarget)
	}
	// Hand it straight to a waiting agent if one is already polling.
	if agents := d.waiting[job.TargetID]; len(agents) > 0 {
		agent := agents[0]
		d.waiting[job.TargetID] = agents[1:]
		entry.claimed = true
		d.inflight[job.ID] = entry
		d.mu.Unlock()

		agent.jobs <- job
	} else {
		if _, seen := d.lastSeen[job.TargetID]; !seen {
			d.mu.Unlock()
			return Outcome{}, ErrNoAgent
		}
		d.queued[job.TargetID] = append(d.queued[job.TargetID], entry)
		d.mu.Unlock()
	}

	timer := time.NewTimer(d.resultTimeout)
	defer timer.Stop()

	select {
	case outcome := <-entry.outcome:
		if outcome.Error != "" {
			return outcome, errors.New(outcome.Error)
		}
		return outcome, nil
	case <-ctx.Done():
		d.abandon(job)
		return Outcome{}, ctx.Err()
	case <-timer.C:
		d.abandon(job)
		return Outcome{}, ErrAgentTimeout
	}
}

// abandon removes a job whose submitter has gone away, so a late result does
// not block on a channel nobody reads.
func (d *Dispatcher) abandon(job Job) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.inflight, job.ID)
	remaining := d.queued[job.TargetID][:0]
	for _, entry := range d.queued[job.TargetID] {
		if entry.job.ID != job.ID {
			remaining = append(remaining, entry)
		}
	}
	d.queued[job.TargetID] = remaining
}

// Poll is the agent's long poll. It returns a job when one is available, or
// false when the wait elapses — at which point the agent immediately polls
// again. A returning empty poll is how the connection stays fresh through
// proxies and NAT without a heartbeat protocol of its own.
func (d *Dispatcher) Poll(ctx context.Context, targetID string, wait time.Duration) (Job, bool) {
	d.mu.Lock()
	d.lastSeen[targetID] = d.now()
	d.mu.Unlock()

	// An agent is here, so anything parked for this target can drain. wake
	// hands the entries to callbacks on their own goroutines; this poll
	// continues to the rendezvous and picks the resulting jobs up next time
	// around.
	d.wake(targetID)

	d.mu.Lock()

	// Take a queued job if there is one.
	if queued := d.queued[targetID]; len(queued) > 0 {
		entry := queued[0]
		d.queued[targetID] = queued[1:]
		entry.claimed = true
		d.inflight[entry.job.ID] = entry
		d.mu.Unlock()
		return entry.job, true
	}

	self := &waiter{jobs: make(chan Job, 1)}
	d.waiting[targetID] = append(d.waiting[targetID], self)
	d.mu.Unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case job := <-self.jobs:
		return job, true
	case <-ctx.Done():
		d.stopWaiting(targetID, self)
		return Job{}, false
	case <-timer.C:
		d.stopWaiting(targetID, self)
		// A job may have been handed over between the timer firing and the
		// lock being taken. Check rather than dropping it on the floor.
		select {
		case job := <-self.jobs:
			return job, true
		default:
			return Job{}, false
		}
	}
}

func (d *Dispatcher) stopWaiting(targetID string, self *waiter) {
	d.mu.Lock()
	defer d.mu.Unlock()
	remaining := d.waiting[targetID][:0]
	for _, agent := range d.waiting[targetID] {
		if agent != self {
			remaining = append(remaining, agent)
		}
	}
	d.waiting[targetID] = remaining
}

// Complete delivers an agent's result to the waiting sync.
//
// The target is checked against the job's: an agent authenticates as one
// target, and accepting a result for another would let one host report on
// another's deployments.
func (d *Dispatcher) Complete(targetID string, outcome Outcome) error {
	d.mu.Lock()
	entry, ok := d.inflight[outcome.JobID]
	if ok {
		delete(d.inflight, outcome.JobID)
	}
	d.lastSeen[targetID] = d.now()
	d.mu.Unlock()

	if !ok {
		// The submitter timed out or went away. Not an error the agent can do
		// anything about, and not a reason to fail its request.
		return nil
	}
	if entry.job.TargetID != targetID {
		return fmt.Errorf("HD0364: an agent for %s reported a result for %s", targetID, entry.job.TargetID)
	}
	entry.outcome <- outcome
	return nil
}

// LastSeen reports when an agent for a target last polled, and whether one
// ever has. It turns "no agent is connected" into "the agent was last seen at
// 14:03", which is the difference between a useful error and a shrug.
func (d *Dispatcher) LastSeen(targetID string) (time.Time, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	seen, ok := d.lastSeen[targetID]
	return seen, ok
}

// Connected reports whether an agent for the target is polling right now.
func (d *Dispatcher) Connected(targetID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.waiting[targetID]) > 0
}
