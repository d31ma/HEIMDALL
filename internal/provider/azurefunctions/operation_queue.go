package azurefunctions

import (
	"context"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
)

type operationQueue struct {
	client    *armClient
	context   context.Context
	plan      provider.Plan
	options   ApplyOptions
	operation provider.Operation
	attempted bool
	result    provider.Result
	err       error
}

func (q *operationQueue) run() (provider.Result, error) {
	q.processOperations()
	return q.result, q.contextError(q.context.Err())
}
func (q *operationQueue) processOperations() {
	for index := 0; index < len(q.plan.Operations) && q.err == nil; index++ {
		q.process(q.plan.Operations[index])
	}
}
func (q *operationQueue) process(operation provider.Operation) {
	q.operation = operation
	q.attempted = false
	q.err = (sequence.Steps{Actions: []func() error{q.context.Err, q.execute}}).Run()
	q.recordFailure()
	q.recordApplied()
}
func (q *operationQueue) execute() error {
	if q.operation.Kind == provider.OpNoop || q.operation.Prune && !q.options.Prune {
		return nil
	}
	q.attempted = true
	operation := functionOperation{client: q.client, context: q.context, plan: q.plan, operation: q.operation, want: q.options.Spec}
	return operation.run()
}
func (q *operationQueue) recordFailure() {
	if q.attempted && q.err != nil {
		q.result.Failures = map[string]string{q.operation.Service: q.err.Error()}
	}
}
func (q *operationQueue) recordApplied() {
	if q.attempted && q.err == nil {
		q.result.Applied = append(q.result.Applied, q.operation)
	}
}
func (q operationQueue) contextError(contextError error) error {
	if q.err != nil {
		return contextError
	}
	return nil
}
