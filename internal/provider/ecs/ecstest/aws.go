// Package ecstest is a fake AWS for the ECS adapter's tests: the ECS JSON 1.1
// surface, the CloudWatch Query/XML surface, and the CloudWatch Logs JSON
// surface, all on one listener. Like dockertest, it fakes at the HTTP
// boundary so the adapter's real marshalling, real signing, and real error
// paths are exercised — only the cloud is missing.
package ecstest

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/smithy-go/encoding/cbor"
)

// Service is one fake ECS service.
type Service struct {
	Arn            string
	Name           string
	Cluster        string
	TaskDefinition string
	Desired        int
	Running        int
	Status         string
	Tags           map[string]string
	TaskArns       []string
	// LoadBalancers records what CreateService attached: target group ARN,
	// container name, container port — the seam the infrastructure hands in.
	LoadBalancers []map[string]any
	// LaunchType and CapacityProviders record how the service asked to be
	// scheduled. They are mutually exclusive, and ECS refuses both.
	LaunchType        string
	CapacityProviders []string
}

// TaskDefinition is one registered revision.
type TaskDefinition struct {
	Arn        string
	Family     string
	Image      string
	Env        map[string]string
	LogGroup   string
	StreamName string
}

// AWS is the fake, exported state and all, in dockertest's style.
type AWS struct {
	Mu              sync.Mutex
	Services        map[string]*Service        // by name
	TaskDefinitions map[string]*TaskDefinition // by arn
	LogLines        map[string][]string        // by stream name
	nextID          int

	server *httptest.Server
	conns  atomic.Int64
}

func New() *AWS {
	fake := &AWS{
		Services:        map[string]*Service{},
		TaskDefinitions: map[string]*TaskDefinition{},
		LogLines:        map[string][]string{},
	}
	fake.server = httptest.NewUnstartedServer(http.HandlerFunc(fake.route))
	fake.server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			fake.conns.Add(1)
		}
	}
	fake.server.Start()
	return fake
}

// Connections is the number of TCP connections ever accepted — the
// regression signal for the connection-per-call transport leak.
func (f *AWS) Connections() int64 { return f.conns.Load() }

func (f *AWS) URL() string { return f.server.URL }
func (f *AWS) Close()      { f.server.Close() }

func (f *AWS) route(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))

	// The three protocols are distinguishable by headers: JSON 1.1 carries
	// X-Amz-Target; the Query protocol posts form-encoded Action=...
	if target := r.Header.Get("X-Amz-Target"); target != "" {
		operation := target[strings.LastIndex(target, ".")+1:]
		if strings.HasPrefix(target, "Logs_") {
			f.logsCall(w, operation, body)
			return
		}
		f.ecsCall(w, operation, body)
		return
	}
	// CloudWatch speaks Smithy RPC v2: CBOR bodies on
	// /service/<sdkId>/operation/<name>. Learned by running the SDK against
	// this fake, not from the docs — the docs still show the Query protocol.
	if strings.HasSuffix(r.URL.Path, "/operation/GetMetricData") {
		f.metricData(w)
		return
	}
	http.Error(w, "unrecognised call", http.StatusBadRequest)
}

func (f *AWS) writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	_ = json.NewEncoder(w).Encode(body)
}

func (f *AWS) errorJSON(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"__type": code, "message": message})
}

func (f *AWS) serviceJSON(service *Service) map[string]any {
	tags := []map[string]string{}
	for key, value := range service.Tags {
		tags = append(tags, map[string]string{"key": key, "value": value})
	}
	return map[string]any{
		"serviceArn": service.Arn, "serviceName": service.Name,
		"desiredCount": service.Desired, "runningCount": service.Running,
		"status": service.Status, "taskDefinition": service.TaskDefinition,
		"tags": tags,
		"events": []map[string]any{{
			"id": "ev-1", "createdAt": float64(time.Now().Add(-time.Minute).Unix()),
			"message": fmt.Sprintf("(service %s) has reached a steady state.", service.Name),
		}},
	}
}

