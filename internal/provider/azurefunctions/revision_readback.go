package azurefunctions

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/d31ma/heimdall/internal/provider"
)

type revisionReadback struct{ client *armClient }

func (r revisionReadback) health(ctx context.Context, app *nativeApp, desired nativeApp) (provider.Health, error) {
	health := (appComparison{}).ready(app)
	if health != provider.Healthy {
		return health, nil
	}
	if !(appComparison{}).matches(app, desired) {
		return provider.Progressing, nil
	}
	revision, err := r.inspect(ctx, app)
	if err != nil {
		return provider.Progressing, err
	}
	return r.compare(app, desired, revision), nil
}

func (r revisionReadback) inspect(ctx context.Context, app *nativeApp) (*nativeRevision, error) {
	if app == nil || app.Properties == nil || (optionalValue[string]{}).read(app.Properties.LatestReadyRevisionName) == "" {
		return nil, nil
	}
	return r.read(ctx, (optionalValue[string]{}).read(app.Name), (optionalValue[string]{}).read(app.Properties.LatestReadyRevisionName))
}

func (revisionReadback) compare(app *nativeApp, desired nativeApp, revision *nativeRevision) provider.Health {
	health := (appComparison{}).ready(app)
	if health != provider.Healthy {
		return health
	}
	if !(appComparison{}).matches(app, desired) || desired.Properties == nil {
		return provider.Progressing
	}
	if revision == nil || revision.Properties == nil || !(templateComparison{}).matches(revision.Properties.Template, desired.Properties.Template) {
		return provider.Progressing
	}
	return (revisionComparison{}).ready(revision)
}

func (r revisionReadback) read(ctx context.Context, name, revisionName string) (*nativeRevision, error) {
	valid := regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,255}$`)
	if !valid.MatchString(name) || !valid.MatchString(revisionName) {
		return nil, &AdapterError{Code: "HD0472", Message: "ARM returned an invalid native function or ready revision name"}
	}
	endpoint, err := url.Parse(r.client.path(name))
	if err != nil {
		return nil, err
	}
	endpoint.Path += "/revisions/" + revisionName
	endpoint.RawPath = ""
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	response, err := r.client.request(bounded, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusNotFound {
		response.Body.Close()
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, (armResponse{response: response}).failure()
	}
	var revision nativeRevision
	if err := (armResponse{response: response}).decode(&revision); err != nil {
		return nil, err
	}
	if (optionalValue[string]{}).read(revision.Name) != revisionName || !strings.EqualFold((optionalValue[string]{}).read(revision.ID), endpoint.Path) {
		return nil, &AdapterError{Code: "HD0472", Message: "ARM ready revision response does not identify the requested resource"}
	}
	return &revision, nil
}
