// Package dockertest is a stand-in for the Docker Engine's HTTP API.
//
// It is a test double at a real external boundary, not a mock of HEIMDALL's
// own code: the adapter's request building, label filtering, stream framing,
// and error handling all run for real against it. A live Engine is the better
// test and scripts/e2e-docker.sh uses one; this exists so the adapter and the
// reconcile slice stay covered on a machine with no Docker daemon.
//
// It lives in its own package so both the adapter's tests and the reconcile
// tests can drive it without duplicating three hundred lines.
package dockertest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Engine is the fake. Its zero value is not usable; call New.
type Engine struct {
	server *httptest.Server

	// Mu guards Containers for tests that inspect it directly.
	Mu         sync.Mutex
	Containers map[string]*Container
	// Services holds the Swarm fakes; nil until a Swarm test creates one.
	Services     map[string]*Service
	SwarmSecrets map[string]*SwarmSecret
	Volumes      map[string]bool
	Pulled       []string
	// auths records the credential each pull carried, so a test can assert a
	// private image was authenticated and a public one was not.
	auths  map[string]*Auth
	nextID int
	// FailPull makes a pull of any image containing this substring fail,
	// exercising the in-stream error path the Engine actually uses.
	FailPull string
	conns    atomic.Int64
}

// Container is one container the fake is holding.
type Container struct {
	ID      string
	Name    string
	Image   string
	Labels  map[string]string
	Env     []string
	Binds   []string
	Running bool
	Health  string
	Created int64
	Started time.Time
}

// New starts a fake Engine on a local HTTP listener.
func New() *Engine {
	engine := &Engine{
		Containers: map[string]*Container{},
		Volumes:    map[string]bool{},
		auths:      map[string]*Auth{},
	}
	engine.server = httptest.NewUnstartedServer(http.HandlerFunc(engine.route))
	engine.server.Config.ConnState = engine.countConn
	engine.server.Start()
	return engine
}

// countConn tallies accepted TCP connections. An adapter that reuses its
// transport holds this near one across any number of calls; the
// connection-per-poll leak this exists to catch counts one per call.
func (e *Engine) countConn(_ net.Conn, state http.ConnState) {
	if state == http.StateNew {
		e.conns.Add(1)
	}
}

// Connections is the number of TCP connections ever accepted.
func (e *Engine) Connections() int64 { return e.conns.Load() }

// NewAt starts a fake Engine on a fixed address, so a demo target registered
// against it survives the fake being restarted.
func NewAt(addr string) (*Engine, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	engine := &Engine{
		Containers: map[string]*Container{},
		Volumes:    map[string]bool{},
		auths:      map[string]*Auth{},
	}
	engine.server = &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: http.HandlerFunc(engine.route), ConnState: engine.countConn},
	}
	engine.server.Start()
	return engine, nil
}

func (f *Engine) URL() string { return f.server.URL }
func (f *Engine) Close()      { f.server.Close() }

// Reset empties the engine between test cases.
func (f *Engine) Reset() {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Containers = map[string]*Container{}
	f.Volumes = map[string]bool{}
	f.auths = map[string]*Auth{}
	f.Pulled = nil
	f.FailPull = ""
}

// removeByService deletes a container out of band, the way `docker rm -f`
// would. It is how the self-heal and drift tests create real drift.
// RemoveByService deletes a container out of band, the way `docker rm -f`
// would. It is how drift and self-heal tests create real drift.
func (f *Engine) RemoveByService(service string) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for id, container := range f.Containers {
		if container.Labels["dev.delma.heimdall.service"] == service {
			delete(f.Containers, id)
		}
	}
}

// StopByService stops a container out of band.
func (f *Engine) StopByService(service string) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, container := range f.Containers {
		if container.Labels["dev.delma.heimdall.service"] == service {
			container.Running = false
		}
	}
}

