package azurefunctions

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/d31ma/heimdall/internal/provider"
)

type revisionComparison struct{}

func (revisionComparison) ready(revision *nativeRevision) provider.Health {
	if revision == nil || revision.Properties == nil {
		return provider.Progressing
	}
	p := revision.Properties
	provisioning := (optionalValue[armappcontainers.RevisionProvisioningState]{}).read(p.ProvisioningState)
	health := (optionalValue[armappcontainers.RevisionHealthState]{}).read(p.HealthState)
	running := (optionalValue[armappcontainers.RevisionRunningState]{}).read(p.RunningState)
	if provisioning == armappcontainers.RevisionProvisioningStateFailed || health == armappcontainers.RevisionHealthStateUnhealthy || running == armappcontainers.RevisionRunningStateFailed || running == armappcontainers.RevisionRunningStateDegraded {
		return provider.Degraded
	}
	if !(optionalValue[bool]{}).read(p.Active) || provisioning != armappcontainers.RevisionProvisioningStateProvisioned {
		return provider.Progressing
	}
	if health == armappcontainers.RevisionHealthStateHealthy && (running == armappcontainers.RevisionRunningStateRunning || running == armappcontainers.RevisionRunningStateProcessing) {
		return provider.Healthy
	}
	if running == armappcontainers.RevisionRunningStateStopped && p.Replicas != nil && *p.Replicas == 0 && (health == armappcontainers.RevisionHealthStateNone || health == armappcontainers.RevisionHealthStateHealthy) && p.Template != nil && p.Template.Scale != nil && (optionalValue[int32]{}).read(p.Template.Scale.MinReplicas) == 0 {
		return provider.Healthy
	}
	return provider.Progressing
}
