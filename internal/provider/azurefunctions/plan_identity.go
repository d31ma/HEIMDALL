package azurefunctions

import (
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
	"github.com/d31ma/heimdall/internal/spec"
)

type planIdentity struct {
	target provider.Target
	plan   provider.Plan
	want   spec.DeploySpec
	hash   string
}

func (planIdentity) validate(target provider.Target, plan provider.Plan, want spec.DeploySpec) error {
	identity := planIdentity{target: target, plan: plan, want: want}
	return (sequence.Steps{Actions: []func() error{identity.hashSpec, identity.matches}}).Run()
}
func (p *planIdentity) hashSpec() error { hash, err := spec.Hash(p.want); p.hash = hash; return err }
func (p *planIdentity) matches() error {
	project := (resourceIdentity{}).project(p.target.Project)
	return p.compare(project)
}
func (p *planIdentity) compare(project string) error {
	if p.hash != p.plan.SpecHash || p.plan.Target != p.target.ID || p.plan.App.App != p.want.App || p.plan.App.Project != project || p.plan.Revision != p.want.Revision {
		return &AdapterError{Code: (functionContract{}).planCode(), Message: "plan/spec/target identity mismatch; re-plan before applying"}
	}
	return nil
}
