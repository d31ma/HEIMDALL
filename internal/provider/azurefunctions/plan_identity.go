package azurefunctions

import (
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

type planIdentity struct{}

func (planIdentity) validate(target provider.Target, plan provider.Plan, want spec.DeploySpec) error {
	hash, err := spec.Hash(want)
	if err != nil {
		return err
	}
	project := target.Project
	if project == "" {
		project = "default"
	}
	if hash != plan.SpecHash || plan.Target != target.ID || plan.App.App != want.App || plan.App.Project != project || plan.Revision != want.Revision {
		return (&AdapterError{Code: "HD0475", Message: "plan/spec/target identity mismatch; re-plan before applying"})
	}
	return nil
}
