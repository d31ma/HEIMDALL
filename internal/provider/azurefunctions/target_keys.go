package azurefunctions

import (
	"fmt"
	"sort"
)

type targetKeys struct{}

func (k targetKeys) validate(config map[string]string) error {
	unsupported := k.unsupported(config)
	sort.Strings(unsupported)
	return k.rejection(unsupported)
}
func (k targetKeys) unsupported(config map[string]string) []string {
	unsupported := []string{}
	for key := range config {
		unsupported = append(unsupported, k.unknown(key)...)
	}
	return unsupported
}
func (targetKeys) unknown(key string) []string {
	contract := functionContract{}
	switch key {
	case contract.subscriptionKey(), contract.groupKey(), contract.storageKey(), contract.identityKey(), contract.clientKey():
		return nil
	default:
		return []string{key}
	}
}
func (targetKeys) rejection(unsupported []string) error {
	if len(unsupported) == 0 {
		return nil
	}
	return &AdapterError{Code: (functionContract{}).configurationCode(), Message: fmt.Sprintf("unsupported azurefunctions target configuration keys %q; supported keys are subscription_id, resource_group, storage_account_resource_id, identity_resource_id and identity_client_id", unsupported)}
}
