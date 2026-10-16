package azurefunctions

import "github.com/d31ma/heimdall/app/server/services/provider"

type prunePlan struct{ desired map[string]bool }

func (p prunePlan) operations(live map[string]*nativeApp) []provider.Operation {
	operations := []provider.Operation{}
	for service := range live {
		operations = append(operations, p.operation(service)...)
	}
	return operations
}
func (p prunePlan) operation(service string) []provider.Operation {
	if p.desired[service] {
		return nil
	}
	return []provider.Operation{{Kind: provider.OpDelete, Service: service, Prune: true, Reason: "function is no longer declared"}}
}
