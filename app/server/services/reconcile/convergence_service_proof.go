package reconcile

import (
	"cmp"

	"github.com/d31ma/heimdall/app/server/services/provider"
)

type convergenceServiceProof struct {
	name       string
	operations []provider.Operation
	state      provider.ServiceState
	present    bool
}

func (p convergenceServiceProof) Reason() string {
	message := p.healthMessage()
	operations := p.operationReasons()
	return cmp.Or(p.missingPlan(), operations, p.missingLive(), p.healthReason(message))
}

func (p convergenceServiceProof) missingPlan() string {
	if len(p.operations) == 0 {
		return p.name + " has no current runtime plan proof"
	}
	return ""
}

func (p convergenceServiceProof) operationReasons() string {
	reasons := make([]string, 0, len(p.operations))
	for _, operation := range p.operations {
		reasons = append(reasons, p.OperationReason(operation))
	}
	return cmp.Or(reasons...)
}

func (p convergenceServiceProof) OperationReason(operation provider.Operation) string {
	return p.operationReason(operation)
}

func (p convergenceServiceProof) operationReason(operation provider.Operation) string {
	if operation.Kind != provider.OpNoop {
		return p.name + " still requires runtime " + string(operation.Kind)
	}
	return ""
}

func (p convergenceServiceProof) missingLive() string {
	if !p.present {
		return p.name + " is not running"
	}
	return ""
}

func (p convergenceServiceProof) healthMessage() string {
	if p.state.Message != "" {
		return " (" + p.state.Message + ")"
	}
	return ""
}

func (p convergenceServiceProof) healthReason(message string) string {
	if p.state.Health != provider.Healthy {
		return p.name + " is " + string(p.state.Health) + message
	}
	return ""
}
