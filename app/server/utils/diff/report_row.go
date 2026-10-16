package diff

import "github.com/d31ma/heimdall/app/server/services/provider"

type reportRow struct {
	report *reportBuilder
	name   string
	index  int
}

func (r *reportRow) ApplyLive(state provider.ServiceState) {
	r.ensure(ServiceDiff{Service: r.name})
	r.live(state)
}
func (r *reportRow) ApplyMissing(operation provider.Operation) {
	r.ensure(ServiceDiff{Service: r.name, Kind: Added})
	r.missingHealth()
	r.missingMessage(operation.Reason)
}
func (r *reportRow) ensure(initial ServiceDiff) {
	index, present := r.report.byName[r.name]
	if !present {
		index = len(r.report.summary.Services)
		r.report.summary.Services = append(r.report.summary.Services, initial)
		r.report.byName[r.name] = index
	}
	r.index = index
}
func (r *reportRow) live(state provider.ServiceState) {
	row := &r.report.summary.Services[r.index]
	row.Health = state.Health
	row.Message = state.Message
	row.Schedule = state.Schedule
	row.LastRunAt = state.LastRunAt
	row.LastRunStatus = state.LastRunStatus
	row.NextRunAt = state.NextRunAt
	row.FunctionVersion = state.FunctionVersion
}
func (r *reportRow) missingHealth() {
	r.report.summary.Services[r.index].Health = provider.Missing
}
func (r *reportRow) missingMessage(reason string) {
	if r.report.summary.Services[r.index].Message == "" {
		r.report.summary.Services[r.index].Message = reason
	}
}
