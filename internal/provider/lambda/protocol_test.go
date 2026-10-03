package lambda_test

import (
	"encoding/json"
	"fmt"
	"github.com/aws/smithy-go/encoding/cbor"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type protocol struct {
	mu                  sync.Mutex
	server              *httptest.Server
	functions           map[string]map[string]any
	images              map[string]string
	tags                map[string]map[string]string
	versions            map[string]map[string]map[string]any
	versionImages       map[string]map[string]string
	mutations           []string
	failUpdate          bool
	pendingReads        int
	requests            int
	invokeCalls         int
	invokeFailure       bool
	handlerFailure      bool
	oversizedResponse   bool
	lastQualifier       string
	lastInput           []byte
	metricInput         []byte
	logInput            map[string]any
	ownershipLossAt     string
	publicationConflict bool
}

func newProtocol(t *testing.T) *protocol {
	p := &protocol{}
	p.reset()
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.server.Close)
	return p
}

func (p *protocol) reset() {
	p.functions = map[string]map[string]any{}
	p.images = map[string]string{}
	p.tags = map[string]map[string]string{}
	p.versions = map[string]map[string]map[string]any{}
	p.versionImages = map[string]map[string]string{}
	p.mutations = nil
	p.failUpdate = false
	p.pendingReads = 0
	p.requests = 0
	p.invokeCalls = 0
	p.invokeFailure = false
	p.handlerFailure = false
	p.oversizedResponse = false
	p.ownershipLossAt = ""
	p.publicationConflict = false
}

func (p *protocol) clone(input map[string]any) map[string]any {
	b, _ := json.Marshal(input)
	var result map[string]any
	_ = json.Unmarshal(b, &result)
	return result
}

