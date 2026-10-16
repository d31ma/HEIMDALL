package azurefunctions

import (
	"context"

	"github.com/d31ma/heimdall/app/server/services/provider"
)

type revisionReadback struct{ client *armClient }

func (r revisionReadback) health(ctx context.Context, app *nativeApp, desired nativeApp) (provider.Health, error) {
	health := (appComparison{}).ready(app)
	matches := (appComparison{}).matches(app, desired)
	revision, err := r.inspectHealthy(ctx, app, health, matches)
	observed := r.compare(app, desired, revision)
	return r.result(observed, err)
}
func (r revisionReadback) inspectHealthy(ctx context.Context, app *nativeApp, health provider.Health, matches bool) (*nativeRevision, error) {
	if health != provider.Healthy || !matches {
		return nil, nil
	}
	return r.inspect(ctx, app)
}
func (r revisionReadback) inspect(ctx context.Context, app *nativeApp) (*nativeRevision, error) {
	value := (appValues{}).read(app)
	properties := (appValues{}).properties(value.Properties)
	name := (optionalValue[string]{}).read(value.Name)
	revision := (optionalValue[string]{}).read(properties.LatestReadyRevisionName)
	available := (comparisonChecks{}).all([]bool{app != nil, value.Properties != nil, revision != ""})
	return r.readAvailable(ctx, name, revision, available)
}
func (r revisionReadback) readAvailable(ctx context.Context, name, revision string, available bool) (*nativeRevision, error) {
	if !available {
		return nil, nil
	}
	return r.read(ctx, name, revision)
}
func (revisionReadback) compare(app *nativeApp, desired nativeApp, revision *nativeRevision) provider.Health {
	health := (appComparison{}).ready(app)
	properties := (appValues{}).properties(desired.Properties)
	actual := (revisionComparison{}).value(revision)
	matches := (comparisonChecks{}).all([]bool{(appComparison{}).matches(app, desired), desired.Properties != nil, (revisionComparison{}).propertiesPresent(revision), (templateComparison{}).matches(actual.Template, properties.Template)})
	revisionHealth := (revisionComparison{}).ready(revision)
	return (revisionReadback{}).comparisonResult(health, matches, revisionHealth)
}
func (revisionReadback) comparisonResult(health provider.Health, matches bool, revisionHealth provider.Health) provider.Health {
	switch {
	case health != provider.Healthy:
		return health
	case !matches:
		return provider.Progressing
	}
	return revisionHealth
}
func (revisionReadback) result(health provider.Health, err error) (provider.Health, error) {
	if err != nil {
		return provider.Progressing, err
	}
	return health, nil
}
func (r revisionReadback) read(ctx context.Context, name, revisionName string) (*nativeRevision, error) {
	resource := revisionResource{client: r.client, name: name, revisionName: revisionName}
	return resource.read(ctx)
}
