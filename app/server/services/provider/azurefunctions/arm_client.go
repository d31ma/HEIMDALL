package azurefunctions

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

type armClient struct {
	sdk    *arm.Client
	config targetConfig
}

func (c *armClient) path(name string) string {
	path := "/subscriptions/" + url.PathEscape(c.config.subscription) + "/resourceGroups/" + url.PathEscape(c.config.group) + "/providers/Microsoft.App/containerApps"
	return strings.TrimRight(c.sdk.Endpoint(), (functionContract{}).pathSeparator()) + path + c.resourceSuffix(name) + "?api-version=" + (functionContract{}).apiVersion()
}
func (*armClient) resourceSuffix(name string) string {
	if name == "" {
		return ""
	}
	return (functionContract{}).pathSeparator() + url.PathEscape(name)
}
func (c *armClient) request(ctx context.Context, method, endpoint string, body any) (*http.Response, error) {
	request := armRequest{client: c, context: ctx, method: method, endpoint: endpoint, body: body}
	return request.run()
}
func (c *armClient) get(ctx context.Context, name string) (*nativeApp, error) {
	var app nativeApp
	read := armRead{client: c, context: ctx, endpoint: c.path(name), destination: &app, allowMissing: true}
	err := read.run()
	return c.appResult(&app, read.missing, err)
}
func (*armClient) appResult(app *nativeApp, missing bool, err error) (*nativeApp, error) {
	if err != nil || missing {
		return nil, err
	}
	return app, nil
}
func (c *armClient) list(ctx context.Context) ([]*nativeApp, error) {
	pages := appPages{client: c, context: ctx, next: c.path("")}
	return pages.read()
}