// Inject adds a container the fake did not create — a workload belonging to
// someone else. HEIMDALL must never see it or touch it.
func (f *Engine) Inject(container *Container) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if container.ID == "" {
		f.nextID++
		container.ID = fmt.Sprintf("injected%08d", f.nextID)
	}
	f.Containers[container.ID] = container
}

// Get returns one container by id.
func (f *Engine) Get(id string) (*Container, bool) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	container, ok := f.Containers[id]
	return container, ok
}

// Count is how many containers exist.
func (f *Engine) Count() int {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return len(f.Containers)
}

func (f *Engine) route(w http.ResponseWriter, r *http.Request) {
	// Strip the pinned API version prefix the client sends.
	path := r.URL.Path
	if index := strings.Index(path, "/containers"); index > 0 {
		path = path[index:]
	} else if index := strings.Index(path, "/images"); index > 0 {
		path = path[index:]
	} else if index := strings.Index(path, "/volumes"); index > 0 {
		path = path[index:]
	} else if index := strings.Index(path, "/version"); index > 0 {
		path = path[index:]
	} else if index := strings.Index(path, "/events"); index > 0 {
		path = path[index:]
	}

	if f.swarmRoute(w, r) {
		return
	}

	switch {
	case path == "/version":
		f.writeJSON(w, map[string]string{"Version": "27.3.1", "ApiVersion": "1.47", "Os": "linux", "Arch": "arm64"})
	case path == "/containers/json":
		f.listContainers(w, r)
	case path == "/containers/create":
		f.createContainer(w, r)
	case path == "/images/create":
		f.pullImage(w, r)
	case path == "/volumes/create":
		f.createVolume(w, r)
	case path == "/events":
		f.writeRaw(w, "")
	case strings.HasSuffix(path, "/json") && strings.HasPrefix(path, "/containers/"):
		f.inspectContainer(w, strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json"))
	case strings.HasSuffix(path, "/start"):
		f.setRunning(w, containerID(path, "/start"), true)
	case strings.HasSuffix(path, "/stop"):
		f.setRunning(w, containerID(path, "/stop"), false)
	case strings.HasSuffix(path, "/stats"):
		f.stats(w, containerID(path, "/stats"))
	case strings.HasSuffix(path, "/logs"):
		f.logs(w, containerID(path, "/logs"))
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/containers/"):
		f.deleteContainer(w, strings.TrimPrefix(path, "/containers/"))
	default:
		f.writeError(w, http.StatusNotFound, "no such endpoint: "+path)
	}
}

func containerID(path, suffix string) string {
	return strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), suffix)
}

func (f *Engine) listContainers(w http.ResponseWriter, r *http.Request) {
	f.Mu.Lock()
	defer f.Mu.Unlock()

	// Honour the label filter exactly as the Engine does: every requested
	// label must match. Getting this wrong in the fake would hide a real bug
	// where the adapter touches containers it does not own.
	var filters map[string][]string
	if raw := r.URL.Query().Get("filters"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &filters)
	}

	summaries := []map[string]any{}
	for _, container := range f.Containers {
		if !matchesLabels(container.Labels, filters["label"]) {
			continue
		}
		state := "exited"
		if container.Running {
			state = "running"
		}
		summaries = append(summaries, map[string]any{
			"Id": container.ID, "Names": []string{"/" + container.Name},
			"Image": container.Image, "State": state, "Status": state,
			"Labels": container.Labels, "Created": container.Created,
		})
	}
	f.writeJSON(w, summaries)
}

func matchesLabels(labels map[string]string, required []string) bool {
	for _, requirement := range required {
		key, value, found := strings.Cut(requirement, "=")
		if !found {
			if _, present := labels[key]; !present {
				return false
			}
			continue
		}
		if labels[key] != value {
			return false
		}
	}
	return true
}

