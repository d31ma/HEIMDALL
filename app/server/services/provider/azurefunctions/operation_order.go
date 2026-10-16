package azurefunctions

import "github.com/d31ma/heimdall/app/server/services/provider"

type operationOrder struct{ operations []provider.Operation }

func (o operationOrder) less(left, right int) bool {
	if o.operations[left].Wave != o.operations[right].Wave {
		return o.operations[left].Wave < o.operations[right].Wave
	}
	return o.operations[left].Service < o.operations[right].Service
}
