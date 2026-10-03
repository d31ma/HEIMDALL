package azurefunctions

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

type armClient struct {
	sdk    *arm.Client
	config targetConfig
}

func (c *armClient) path(name string) string {
	path := "/subscriptions/" + url.PathEscape(c.config.subscription) + "/resourceGroups/" + url.PathEscape(c.config.group) + "/providers/Microsoft.App/containerApps"
	if name != "" {
		path += "/" + url.PathEscape(name)
	}
	return strings.TrimRight(c.sdk.Endpoint(), "/") + path + "?api-version=2026-07-01"
}

func (c *armClient) request(ctx context.Context, method, endpoint string, body any) (*http.Response, error) {
	request, err := runtime.NewRequest(ctx, method, endpoint)
	if err != nil {
		return nil, err
	}
	runtime.SkipBodyDownload(request)
	if body != nil {
		if err = runtime.MarshalAsJSON(request, body); err != nil {
			return nil, err
		}
	}
	response, err := c.sdk.Pipeline().Do(request)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (c *armClient) get(ctx context.Context, name string) (*nativeApp, error) {
	response, err := c.request(ctx, http.MethodGet, c.path(name), nil)
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
	var app nativeApp
	if err := (armResponse{response: response}).decode(&app); err != nil {
		return nil, err
	}
	return &app, nil
}

func (c *armClient) list(ctx context.Context) ([]*nativeApp, error) {
	next := c.path("")
	apps := []*nativeApp{}
	for pages := 0; next != ""; pages++ {
		if pages >= 100 {
			return nil, (&AdapterError{Code: "HD0472", Message: "ARM list exceeds the 100-page limit"})
		}
		response, err := c.request(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			return nil, (armResponse{response: response}).failure()
		}
		var page listPage
		if err := (armResponse{response: response}).decode(&page); err != nil {
			return nil, err
		}
		apps = append(apps, page.Value...)
		if len(apps) > 10000 {
			return nil, (&AdapterError{Code: "HD0472", Message: "ARM list exceeds the 10000-resource limit"})
		}
		if page.NextLink == "" {
			return apps, nil
		}
		base, _ := url.Parse(c.path(""))
		link, err := url.Parse(page.NextLink)
		if err != nil {
			return nil, err
		}
		link = base.ResolveReference(link)
		if link.Scheme != base.Scheme || !strings.EqualFold(link.Host, base.Host) || link.User != nil || link.Path != base.Path || link.Query().Get("api-version") != "2026-07-01" {
			return nil, (&AdapterError{Code: "HD0472", Message: "ARM pagination escaped the target collection or API version"})
		}
		next = link.String()
	}
	return apps, nil
}