func (f *Engine) createContainer(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Image      string            `json:"Image"`
		Env        []string          `json:"Env"`
		Labels     map[string]string `json:"Labels"`
		HostConfig struct {
			Binds []string `json:"Binds"`
		} `json:"HostConfig"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		f.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	f.Mu.Lock()
	defer f.Mu.Unlock()

	name := r.URL.Query().Get("name")
	for _, container := range f.Containers {
		if container.Name == name {
			f.writeError(w, http.StatusConflict, "container name "+name+" is already in use")
			return
		}
	}

	f.nextID++
	id := fmt.Sprintf("c%08d", f.nextID)
	f.Containers[id] = &Container{
		ID: id, Name: name, Image: request.Image, Labels: request.Labels,
		Env: request.Env, Binds: request.HostConfig.Binds,
		Created: time.Now().UnixNano(), Health: "healthy",
	}
	f.writeJSON(w, map[string]any{"Id": id, "Warnings": []string{}})
}

func (f *Engine) inspectContainer(w http.ResponseWriter, id string) {
	f.Mu.Lock()
	defer f.Mu.Unlock()

	container, ok := f.Containers[id]
	if !ok {
		f.writeError(w, http.StatusNotFound, "no such container: "+id)
		return
	}
	status := "exited"
	if container.Running {
		status = "running"
	}
	f.writeJSON(w, map[string]any{
		"Id": container.ID, "Name": "/" + container.Name,
		"State": map[string]any{
			"Status": status, "Running": container.Running, "ExitCode": 0,
			"StartedAt": container.Started,
			"Health":    map[string]any{"Status": container.Health, "FailingStreak": 0},
		},
		"RestartCount": 0,
		"Config":       map[string]any{"Image": container.Image, "Labels": container.Labels, "Env": container.Env},
		"HostConfig":   map[string]any{"Memory": 0, "NanoCpus": 0},
	})
}

func (f *Engine) setRunning(w http.ResponseWriter, id string, running bool) {
	f.Mu.Lock()
	defer f.Mu.Unlock()

	container, ok := f.Containers[id]
	if !ok {
		f.writeError(w, http.StatusNotFound, "no such container: "+id)
		return
	}
	container.Running = running
	if running {
		container.Started = time.Now().UTC()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (f *Engine) deleteContainer(w http.ResponseWriter, id string) {
	f.Mu.Lock()
	defer f.Mu.Unlock()

	if _, ok := f.Containers[id]; !ok {
		f.writeError(w, http.StatusNotFound, "no such container: "+id)
		return
	}
	delete(f.Containers, id)
	w.WriteHeader(http.StatusNoContent)
}

// pullImage reproduces the Engine's most surprising behaviour: a failed pull
// is reported as an error object *inside* a 200 stream, not as a status code.
func (f *Engine) pullImage(w http.ResponseWriter, r *http.Request) {
	image := r.URL.Query().Get("fromImage") + ":" + r.URL.Query().Get("tag")

	var credential *Auth
	if header := r.Header.Get("X-Registry-Auth"); header != "" {
		decoded, err := base64.URLEncoding.DecodeString(header)
		if err != nil {
			f.writeError(w, http.StatusBadRequest, "X-Registry-Auth is not valid base64url")
			return
		}
		credential = &Auth{}
		if err := json.Unmarshal(decoded, credential); err != nil {
			f.writeError(w, http.StatusBadRequest, "X-Registry-Auth is not valid JSON")
			return
		}
	}

	f.Mu.Lock()
	shouldFail := f.FailPull != "" && strings.Contains(image, f.FailPull)
	f.Pulled = append(f.Pulled, image)
	f.auths[image] = credential
	f.Mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if shouldFail {
		_, _ = fmt.Fprintf(w, "{\"status\":\"Pulling from %s\"}\n{\"error\":\"manifest unknown\"}\n", image)
		return
	}
	_, _ = fmt.Fprintf(w, "{\"status\":\"Pulling from %s\"}\n{\"status\":\"Download complete\"}\n", image)
}

func (f *Engine) createVolume(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name string `json:"Name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&request)

	f.Mu.Lock()
	f.Volumes[request.Name] = true
	f.Mu.Unlock()

	f.writeJSON(w, map[string]any{"Name": request.Name})
}

