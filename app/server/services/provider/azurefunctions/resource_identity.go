package azurefunctions

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/d31ma/heimdall/app/server/services/provider"
)

type resourceIdentity struct{}

func (resourceIdentity) tag(name string) string { return (resourceTags{}).key(name) }

func (resourceIdentity) name(app provider.AppRef, service string) string {
	name := strings.ToLower("hdf-" + app.Project + (resourceIdentity{}).nameSeparator() + app.App + (resourceIdentity{}).nameSeparator() + service)
	name = strings.Map((resourceIdentity{}).nameCharacter, name)

	digest := sha256.Sum256([]byte(app.Project + (functionContract{}).separator() + app.App + (functionContract{}).separator() + service))
	name = (resourceIdentity{}).shortName(name)
	return strings.TrimRight(name, (resourceIdentity{}).nameSeparator()) + (resourceIdentity{}).nameSeparator() + fmt.Sprintf("%x", digest[:(resourceIdentity{}).digestBytes()])
}

func (r resourceIdentity) owned(app *nativeApp, reference provider.AppRef) bool {
	value := (appValues{}).read(app)
	return (comparisonChecks{}).all([]bool{app != nil, value.tagValue(r.managedTag()) == r.manager(), value.tagValue(r.providerTag()) == (functionContract{}).providerName(), value.tagValue(r.projectTag()) == reference.Project, value.tagValue(r.appTag()) == reference.App, value.tagValue(r.serviceTag()) != ""})
}

func (resourceIdentity) reference(reference, subscription, resourceType string) (string, error) {
	return (resourceReference{}).name(reference, subscription, resourceType)
}

func (resourceIdentity) nameCharacter(character rune) rune {
	if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
		return character
	}
	return '-'
}
func (r resourceIdentity) shortName(name string) string {
	if len(name) > r.namePrefixLimit() {
		return name[:r.namePrefixLimit()]
	}
	return name
}
func (resourceIdentity) namePrefixLimit() int { return 21 }
func (resourceIdentity) digestBytes() int     { return 5 }
func (resourceIdentity) project(project string) string {
	if project == "" {
		return (functionContract{}).defaultProject()
	}
	return project
}

func (resourceIdentity) managedTag() string     { return "managed-by" }
func (resourceIdentity) providerTag() string    { return "provider" }
func (resourceIdentity) projectTag() string     { return "project" }
func (resourceIdentity) appTag() string         { return "app" }
func (resourceIdentity) serviceTag() string     { return "service" }
func (resourceIdentity) revisionTag() string    { return "revision" }
func (resourceIdentity) serviceHashTag() string { return "service-hash" }
func (resourceIdentity) specHashTag() string    { return "spec-hash" }
func (resourceIdentity) manager() string        { return "heimdall" }

func (resourceIdentity) nameSeparator() string { return "-" }
