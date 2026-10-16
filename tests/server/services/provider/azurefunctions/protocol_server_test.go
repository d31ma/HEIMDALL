package azurefunctions

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/d31ma/heimdall/app/server/services/provider"
)

type protocolServer struct {
	mu        sync.Mutex
	testing   *testing.T
	apps      map[string]*nativeApp
	revisions map[string]*nativeRevision
	serial    map[string]int
	calls     []string
	bodies    []map[string]any
	remaining map[string]int
	stalled   bool
	failed    bool
	frozen    bool
	nextLink  string
	server    *httptest.Server
}

func newProtocolServer(t *testing.T) (*Provider, *protocolServer) {
	t.Helper()
	server := &protocolServer{testing: t}
	server.reset(t)
	server.server = httptest.NewTLSServer(http.HandlerFunc(server.serve))
	t.Cleanup(server.server.Close)
	options := &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: server.server.Client(), Retry: policy.RetryOptions{MaxRetries: -1},
		Cloud: cloud.Configuration{ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/", Services: map[cloud.ServiceName]cloud.ServiceConfiguration{cloud.ResourceManager: {Endpoint: server.server.URL, Audience: "https://management.azure.com"}}},
	}, DisableRPRegistration: true}
	return &Provider{Credential: &azfake.TokenCredential{}, ClientOptions: options}, server
}

func (s *protocolServer) reset(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apps, s.remaining = map[string]*nativeApp{}, map[string]int{}
	s.revisions, s.serial = map[string]*nativeRevision{}, map[string]int{}
	s.calls, s.bodies = nil, nil
	s.stalled, s.failed, s.nextLink = false, false, ""
	s.frozen = false
}

func (s *protocolServer) serve(writer http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.URL.Query().Get("api-version") != "2026-07-01" || !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
		s.testing.Error("real ARM pipeline must send the pinned API version and bearer credential")
		http.Error(writer, "invalid contract", 400)
		return
	}
	name := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
	s.calls = append(s.calls, request.Method+":"+name)
	writer.Header().Set("Content-Type", "application/json")
	if request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/revisions/") {
		revision := s.revisions[request.URL.Path]
		if revision == nil {
			http.Error(writer, "not found", 404)
			return
		}
		if err := json.NewEncoder(writer).Encode(revision); err != nil {
			s.testing.Error(err)
		}
		return
	}
	if request.Method == http.MethodGet && name == "containerApps" {
		page := listPage{NextLink: s.nextLink}
		for _, app := range s.apps {
			page.Value = append(page.Value, app)
		}
		if err := json.NewEncoder(writer).Encode(page); err != nil {
			s.testing.Error(err)
		}
		return
	}
	switch request.Method {
	case http.MethodGet:
		app := s.apps[name]
		if app == nil {
			http.Error(writer, "not found", 404)
			return
		}
		response := *app
		properties := *app.Properties
		response.Properties = &properties
		if s.stalled || s.remaining[name] > 0 {
			properties.LatestReadyRevisionName = pointer("previous-revision")
			if s.remaining[name] > 0 {
				s.remaining[name]--
			}
		}
		if s.failed {
			properties.ProvisioningState = pointer(armappcontainers.ContainerAppProvisioningStateFailed)
		}
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			s.testing.Error(err)
		}
	case http.MethodPut:
		data, err := io.ReadAll(io.LimitReader(request.Body, 1048577))
		if err != nil || len(data) > 1048576 {
			s.testing.Error("invalid request body")
			http.Error(writer, "invalid body", 400)
			return
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			s.testing.Error(err)
			http.Error(writer, "invalid JSON", 400)
			return
		}
		s.bodies = append(s.bodies, raw)
		var app nativeApp
		if err := json.Unmarshal(data, &app); err != nil {
			s.testing.Error(err)
			http.Error(writer, "invalid contract", 400)
			return
		}
		app.Name, app.ID = pointer(name), pointer(request.URL.Path)
		if app.Properties == nil {
			s.testing.Error("missing properties")
			http.Error(writer, "missing properties", 400)
			return
		}
		app.Properties.ProvisioningState = pointer(armappcontainers.ContainerAppProvisioningStateSucceeded)
		previous := s.apps[name]
		if previous != nil && s.frozen {
			app.Properties.LatestRevisionName = previous.Properties.LatestRevisionName
			app.Properties.LatestReadyRevisionName = previous.Properties.LatestReadyRevisionName
		} else {
			revisionName := ""
			if previous != nil {
				previousName := value(previous.Properties.LatestReadyRevisionName)
				old := s.revisions[request.URL.Path+"/revisions/"+previousName]
				if old != nil && (templateComparison{}).matches(old.Properties.Template, app.Properties.Template) {
					revisionName = previousName
				}
			}
			if revisionName == "" {
				s.serial[name]++
				revisionName = fmt.Sprintf("%s--r%d", name, s.serial[name])
				templateData, err := json.Marshal(app.Properties.Template)
				if err != nil {
					s.testing.Error(err)
					http.Error(writer, "invalid template", 400)
					return
				}
				var snapshot nativeTemplate
				if err := json.Unmarshal(templateData, &snapshot); err != nil {
					s.testing.Error(err)
					http.Error(writer, "invalid template", 400)
					return
				}
				path := request.URL.Path + "/revisions/" + revisionName
				s.revisions[path] = &nativeRevision{ID: pointer(path), Name: pointer(revisionName), Properties: &nativeRevisionProperties{
					Active: pointer(true), ProvisioningState: pointer(armappcontainers.RevisionProvisioningStateProvisioned),
					HealthState: pointer(armappcontainers.RevisionHealthStateHealthy), RunningState: pointer(armappcontainers.RevisionRunningStateRunning),
					Replicas: pointer(int32(1)), Template: &snapshot,
				}}
			}
			app.Properties.LatestRevisionName, app.Properties.LatestReadyRevisionName = pointer(revisionName), pointer(revisionName)
		}
		s.apps[name] = &app
		s.remaining[name] = 1
		writer.WriteHeader(http.StatusAccepted)
		writer.Write([]byte(`{}`))
	case http.MethodDelete:
		delete(s.apps, name)
		writer.WriteHeader(http.StatusNoContent)
	default:
		s.testing.Error("unexpected HTTP operation")
		http.Error(writer, "unsupported", 405)
	}
}

func testTarget() provider.Target {
	root := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/test/providers/"
	return provider.Target{ID: "functions", Provider: "azurefunctions", Project: "conf", Region: "westeurope", Endpoint: root + "Microsoft.App/managedEnvironments/functions", Config: map[string]string{
		"subscription_id": "00000000-0000-0000-0000-000000000001", "resource_group": "test",
		"identity_resource_id": root + "Microsoft.ManagedIdentity/userAssignedIdentities/worker",
		"identity_client_id":   "00000000-0000-0000-0000-000000000002", "storage_account_resource_id": root + "Microsoft.Storage/storageAccounts/functionstorage",
	}}
}
