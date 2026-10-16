package azurefunctions

import (
	"strings"

	"github.com/d31ma/heimdall/app/server/services/provider"
)

type appComparison struct{}

func (appComparison) ready(app *nativeApp) provider.Health { return (appReadiness{}).health(app) }
func (c appComparison) matches(live *nativeApp, desired nativeApp) bool {
	actual := (appValues{}).read(live)
	actualProperties := (appValues{}).properties(actual.Properties)
	expectedProperties := (appValues{}).properties(desired.Properties)
	return (comparisonChecks{}).all([]bool{
		c.present(live, desired),
		actual.Kind == desired.Kind,
		strings.EqualFold((optionalValue[string]{}).read(actual.Location), (optionalValue[string]{}).read(desired.Location)),
		(tagComparison{}).matches(actual.Tags, desired.Tags),
		(identityComparison{}).matches(actual.Identity, desired.Identity),
		strings.EqualFold((optionalValue[string]{}).read(actualProperties.EnvironmentID), (optionalValue[string]{}).read(expectedProperties.EnvironmentID)),
		(configurationComparison{}).matches(actualProperties.Configuration),
		(templateComparison{}).matches(actualProperties.Template, expectedProperties.Template),
	})
}
func (appComparison) present(live *nativeApp, desired nativeApp) bool {
	return live != nil && live.Properties != nil && live.Identity != nil && desired.Identity != nil
}