func (f *Engine) stats(w http.ResponseWriter, id string) {
	f.Mu.Lock()
	_, ok := f.Containers[id]
	f.Mu.Unlock()
	if !ok {
		f.writeError(w, http.StatusNotFound, "no such container: "+id)
		return
	}
	f.writeJSON(w, map[string]any{
		"read": time.Now().UTC(),
		"cpu_stats": map[string]any{
			"cpu_usage":        map[string]any{"total_usage": 200_000_000, "percpu_usage": []int{1, 2}},
			"system_cpu_usage": 2_000_000_000, "online_cpus": 2,
			"throttling_data": map[string]any{"throttled_periods": 7},
		},
		"precpu_stats": map[string]any{
			"cpu_usage":        map[string]any{"total_usage": 100_000_000},
			"system_cpu_usage": 1_000_000_000,
		},
		"memory_stats": map[string]any{
			"usage": 200 << 20, "limit": 512 << 20,
			"stats": map[string]any{"inactive_file": 50 << 20},
		},
		"networks": map[string]any{"eth0": map[string]any{
			"rx_bytes": 1024, "tx_bytes": 2048, "rx_errors": 1, "rx_dropped": 2, "tx_errors": 0, "tx_dropped": 0,
		}},
		"pids_stats": map[string]any{"current": 12},
		"blkio_stats": map[string]any{"io_service_bytes_recursive": []map[string]any{
			{"op": "Read", "value": 4096}, {"op": "Write", "value": 8192},
		}},
	})
}

// logs writes a multiplexed stream with the Engine's 8-byte frame headers, so
// the adapter's demuxer is exercised rather than assumed.
func (f *Engine) logs(w http.ResponseWriter, id string) {
	f.Mu.Lock()
	_, ok := f.Containers[id]
	f.Mu.Unlock()
	if !ok {
		f.writeError(w, http.StatusNotFound, "no such container: "+id)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
	w.WriteHeader(http.StatusOK)
	for _, line := range []string{"listening on :8000\n", "ready\n"} {
		header := []byte{1, 0, 0, 0, 0, 0, 0, 0}
		header[4] = byte(len(line) >> 24)
		header[5] = byte(len(line) >> 16)
		header[6] = byte(len(line) >> 8)
		header[7] = byte(len(line))
		_, _ = w.Write(header)
		_, _ = w.Write([]byte(line))
	}
}

func (f *Engine) writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (f *Engine) writeRaw(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func (f *Engine) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}

// Auth is the credential a pull carried, if any.
type Auth struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	ServerAddress string `json:"serveraddress"`
}

// AuthFor returns the credential used to pull an image, or nil if the pull
// was anonymous. It is how a test proves a private image was authenticated
// and a public one was not.
func (f *Engine) AuthFor(image string) *Auth {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	name, tag := image, "latest"
	if index := strings.LastIndex(image, ":"); index > strings.LastIndex(image, "/") {
		name, tag = image[:index], image[index+1:]
	}
	return f.auths[name+":"+tag]
}

// --- Swarm fakes -----------------------------------------------------------

// Service is one Swarm service the fake holds, with the tasks the fake
// scheduler "placed" for it.
type Service struct {
	ID      string
	Version uint64
	Spec    map[string]any
	Tasks   []Task
}

// Task is one fake scheduled unit.
type Task struct {
	ID     string
	NodeID string
	State  string
}

