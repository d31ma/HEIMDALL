package azurefunctions

import "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"

type nativeScale struct {
	AllowScalingRuleOverride *bool                         `json:"allowScalingRuleOverride,omitempty"`
	CooldownPeriod           *int32                        `json:"cooldownPeriod,omitempty"`
	MaxReplicas              *int32                        `json:"maxReplicas,omitempty"`
	MinReplicas              *int32                        `json:"minReplicas,omitempty"`
	PollingInterval          *int32                        `json:"pollingInterval,omitempty"`
	Rules                    []*armappcontainers.ScaleRule `json:"rules,omitempty"`
}