func (p *protocol) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests++
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Amzn-Requestid", "fixture-request")
	if strings.HasSuffix(r.URL.Path, "/operation/GetMetricData") {
		p.metricInput, _ = io.ReadAll(r.Body)
		results := cbor.List{}
		for _, id := range []string{"invocations", "errors", "throttles", "duration"} {
			results = append(results, cbor.Map{"Id": cbor.String(id), "StatusCode": cbor.String("Complete"), "Timestamps": cbor.List{&cbor.Tag{ID: 1, Value: cbor.Uint(time.Now().Unix())}}, "Values": cbor.List{cbor.Float64(2)}})
		}
		w.Header().Set("Smithy-Protocol", "rpc-v2-cbor")
		w.Header().Set("Content-Type", "application/cbor")
		_, _ = w.Write(cbor.Encode(cbor.Map{"MetricDataResults": results}))
		return
	}
	if r.Header.Get("X-Amz-Target") == "Logs_20140328.DescribeLogStreams" {
		_ = json.NewEncoder(w).Encode(map[string]any{"logStreams": []map[string]string{{"logStreamName": "2026/10/03/[1]invocation"}, {"logStreamName": "2026/10/03/[2]other"}}})
		return
	}
	if r.Header.Get("X-Amz-Target") == "Logs_20140328.FilterLogEvents" {
		_ = json.NewDecoder(r.Body).Decode(&p.logInput)
		_ = json.NewEncoder(w).Encode(map[string]any{"events": []map[string]any{{"message": "version one output", "timestamp": time.Now().UnixMilli(), "eventId": "fixture-event"}}})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/2015-03-31/functions")
	if strings.HasPrefix(r.URL.Path, "/2017-03-31/tags/") {
		var input struct {
			Tags map[string]string `json:"Tags"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		arn := strings.TrimPrefix(r.URL.Path, "/2017-03-31/tags/")
		name := arn[strings.LastIndex(arn, ":")+1:]
		for key, value := range input.Tags {
			p.tags[name][key] = value
		}
		p.mutations = append(p.mutations, "tag:"+name)
		w.WriteHeader(204)
		return
	}
	if r.Method == "GET" && (path == "/" || path == "") {
		functions := []map[string]any{}
		for _, config := range p.functions {
			functions = append(functions, config)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Functions": functions})
		return
	}
	var input map[string]any
	if r.Method == "POST" || r.Method == "PUT" {
		_ = json.NewDecoder(r.Body).Decode(&input)
	}
	if r.Method == "POST" && (path == "/" || path == "") {
		name := input["FunctionName"].(string)
		input["FunctionArn"] = "arn:aws:lambda:us-east-1:000000000000:function:" + name
		input["State"] = "Active"
		input["LastUpdateStatus"] = "Successful"
		input["Version"] = "$LATEST"
		input["RevisionId"] = "native-revision"
		input["CodeSha256"] = "code-hash"
		input["ImageConfigResponse"] = map[string]any{"ImageConfig": input["ImageConfig"]}
		p.functions[name] = input
		p.images[name] = input["Code"].(map[string]any)["ImageUri"].(string)
		p.tags[name] = map[string]string{}
		for key, value := range input["Tags"].(map[string]any) {
			p.tags[name][key] = value.(string)
		}
		p.versions[name] = map[string]map[string]any{}
		p.versionImages[name] = map[string]string{}
		p.mutations = append(p.mutations, "create:"+name)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(input)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	name := parts[0]
	config, exists := p.functions[name]
	if !exists {
		w.Header().Set("X-Amzn-Errortype", "ResourceNotFoundException")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"message":"missing"}`))
		return
	}
	if len(parts) == 1 && r.Method == "GET" {
		selected, image := config, p.images[name]
		if version := r.URL.Query().Get("Qualifier"); version != "" {
			selected = p.versions[name][version]
			image = p.versionImages[name][version]
		}
		if p.pendingReads > 0 {
			p.pendingReads--
			selected = p.clone(selected)
			selected["State"] = "Pending"
			selected["LastUpdateStatus"] = "InProgress"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Configuration": selected, "Code": map[string]string{"ImageUri": image, "ResolvedImageUri": image}, "Tags": p.tags[name]})
		return
	}
	if len(parts) == 1 && r.Method == "DELETE" {
		delete(p.functions, name)
		p.mutations = append(p.mutations, "delete:"+name)
		w.WriteHeader(204)
		return
	}
	if len(parts) > 1 && parts[1] == "configuration" {
		for key, value := range input {
			config[key] = value
		}
		config["ImageConfigResponse"] = map[string]any{"ImageConfig": input["ImageConfig"]}
		if p.failUpdate {
			config["LastUpdateStatus"] = "Failed"
		}
		if p.ownershipLossAt == "configure" {
			p.tags[name]["dev.delma.heimdall.managed-by"] = "another-authority"
		}
		p.mutations = append(p.mutations, "configure:"+name)
		_ = json.NewEncoder(w).Encode(config)
		return
	}
	if len(parts) > 1 && parts[1] == "code" {
		p.images[name] = input["ImageUri"].(string)
		config["Architectures"] = input["Architectures"]
		if p.ownershipLossAt == "code" {
			p.tags[name]["dev.delma.heimdall.managed-by"] = "another-authority"
		}
		p.mutations = append(p.mutations, "code:"+name)
		_ = json.NewEncoder(w).Encode(config)
		return
	}
	if len(parts) > 1 && parts[1] == "versions" {
		if r.Method == "GET" {
			versions := []map[string]any{}
			for _, version := range p.versions[name] {
				versions = append(versions, version)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Versions": versions})
			return
		}
		if p.publicationConflict {
			w.Header().Set("X-Amzn-Errortype", "ResourceConflictException")
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"message":"unchanged publication"}`))
			return
		}
		version := fmt.Sprint(len(p.versions[name]) + 1)
		published := p.clone(config)
		published["Version"] = version
		p.versions[name][version] = published
		p.versionImages[name][version] = p.images[name]
		if p.ownershipLossAt == "publish" {
			p.tags[name]["dev.delma.heimdall.managed-by"] = "another-authority"
		}
		p.mutations = append(p.mutations, "publish:"+name)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(published)
		return
	}
	if len(parts) > 1 && parts[1] == "invocations" {
		p.invokeCalls++
		p.lastQualifier = r.URL.Query().Get("Qualifier")
		p.lastInput, _ = json.Marshal(input)
		if p.invokeFailure {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"message":"ambiguous transport failure"}`))
			return
		}
		w.Header().Set("X-Amz-Executed-Version", p.lastQualifier)
		if p.handlerFailure {
			w.Header().Set("X-Amz-Function-Error", "Unhandled")
		}
		if p.oversizedResponse {
			_, _ = w.Write([]byte(strings.Repeat("x", 6*1024*1024+1)))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	w.WriteHeader(400)
}
