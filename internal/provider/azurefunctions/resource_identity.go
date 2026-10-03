package azurefunctions

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	"github.com/d31ma/heimdall/internal/provider"
)

type resourceIdentity struct{}

func (resourceIdentity) tag(name string) string { return "dev.delma.heimdall." + name }

func (resourceIdentity) name(app provider.AppRef, service string) string {
	name := strings.ToLower("hdf-" + app.Project + "-" + app.App + "-" + service)
	name = strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			return character
		}
		return '-'
	}, name)
	digest := sha256.Sum256([]byte(app.Project + "\x00" + app.App + "\x00" + service))
	if len(name) > 21 {
		name = name[:21]
	}
	return strings.TrimRight(name, "-") + "-" + fmt.Sprintf("%x", digest[:5])
}

func (resourceIdentity) owned(app *nativeApp, reference provider.AppRef) bool {
	return app != nil && app.tagValue("managed-by") == "heimdall" && app.tagValue("provider") == "azurefunctions" && app.tagValue("project") == reference.Project && app.tagValue("app") == reference.App && app.tagValue("service") != ""
}

func (resourceIdentity) reference(reference, subscription, resourceType string) (string, error) {
	parts := strings.Split(reference, "/")
	typeParts := strings.Split(resourceType, "/")
	validName := regexp.MustCompile(`^[a-zA-Z0-9_.()-]{1,90}$`)
	if len(parts) != 9 || parts[0] != "" || !strings.EqualFold(parts[1], "subscriptions") || !strings.EqualFold(parts[2], subscription) || !strings.EqualFold(parts[3], "resourceGroups") || !validName.MatchString(parts[4]) || !strings.EqualFold(parts[5], "providers") || !strings.EqualFold(parts[6], typeParts[0]) || !strings.EqualFold(parts[7], typeParts[1]) || !validName.MatchString(parts[8]) {
		return "", (&AdapterError{Code: "HD0470", Message: fmt.Sprintf("expected an existing %s ARM resource ID in the target subscription", resourceType)})
	}
	return parts[8], nil
}
