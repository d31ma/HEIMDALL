package azurefunctions

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/d31ma/heimdall/internal/provider"
)

type revisionComparison struct {
	present      bool
	active       bool
	provisioning armappcontainers.RevisionProvisioningState
	health       armappcontainers.RevisionHealthState
	running      armappcontainers.RevisionRunningState
	hasReplicas  bool
	replicas     int32
	hasTemplate  bool
	hasScale     bool
	minimum      int32
	failure      bool
	activeReady  bool
	runningReady bool
	idleReady    bool
}

func (r revisionComparison) ready(revision *nativeRevision) provider.Health {
	r.read(revision)
	r.prepareReadiness()
	return r.classify()
}
func (r *revisionComparison) read(revision *nativeRevision) {
	value := r.value(revision)
	r.present = r.propertiesPresent(revision)
	r.active = (optionalValue[bool]{}).read(value.Active)
	r.provisioning = (optionalValue[armappcontainers.RevisionProvisioningState]{}).read(value.ProvisioningState)
	r.health = (optionalValue[armappcontainers.RevisionHealthState]{}).read(value.HealthState)
	r.running = (optionalValue[armappcontainers.RevisionRunningState]{}).read(value.RunningState)
	r.hasReplicas = value.Replicas != nil
	r.replicas = (optionalValue[int32]{}).read(value.Replicas)
	r.hasTemplate = value.Template != nil
	template := (templateComparison{}).value(value.Template)
	r.hasScale = template.Scale != nil
	scale := (scaleComparison{}).value(template.Scale)
	r.minimum = (optionalValue[int32]{}).read(scale.MinReplicas)
}
func (revisionComparison) value(revision *nativeRevision) nativeRevisionProperties {
	if revision == nil || revision.Properties == nil {
		return nativeRevisionProperties{}
	}
	return *revision.Properties
}
func (revisionComparison) propertiesPresent(revision *nativeRevision) bool {
	return revision != nil && revision.Properties != nil
}
func (r *revisionComparison) prepareReadiness() {
	r.failure = r.failedState()
	r.activeReady = r.activeState()
	r.runningReady = r.runningState()
	r.idleReady = r.idleState()
}
func (r revisionComparison) failedState() bool {
	return r.provisioning == armappcontainers.RevisionProvisioningStateFailed || r.health == armappcontainers.RevisionHealthStateUnhealthy || r.running == armappcontainers.RevisionRunningStateFailed || r.running == armappcontainers.RevisionRunningStateDegraded
}
func (r revisionComparison) activeState() bool {
	return r.active && r.provisioning == armappcontainers.RevisionProvisioningStateProvisioned
}
func (r revisionComparison) runningState() bool {
	return r.health == armappcontainers.RevisionHealthStateHealthy && (r.running == armappcontainers.RevisionRunningStateRunning || r.running == armappcontainers.RevisionRunningStateProcessing)
}
func (r revisionComparison) idleState() bool {
	return r.running == armappcontainers.RevisionRunningStateStopped && r.hasReplicas && r.replicas == 0 && (r.health == armappcontainers.RevisionHealthStateNone || r.health == armappcontainers.RevisionHealthStateHealthy) && r.hasTemplate && r.hasScale && r.minimum == 0
}
func (r revisionComparison) classify() provider.Health {
	switch {
	case !r.present:
		return provider.Progressing
	case r.failure:
		return provider.Degraded
	case !r.activeReady:
		return provider.Progressing
	case r.runningReady || r.idleReady:
		return provider.Healthy
	}
	return provider.Progressing
}