func (f *AWS) ecsCall(w http.ResponseWriter, operation string, body []byte) {
	f.Mu.Lock()
	defer f.Mu.Unlock()

	var request map[string]any
	_ = json.Unmarshal(body, &request)
	str := func(key string) string { value, _ := request[key].(string); return value }

	switch operation {
	case "RegisterTaskDefinition":
		f.nextID++
		family := str("family")
		arn := fmt.Sprintf("arn:aws:ecs:us-east-1:000000000000:task-definition/%s:%d", family, f.nextID)
		definition := &TaskDefinition{Arn: arn, Family: family, Env: map[string]string{}}
		if containers, ok := request["containerDefinitions"].([]any); ok && len(containers) > 0 {
			container, _ := containers[0].(map[string]any)
			definition.Image, _ = container["image"].(string)
			if env, ok := container["environment"].([]any); ok {
				for _, pair := range env {
					entry, _ := pair.(map[string]any)
					name, _ := entry["name"].(string)
					value, _ := entry["value"].(string)
					definition.Env[name] = value
				}
			}
			if logConfig, ok := container["logConfiguration"].(map[string]any); ok {
				if options, ok := logConfig["options"].(map[string]any); ok {
					definition.LogGroup, _ = options["awslogs-group"].(string)
					definition.StreamName, _ = options["awslogs-stream-prefix"].(string)
				}
			}
		}
		f.TaskDefinitions[arn] = definition
		f.writeJSON(w, map[string]any{"taskDefinition": map[string]any{"taskDefinitionArn": arn}})

	case "CreateService":
		name := str("serviceName")
		if _, exists := f.Services[name]; exists {
			f.errorJSON(w, "InvalidParameterException", "service already exists: "+name)
			return
		}
		f.nextID++
		desired := 1
		if count, ok := request["desiredCount"].(float64); ok {
			desired = int(count)
		}
		tags := map[string]string{}
		if raw, ok := request["tags"].([]any); ok {
			for _, pair := range raw {
				entry, _ := pair.(map[string]any)
				key, _ := entry["key"].(string)
				value, _ := entry["value"].(string)
				tags[key] = value
			}
		}
		service := &Service{
			Arn:  fmt.Sprintf("arn:aws:ecs:us-east-1:000000000000:service/%s/%s", str("cluster"), name),
			Name: name, Cluster: str("cluster"), TaskDefinition: str("taskDefinition"),
			Desired: desired, Running: desired, Status: "ACTIVE", Tags: tags,
		}
		service.LaunchType, _ = request["launchType"].(string)
		if raw, ok := request["capacityProviderStrategy"].([]any); ok {
			for _, entry := range raw {
				if item, ok := entry.(map[string]any); ok {
					name, _ := item["capacityProvider"].(string)
					service.CapacityProviders = append(service.CapacityProviders, name)
				}
			}
		}
		if raw, ok := request["loadBalancers"].([]any); ok {
			for _, entry := range raw {
				if balancer, ok := entry.(map[string]any); ok {
					service.LoadBalancers = append(service.LoadBalancers, balancer)
				}
			}
		}
		// The fake scheduler runs everything immediately.
		for i := 0; i < desired; i++ {
			f.nextID++
			service.TaskArns = append(service.TaskArns,
				fmt.Sprintf("arn:aws:ecs:us-east-1:000000000000:task/%s/task%08d", str("cluster"), f.nextID))
		}
		f.Services[name] = service
		f.writeJSON(w, map[string]any{"service": f.serviceJSON(service)})

	case "UpdateService":
		service, ok := f.Services[str("service")]
		if !ok {
			f.errorJSON(w, "ServiceNotFoundException", "no such service")
			return
		}
		if definition := str("taskDefinition"); definition != "" {
			service.TaskDefinition = definition
		}
		if count, ok := request["desiredCount"].(float64); ok {
			service.Desired = int(count)
			service.Running = int(count)
		}
		f.writeJSON(w, map[string]any{"service": f.serviceJSON(service)})

	case "DeleteService":
		name := str("service")
		// The adapter passes the bare name; consoles pass ARNs. Accept both.
		for key, service := range f.Services {
			if key == name || service.Arn == name {
				delete(f.Services, key)
				f.writeJSON(w, map[string]any{"service": f.serviceJSON(service)})
				return
			}
		}
		f.errorJSON(w, "ServiceNotFoundException", "no such service")

	case "TagResource":
		for _, service := range f.Services {
			if service.Arn != str("resourceArn") {
				continue
			}
			if raw, ok := request["tags"].([]any); ok {
				for _, pair := range raw {
					entry, _ := pair.(map[string]any)
					key, _ := entry["key"].(string)
					value, _ := entry["value"].(string)
					service.Tags[key] = value
				}
			}
		}
		f.writeJSON(w, map[string]any{})

	case "ListServices":
		arns := []string{}
		for _, service := range f.Services {
			if service.Cluster == str("cluster") {
				arns = append(arns, service.Arn)
			}
		}
		f.writeJSON(w, map[string]any{"serviceArns": arns})

	case "DescribeServices":
		out := []map[string]any{}
		wanted, _ := request["services"].([]any)
		for _, raw := range wanted {
			arn, _ := raw.(string)
			for _, service := range f.Services {
				if service.Arn == arn || service.Name == arn {
					out = append(out, f.serviceJSON(service))
				}
			}
		}
		f.writeJSON(w, map[string]any{"services": out})

	case "ListTasks":
		service, ok := f.Services[str("serviceName")]
		if !ok {
			f.writeJSON(w, map[string]any{"taskArns": []string{}})
			return
		}
		f.writeJSON(w, map[string]any{"taskArns": service.TaskArns})

	case "DescribeTasks":
		out := []map[string]any{}
		wanted, _ := request["tasks"].([]any)
		for _, raw := range wanted {
			arn, _ := raw.(string)
			for _, service := range f.Services {
				for _, taskArn := range service.TaskArns {
					if taskArn != arn {
						continue
					}
					definition := f.TaskDefinitions[service.TaskDefinition]
					image := ""
					if definition != nil {
						image = definition.Image
					}
					out = append(out, map[string]any{
						"taskArn": arn, "lastStatus": "RUNNING",
						"startedAt":  float64(time.Now().Add(-time.Hour).Unix()),
						"containers": []map[string]any{{"image": image}},
					})
				}
			}
		}
		f.writeJSON(w, map[string]any{"tasks": out})

	default:
		f.errorJSON(w, "UnknownOperationException", operation)
	}
}

