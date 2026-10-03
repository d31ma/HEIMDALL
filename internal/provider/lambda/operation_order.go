package lambda

import (
	"github.com/d31ma/heimdall/internal/provider"
	"sort"
)

type operationOrder struct{}

func (o operationOrder) Sort(operations []provider.Operation) {
	sort.Slice(operations, func(left, right int) bool { return o.before(operations[left], operations[right]) })
}
func (operationOrder) before(left, right provider.Operation) bool {
	if left.Wave != right.Wave {
		return left.Wave < right.Wave
	}
	return left.Service < right.Service
}