func (f *Engine) swarmRoute(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	for _, family := range []string{"/services", "/tasks", "/secrets"} {
		if index := strings.Index(path, family); index > 0 {
			path = path[index:]
		}
	}

	switch {
	case path == "/services" && r.Method == http.MethodGet:
		f.listServices(w, r)
	case path == "/services/create":
		f.createService(w, r)
	case strings.HasSuffix(path, "/update") && strings.HasPrefix(path, "/services/"):
		f.updateService(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/services/"), "/update"))
	case strings.HasSuffix(path, "/logs") && strings.HasPrefix(path, "/services/"):
		f.serviceLogs(w, strings.TrimSuffix(strings.TrimPrefix(path, "/services/"), "/logs"))
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/services/"):
		f.deleteService(w, strings.TrimPrefix(path, "/services/"))
	case path == "/tasks" && r.Method == http.MethodGet:
		f.listTasks(w, r)
	case path == "/secrets" && r.Method == http.MethodGet:
		f.listSwarmSecrets(w, r)
	case path == "/secrets/create":
		f.createSwarmSecret(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/secrets/"):
		f.deleteSwarmSecret(w, strings.TrimPrefix(path, "/secrets/"))
	default:
		return false
	}
	return true
}

// SwarmSecret is one fake Swarm secret. Data is kept so a test can assert
// what was stored; the list answer withholds it exactly as the Engine does.
type SwarmSecret struct {
	ID     string
	Name   string
	Labels map[string]string
	Data   string
}

func (f *Engine) listSwarmSecrets(w http.ResponseWriter, r *http.Request) {
	filters := parseFilters(r)
	f.Mu.Lock()
	defer f.Mu.Unlock()
	out := []map[string]any{}
	for _, secret := range f.SwarmSecrets {
		if !matchesLabels(secret.Labels, filters["label"]) {
			continue
		}
		out = append(out, map[string]any{
			"ID":   secret.ID,
			"Spec": map[string]any{"Name": secret.Name, "Labels": secret.Labels},
		})
	}
	f.writeJSON(w, out)
}

func (f *Engine) createSwarmSecret(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name   string            `json:"Name"`
		Labels map[string]string `json:"Labels"`
		Data   string            `json:"Data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		f.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.SwarmSecrets == nil {
		f.SwarmSecrets = map[string]*SwarmSecret{}
	}
	for _, secret := range f.SwarmSecrets {
		if secret.Name == request.Name {
			f.writeError(w, http.StatusConflict, "secret name conflicts with an existing object")
			return
		}
	}
	f.nextID++
	id := fmt.Sprintf("sec%d", f.nextID)
	f.SwarmSecrets[id] = &SwarmSecret{ID: id, Name: request.Name, Labels: request.Labels, Data: request.Data}
	w.WriteHeader(http.StatusCreated)
	f.writeJSON(w, map[string]any{"ID": id})
}

// deleteSwarmSecret refuses while a service references the secret, exactly
// as the Engine does — the safety the prune path leans on.
func (f *Engine) deleteSwarmSecret(w http.ResponseWriter, id string) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, service := range f.Services {
		if template, ok := service.Spec["TaskTemplate"].(map[string]any); ok {
			if container, ok := template["ContainerSpec"].(map[string]any); ok {
				if secrets, ok := container["Secrets"].([]any); ok {
					for _, raw := range secrets {
						if reference, ok := raw.(map[string]any); ok {
							if referenced, _ := reference["SecretID"].(string); referenced == id {
								f.writeError(w, http.StatusBadRequest, "rpc error: secret is in use")
								return
							}
						}
					}
				}
			}
		}
	}
	delete(f.SwarmSecrets, id)
	w.WriteHeader(http.StatusNoContent)
}

func parseFilters(r *http.Request) map[string][]string {
	var filters map[string][]string
	if raw := r.URL.Query().Get("filters"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &filters)
	}
	return filters
}

func writeFramedLogs(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
	w.WriteHeader(http.StatusOK)
	header := []byte{1, 0, 0, 0, 0, 0, 0, 0}
	header[4] = byte(len(text) >> 24)
	header[5] = byte(len(text) >> 16)
	header[6] = byte(len(text) >> 8)
	header[7] = byte(len(text))
	_, _ = w.Write(header)
	_, _ = w.Write([]byte(text))
}

func serviceLabels(spec map[string]any) map[string]string {
	labels := map[string]string{}
	if raw, ok := spec["Labels"].(map[string]any); ok {
		for key, value := range raw {
			labels[key], _ = value.(string)
		}
	}
	return labels
}

func (f *Engine) listServices(w http.ResponseWriter, r *http.Request) {
	filters := parseFilters(r)
	f.Mu.Lock()
	defer f.Mu.Unlock()

	out := []map[string]any{}
	for _, service := range f.Services {
		if !matchesLabels(serviceLabels(service.Spec), filters["label"]) {
			continue
		}
		out = append(out, map[string]any{
			"ID": service.ID, "Version": map[string]any{"Index": service.Version},
			"Spec": service.Spec,
		})
	}
	f.writeJSON(w, out)
}

func (f *Engine) createService(w http.ResponseWriter, r *http.Request) {
	var spec map[string]any
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		f.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Services == nil {
		f.Services = map[string]*Service{}
	}
	f.nextID++
	id := fmt.Sprintf("srv%d", f.nextID)

	// The fake scheduler places every replica immediately, running.
	replicas := 1
	if mode, ok := spec["Mode"].(map[string]any); ok {
		if replicated, ok := mode["Replicated"].(map[string]any); ok {
			if count, ok := replicated["Replicas"].(float64); ok {
				replicas = int(count)
			}
		}
	}
	service := &Service{ID: id, Version: 1, Spec: spec}
	for i := 0; i < replicas; i++ {
		service.Tasks = append(service.Tasks, Task{
			ID: fmt.Sprintf("%s-task%d", id, i), NodeID: fmt.Sprintf("node%d", i%3), State: "running",
		})
	}
	f.Services[id] = service
	w.WriteHeader(http.StatusCreated)
	f.writeJSON(w, map[string]string{"ID": id})
}

func (f *Engine) updateService(w http.ResponseWriter, r *http.Request, id string) {
	var spec map[string]any
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		f.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	service, ok := f.Services[id]
	if !ok {
		f.writeError(w, http.StatusNotFound, "no such service: "+id)
		return
	}
	// Optimistic concurrency, the same rule the real Engine enforces.
	if r.URL.Query().Get("version") != fmt.Sprint(service.Version) {
		f.writeError(w, http.StatusConflict, "update out of sequence")
		return
	}
	service.Spec = spec
	service.Version++
	f.writeJSON(w, map[string]any{})
}

func (f *Engine) deleteService(w http.ResponseWriter, id string) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if _, ok := f.Services[id]; !ok {
		f.writeError(w, http.StatusNotFound, "no such service: "+id)
		return
	}
	delete(f.Services, id)
	f.writeJSON(w, map[string]any{})
}

func (f *Engine) listTasks(w http.ResponseWriter, r *http.Request) {
	filters := parseFilters(r)
	f.Mu.Lock()
	defer f.Mu.Unlock()

	wanted := map[string]bool{}
	for _, id := range filters["service"] {
		wanted[id] = true
	}
	out := []map[string]any{}
	for _, service := range f.Services {
		if len(wanted) > 0 && !wanted[service.ID] {
			continue
		}
		for _, task := range service.Tasks {
			out = append(out, map[string]any{
				"ID": task.ID, "ServiceID": service.ID, "NodeID": task.NodeID,
				"CreatedAt": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
				"Status":    map[string]any{"State": task.State},
			})
		}
	}
	f.writeJSON(w, out)
}

func (f *Engine) serviceLogs(w http.ResponseWriter, id string) {
	f.Mu.Lock()
	_, ok := f.Services[id]
	f.Mu.Unlock()
	if !ok {
		f.writeError(w, http.StatusNotFound, "no such service: "+id)
		return
	}
	writeFramedLogs(w, "service log line one\nservice log line two\n")
}