func (f *AWS) logsCall(w http.ResponseWriter, operation string, body []byte) {
	f.Mu.Lock()
	defer f.Mu.Unlock()

	var request struct {
		LogStreamName string `json:"logStreamName"`
	}
	_ = json.Unmarshal(body, &request)

	if operation != "GetLogEvents" {
		f.errorJSON(w, "UnknownOperationException", operation)
		return
	}
	lines := f.LogLines[request.LogStreamName]
	events := make([]map[string]any, 0, len(lines))
	for i, line := range lines {
		events = append(events, map[string]any{
			"timestamp": time.Now().Add(-time.Minute).UnixMilli() + int64(i),
			"message":   line,
		})
	}
	f.writeJSON(w, map[string]any{"events": events})
}

// metricData answers GetMetricData with one hour of minute samples for both
// queried series, in the RPC v2 CBOR encoding the SDK actually speaks.
func (f *AWS) metricData(w http.ResponseWriter) {
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)

	results := cbor.List{}
	for _, id := range []string{"cpu", "memory"} {
		timestamps := cbor.List{}
		values := cbor.List{}
		for i := 0; i < 60; i++ {
			at := base.Add(time.Duration(i) * time.Minute)
			// Tag 1: epoch timestamp, the encoding AsTime accepts.
			timestamps = append(timestamps, &cbor.Tag{ID: 1, Value: cbor.Uint(at.Unix())})
			values = append(values, cbor.Float64(float64(10+i%20)+0.5))
		}
		results = append(results, cbor.Map{
			"Id": cbor.String(id), "StatusCode": cbor.String("Complete"),
			"Timestamps": timestamps, "Values": values,
		})
	}
	w.Header().Set("Smithy-Protocol", "rpc-v2-cbor")
	w.Header().Set("Content-Type", "application/cbor")
	_, _ = w.Write(cbor.Encode(cbor.Map{"MetricDataResults": results}))
}
