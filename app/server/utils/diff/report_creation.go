package diff

import "github.com/d31ma/heimdall/app/server/services/provider"

type reportCreation struct {
	report    *reportBuilder
	operation provider.Operation
	eligible  bool
}

func (c *reportCreation) Apply() {
	c.kind()
	c.prune()
	c.running()
	c.record()
}
func (c *reportCreation) kind() {
	if c.operation.Kind != provider.OpCreate {
		c.eligible = false
	}
}
func (c *reportCreation) prune() {
	if c.operation.Prune {
		c.eligible = false
	}
}
func (c *reportCreation) running() {
	if _, present := c.report.input.Live.Services[c.operation.Service]; present {
		c.eligible = false
	}
}
func (c *reportCreation) record() {
	if c.eligible {
		row := reportRow{report: c.report, name: c.operation.Service}
		row.ApplyMissing(c.operation)
	}
}
