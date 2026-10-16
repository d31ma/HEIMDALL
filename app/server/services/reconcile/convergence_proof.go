package reconcile

import (
	"cmp"
	"sort"

	"github.com/d31ma/heimdall/app/server/services/provider"
)

type convergenceProof struct {
	expected     provider.Plan
	observed     provider.Plan
	live         provider.LiveState
	touched      map[string]bool
	observeError error
	planError    error
}

func (p convergenceProof) Reason() string {
	operations := p.indexOperations()
	names := p.serviceNames()
	services := p.serviceReasons(names, operations)
	return cmp.Or(p.observeReason(), p.planReason(), p.targetReason(), p.appReason(), p.hashReason(), services)
}

func (p convergenceProof) observeReason() string {
	if p.observeError != nil {
		return "the runtime could not be read: " + p.observeError.Error()
	}
	return ""
}

func (p convergenceProof) planReason() string {
	if p.planError != nil {
		return "the runtime plan could not be read: " + p.planError.Error()
	}
	return ""
}

func (p convergenceProof) targetReason() string {
	if p.observed.Target != p.expected.Target {
		return "the runtime plan names a different target"
	}
	return ""
}

func (p convergenceProof) appReason() string {
	if p.observed.App != p.expected.App {
		return "the runtime plan names a different application"
	}
	return ""
}

func (p convergenceProof) hashReason() string {
	if p.expected.SpecHash == "" || p.observed.SpecHash != p.expected.SpecHash {
		return "the runtime plan names a different desired spec"
	}
	return ""
}

func (p convergenceProof) indexOperations() map[string][]provider.Operation {
	indexed := map[string][]provider.Operation{}
	for _, operation := range p.observed.Operations {
		indexed[operation.Service] = append(indexed[operation.Service], operation)
	}
	return indexed
}

func (p convergenceProof) serviceNames() []string {
	names := make([]string, 0, len(p.touched))
	for name := range p.touched {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (p convergenceProof) serviceReasons(names []string, operations map[string][]provider.Operation) string {
	reasons := make([]string, 0, len(names))
	for _, name := range names {
		state, present := p.live.Services[name]
		proof := convergenceServiceProof{name: name, operations: operations[name], state: state, present: present}
		reasons = append(reasons, proof.Reason())
	}
	return cmp.Or(reasons...)
}
