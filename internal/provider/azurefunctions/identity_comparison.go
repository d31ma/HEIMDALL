package azurefunctions

import (
	"reflect"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
)

type identityComparison struct{}

func (c identityComparison) matches(actual, expected *armappcontainers.ManagedServiceIdentity) bool {
	live := c.value(actual)
	desired := c.value(expected)
	return (comparisonChecks{}).all([]bool{
		c.present(actual, expected),
		(optionalValue[armappcontainers.ManagedServiceIdentityType]{}).read(live.Type) == (optionalValue[armappcontainers.ManagedServiceIdentityType]{}).read(desired.Type),
		len(live.UserAssignedIdentities) == len(desired.UserAssignedIdentities),
		reflect.DeepEqual(c.names(live.UserAssignedIdentities), c.names(desired.UserAssignedIdentities)),
	})
}
func (identityComparison) present(actual, expected *armappcontainers.ManagedServiceIdentity) bool {
	return actual != nil && expected != nil
}
func (identityComparison) names(identities map[string]*armappcontainers.UserAssignedIdentity) map[string]bool {
	names := map[string]bool{}
	for name := range identities {
		names[strings.ToLower(name)] = true
	}
	return names
}

func (identityComparison) value(identity *armappcontainers.ManagedServiceIdentity) armappcontainers.ManagedServiceIdentity {
	if identity == nil {
		return armappcontainers.ManagedServiceIdentity{}
	}
	return *identity
}
