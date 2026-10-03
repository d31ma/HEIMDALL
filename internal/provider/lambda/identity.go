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
	if len(name) > 51 {
		name = name[:51]
	}
	return fmt.Sprintf("%s-%x", name, sum[:6])
}

func (i Identity) Owns(tags map[string]string) bool {
	return i.owns(tags)
}

func (i Identity) owns(tags map[string]string) bool {
	return tags["dev.delma.heimdall.managed-by"] == "heimdall" && tags["dev.delma.heimdall.project"] == i.App.Project && tags["dev.delma.heimdall.app"] == i.App.App && (i.Service == "" || tags["dev.delma.heimdall.service"] == i.Service)
}

func (i Identity) Tags(plan provider.Plan, service spec.Service, version string) (map[string]string, error) {
	return i.tags(plan, service, version)
}

func (i Identity) tags(plan provider.Plan, service spec.Service, version string) (map[string]string, error) {
	hash, err := spec.HashService(service)
	if err != nil {
		return nil, err
	}
	return map[string]string{"dev.delma.heimdall.managed-by": "heimdall", "dev.delma.heimdall.project": i.App.Project, "dev.delma.heimdall.app": i.App.App, "dev.delma.heimdall.service": i.Service, "dev.delma.heimdall.revision": plan.Revision, "dev.delma.heimdall.service-hash": hash, "dev.delma.heimdall.spec-hash": plan.SpecHash, "dev.delma.heimdall.function-version": version}, nil
}
