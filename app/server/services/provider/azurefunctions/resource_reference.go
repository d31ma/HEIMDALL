package azurefunctions

import (
	"fmt"
	"regexp"
	"strings"
)

type resourceReference struct {
	parts        []string
	typeParts    []string
	subscription string
	resourceType string
}

func (r resourceReference) name(reference, subscription, resourceType string) (string, error) {
	r.parts = strings.Split(reference, (functionContract{}).pathSeparator())
	r.typeParts = strings.Split(resourceType, (functionContract{}).pathSeparator())
	r.subscription, r.resourceType = subscription, resourceType
	err := r.validate()
	return r.result(err)
}
func (r resourceReference) validate() error {
	validName := regexp.MustCompile(r.namePattern())
	if len(r.parts) != r.resourceParts() || len(r.typeParts) != r.typePartCount() || r.parts[r.rootIndex()] != "" || !strings.EqualFold(r.parts[r.subscriptionLabelIndex()], "subscriptions") || !strings.EqualFold(r.parts[r.subscriptionIndex()], r.subscription) || !strings.EqualFold(r.parts[r.groupLabelIndex()], "resourceGroups") || !validName.MatchString(r.parts[r.groupIndex()]) || !strings.EqualFold(r.parts[r.providerLabelIndex()], "providers") || !strings.EqualFold(r.parts[r.namespaceIndex()], r.typeParts[r.namespacePartIndex()]) || !strings.EqualFold(r.parts[r.typeIndex()], r.typeParts[r.typePartIndex()]) || !validName.MatchString(r.parts[r.nameIndex()]) {
		return &AdapterError{Code: (functionContract{}).configurationCode(), Message: fmt.Sprintf("expected an existing %s ARM resource ID in the target subscription", r.resourceType)}
	}
	return nil
}
func (r resourceReference) result(err error) (string, error) {
	if err != nil {
		return "", err
	}
	return r.parts[r.nameIndex()], nil
}
func (resourceReference) resourceParts() int          { return 9 }
func (resourceReference) typePartCount() int          { return 2 }
func (resourceReference) rootIndex() int              { return 0 }
func (resourceReference) subscriptionLabelIndex() int { return 1 }
func (resourceReference) subscriptionIndex() int      { return 2 }
func (resourceReference) groupLabelIndex() int        { return 3 }
func (resourceReference) groupIndex() int             { return 4 }
func (resourceReference) providerLabelIndex() int     { return 5 }
func (resourceReference) namespaceIndex() int         { return 6 }
func (resourceReference) typeIndex() int              { return 7 }
func (resourceReference) nameIndex() int              { return 8 }
func (resourceReference) namespacePartIndex() int     { return 0 }
func (resourceReference) typePartIndex() int          { return 1 }

func (resourceReference) namePattern() string { return `^[a-zA-Z0-9_.()-]{1,90}$` }
