package diff

import (
	"github.com/d31ma/heimdall/app/server/services/provider"
	"sort"
)

type reportBuilder struct {
	input   ReportInput
	summary Summary
	byName  map[string]int
}

func (r *reportBuilder) Build() Summary {
	r.initialize()
	r.planStatus()
	r.unreadableStatus()
	r.indexServices()
	r.liveServices()
	r.missingServices()
	mergedHealth := rollup(r.summary.Services)
	r.health(mergedHealth)
	r.order()
	return r.summary
}
func (r *reportBuilder) initialize() {
	r.summary = Summary{App: r.input.App, Target: r.input.Target, Desired: r.input.DesiredRevision,
		Live: r.input.Live.Revision, Services: r.input.Services, Health: r.input.Live.Rollup(), SyncStatus: Synced}
	r.byName = map[string]int{}
}
func (r *reportBuilder) planStatus() {
	if r.input.Plan.Changes() {
		r.summary.SyncStatus = OutOfSync
	}
}
func (r *reportBuilder) unreadableStatus() {
	if r.input.LiveReadFailed {
		r.summary.SyncStatus = Unknown
		r.summary.Health = provider.Missing
	}
}
func (r *reportBuilder) indexServices() {
	for index, service := range r.summary.Services {
		r.byName[service.Service] = index
	}
}
func (r *reportBuilder) liveServices() {
	for name, state := range r.input.Live.Services {
		row := reportRow{report: r, name: name}
		row.ApplyLive(state)
	}
}
func (r *reportBuilder) missingServices() {
	for _, operation := range r.input.Plan.Operations {
		creation := reportCreation{report: r, operation: operation, eligible: true}
		creation.Apply()
	}
}
func (r *reportBuilder) health(merged provider.Health) {
	if !r.input.LiveReadFailed {
		r.summary.Health = merged
	}
}
func (r *reportBuilder) order() {
	sort.Slice(r.summary.Services, func(i, j int) bool { return r.summary.Services[i].Service < r.summary.Services[j].Service })
}
