package lambda

import (
	"crypto/sha256"
	"fmt"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
	"regexp"
)

type Identity struct {
	App     provider.AppRef
	Service string
}

func (i Identity) Name() string {
	return i.name()
}

func (i Identity) name() string {
	raw := fmt.Sprintf("hd-%s-%s-%s", i.App.Project, i.App.App, i.Service)
	name := regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(raw, "-")
	sum := sha256.Sum256([]byte(raw))
	if len(name) > (functionLimits{}).NameLength() {
		name = name[:(functionLimits{}).NameLength()]
	}
	return fmt.Sprintf("%s-%x", name, sum[:(functionLimits{}).NameDigestBytes()])
}

func (i Identity) Owns(tags map[string]string) bool {
	return i.owns(tags)
}

func (i Identity) owns(tags map[string]string) bool {
	return tags[(ownershipLabels{}).Manager()] == (ownershipLabels{}).ManagerValue() && tags[(ownershipLabels{}).Project()] == i.App.Project && tags[(ownershipLabels{}).App()] == i.App.App && (i.Service == "" || tags[(ownershipLabels{}).Service()] == i.Service)
}

func (i Identity) Tags(plan provider.Plan, service spec.Service, version string) (map[string]string, error) {
	return i.tags(plan, service, version)
}

func (i Identity) tags(plan provider.Plan, service spec.Service, version string) (map[string]string, error) {
	hash, err := spec.HashService(service)
	if err != nil {
		return nil, err
	}
	return map[string]string{(ownershipLabels{}).Manager(): (ownershipLabels{}).ManagerValue(), (ownershipLabels{}).Project(): i.App.Project, (ownershipLabels{}).App(): i.App.App, (ownershipLabels{}).Service(): i.Service, (ownershipLabels{}).Revision(): plan.Revision, (ownershipLabels{}).ServiceHash(): hash, (ownershipLabels{}).SpecHash(): plan.SpecHash, (ownershipLabels{}).Version(): version}, nil
}
