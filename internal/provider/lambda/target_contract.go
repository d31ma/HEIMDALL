package lambda

import (
	"errors"
	"regexp"
	"sort"

	"github.com/d31ma/heimdall/internal/provider"
)

type TargetContract struct{ Target provider.Target }

func (c TargetContract) Validate() error {
	keys := c.keys()
	return errors.Join(c.id(), c.definitionFields(), c.role(), c.settings(keys))
}

func (TargetContract) ExecutionRoleKey() string { return "execution_role_arn" }

func (c TargetContract) id() error {
	if c.Target.ID == "" {
		return (Diagnostic{}).Format((Diagnostic{}).TargetCode(), "Lambda target requires an ID")
	}
	return nil
}

func (c TargetContract) definitionFields() error {
	switch {
	case c.Target.Provider != (&Provider{}).Name():
		return (Diagnostic{}).Format((Diagnostic{}).TargetCode(), "Lambda target must name the Lambda provider")
	case c.Target.Region == "":
		return (Diagnostic{}).Format((Diagnostic{}).TargetCode(), "Lambda target requires a region")
	case c.Target.AgentID != "":
		return (Diagnostic{}).Format((Diagnostic{}).TargetCode(), "Lambda functions initially require direct provider access; agent routing is not implemented")
	case c.Target.Endpoint != "":
		return (Diagnostic{}).Format((Diagnostic{}).TargetCode(), "Lambda target endpoints are not implemented; public invocation URLs need a separate contract")
	}
	return nil
}

func (c TargetContract) role() error {
	if !regexp.MustCompile(`^arn:aws(?:-us-gov|-cn)?:iam::[0-9]{12}:role/[A-Za-z0-9+=,.@_/-]+$`).MatchString(c.Target.Config[c.ExecutionRoleKey()]) {
		return (Diagnostic{}).Format((Diagnostic{}).TargetCode(), "Lambda target requires an existing execution_role_arn")
	}
	return nil
}

func (c TargetContract) keys() []string {
	keys := []string{}
	for key := range c.Target.Config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (c TargetContract) settings(keys []string) error {
	issues := []error{}
	for _, key := range keys {
		issues = append(issues, c.setting(key))
	}
	return errors.Join(issues...)
}

func (c TargetContract) setting(key string) error {
	if key != c.ExecutionRoleKey() {
		return (Diagnostic{}).Format((Diagnostic{}).TargetCode(), "Lambda target setting %s is not implemented; network, concurrency and event-source changes need explicit contracts", key)
	}
	return nil
}

func (c TargetContract) ValidateDefinition() error {
	keys := c.keys()
	return errors.Join(c.definitionFields(), c.role(), c.settings(keys))
}
