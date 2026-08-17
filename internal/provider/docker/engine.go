// Package docker is the Docker Engine adapter.
//
// It speaks the Engine's HTTP API over a unix socket with net/http and
// nothing else. The Docker SDK would pull a large dependency tree to wrap
// eight endpoints, and Docker's own compose→ECS/ACI integration — deprecated
// in 2023 — is never used for anything.
package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// apiVersion is pinned. An Engine older than this is refused at Ping rather
// than failing later on a field that quietly changed shape. 1.44 is Docker
// Engine 25 (January 2024); modern engines have begun refusing anything
// older as a *client* minimum, which is how the first live agent found the
// previous pin.
const apiVersion = "v1.44"

// maxBodyBytes bounds a single Engine response. The Engine is trusted, but a
// wedged one returning an endless body must be a timeout, not an OOM.
const maxBodyBytes = 32 << 20

// engine is a minimal Docker Engine API client.
type engine struct {
	client *http.Client
	// base is the scheme+host the transport expects. For a unix socket it is
	// a placeholder host; the dialer ignores it.
	base string
}

// newEngine connects to a Docker Engine. endpoint is a unix socket path, a
// unix:// URL, or a tcp:// address.
func newEngine(endpoint string, timeout time.Duration) (*engine, error) {
	if endpoint == "" {
		endpoint = "unix:///var/run/docker.sock"
	}
	if strings.HasPrefix(endpoint, "/") {
		endpoint = "unix://" + endpoint
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("HD0310: parse docker endpoint %q: %w", endpoint, err)
	}

	transport := &http.Transport{}
	base := ""
	switch parsed.Scheme {
	case "unix":
		socket := parsed.Path
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}
		// The host is never resolved; it exists only to make a valid URL.
		base = "http://docker"
	case "tcp", "http":
		base = "http://" + parsed.Host
	case "https":
		base = "https://" + parsed.Host
	default:
		return nil, fmt.Errorf("HD0310: unsupported docker endpoint scheme %q", parsed.Scheme)
	}

	return &engine{
		base:   base,
		client: &http.Client{Transport: transport, Timeout: timeout},
	}, nil
}

// do issues one Engine request. Non-2xx responses become typed errors
// carrying the Engine's own message, which is almost always the actionable
// one.
func (e *engine) do(ctx context.Context, method, path string, query url.Values, body any) (*http.Response, error) {
	return e.doWithHeader(ctx, method, path, query, body, "", "")
}

func (e *engine) doWithHeader(
	ctx context.Context, method, path string, query url.Values, body any, headerName, headerValue string,
) (*http.Response, error) {
	target := e.base + "/" + apiVersion + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("HD0311: encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, fmt.Errorf("HD0311: build request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if headerName != "" {
		request.Header.Set(headerName, headerValue)
	}

	response, err := e.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("HD0312: docker engine unreachable at %s: %w", e.base, err)
	}
	if response.StatusCode >= 400 {
		defer func() { _ = response.Body.Close() }()
		var message struct {
			Message string `json:"message"`
		}
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		_ = json.Unmarshal(raw, &message)
		if message.Message == "" {
			message.Message = strings.TrimSpace(string(raw))
		}
		return nil, &engineError{Status: response.StatusCode, Message: message.Message, Path: path}
	}
	return response, nil
}

// registryAuth is the Engine's X-Registry-Auth payload.
type registryAuth struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	ServerAddress string `json:"serveraddress"`
}

// header renders the credential the way the Engine expects: base64url of the
// JSON object, with no padding stripped.
func (a *registryAuth) header() (string, error) {
	encoded, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("HD0316: encode registry credential: %w", err)
	}
	return base64.URLEncoding.EncodeToString(encoded), nil
}

// doWithAuth is do plus an optional registry credential. The credential is
// never logged and never stored; it exists for the duration of one request.
func (e *engine) doWithAuth(
	ctx context.Context, method, path string, query url.Values, body any, credential *registryAuth,
) (*http.Response, error) {
	if credential == nil {
		return e.do(ctx, method, path, query, body)
	}
	header, err := credential.header()
	if err != nil {
		return nil, err
	}
	return e.doWithHeader(ctx, method, path, query, body, "X-Registry-Auth", header)
}

// engineError is the Engine's refusal, typed so callers can branch on 404
// without parsing text.
type engineError struct {
	Status  int
	Message string
	Path    string
}

func (e *engineError) Error() string {
	return fmt.Sprintf("HD0313: docker engine %d on %s: %s", e.Status, e.Path, e.Message)
}

func (e *engineError) notFound() bool { return e.Status == http.StatusNotFound }

// isNotFound reports whether err is the Engine saying a resource is absent,
// which for a reconciler usually means "nothing to do" rather than "failed".
func isNotFound(err error) bool {
	var engineErr *engineError
	if ok := asEngineError(err, &engineErr); !ok {
		return false
	}
	return engineErr.notFound()
}

func asEngineError(err error, target **engineError) bool {
	return errors.As(err, target)
}

// decode reads a JSON response body into out.
func (e *engine) decode(ctx context.Context, method, path string, query url.Values, body, out any) error {
	response, err := e.do(ctx, method, path, query, body)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxBodyBytes))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxBodyBytes)).Decode(out); err != nil {
		return fmt.Errorf("HD0314: decode %s response: %w", path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// The eight endpoints the adapter needs.
// ---------------------------------------------------------------------------

type engineVersion struct {
	Version    string `json:"Version"`
	APIVersion string `json:"ApiVersion"`
	OS         string `json:"Os"`
	Arch       string `json:"Arch"`
}

func (e *engine) version(ctx context.Context) (engineVersion, error) {
	var out engineVersion
	err := e.decode(ctx, http.MethodGet, "/version", nil, nil, &out)
	return out, err
}

type containerSummary struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Labels  map[string]string `json:"Labels"`
	Created int64             `json:"Created"`
}

func (e *engine) listContainers(ctx context.Context, filters map[string][]string) ([]containerSummary, error) {
	query := url.Values{"all": {"true"}}
	if len(filters) > 0 {
		encoded, err := json.Marshal(filters)
		if err != nil {
			return nil, fmt.Errorf("HD0311: encode filters: %w", err)
		}
		query.Set("filters", string(encoded))
	}
	var out []containerSummary
	err := e.decode(ctx, http.MethodGet, "/containers/json", query, nil, &out)
	return out, err
}

type containerInspect struct {
	ID      string    `json:"Id"`
	Name    string    `json:"Name"`
	Created time.Time `json:"Created"`
	State   struct {
		Status     string    `json:"Status"`
		Running    bool      `json:"Running"`
		ExitCode   int       `json:"ExitCode"`
		StartedAt  time.Time `json:"StartedAt"`
		FinishedAt time.Time `json:"FinishedAt"`
		Health     *struct {
			Status        string `json:"Status"`
			FailingStreak int    `json:"FailingStreak"`
		} `json:"Health"`
	} `json:"State"`
	RestartCount int `json:"RestartCount"`
	Config       struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
		Env    []string          `json:"Env"`
	} `json:"Config"`
	HostConfig struct {
		Memory   int64 `json:"Memory"`
		NanoCPUs int64 `json:"NanoCpus"`
	} `json:"HostConfig"`
}

func (e *engine) inspectContainer(ctx context.Context, id string) (containerInspect, error) {
	var out containerInspect
	err := e.decode(ctx, http.MethodGet, "/containers/"+id+"/json", nil, nil, &out)
	return out, err
}

// pullImage streams the pull to completion. The Engine reports progress as a
// JSON stream and an error *inside* that stream rather than as a status code,
// so the stream is read to the end and inspected — returning early would
// report a failed pull as a success.
//
// credential is nil for a public image. When set it becomes an
// X-Registry-Auth header, which is the only place a registry password exists
// in this process.
func (e *engine) pullImage(ctx context.Context, image string, credential *registryAuth) error {
	name, tag := splitImage(image)
	query := url.Values{"fromImage": {name}, "tag": {tag}}

	response, err := e.doWithAuth(ctx, http.MethodPost, "/images/create", query, nil, credential)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	scanner := bufio.NewScanner(io.LimitReader(response.Body, maxBodyBytes))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		var progress struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &progress) == nil && progress.Error != "" {
			return fmt.Errorf("HD0315: pull %s: %s", image, progress.Error)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("HD0315: pull %s: %w", image, err)
	}
	return nil
}

// splitImage separates an image reference into the name and tag the Engine's
// create endpoint expects. A digest reference keeps the digest as the tag,
// which is how the Engine pins it.
func splitImage(image string) (string, string) {
	if name, digest, found := strings.Cut(image, "@"); found {
		return name, digest
	}
	// A colon in the final path segment is a tag; one earlier is a registry
	// port, which must not be mistaken for one.
	slash := strings.LastIndex(image, "/")
	colon := strings.LastIndex(image, ":")
	if colon > slash {
		return image[:colon], image[colon+1:]
	}
	return image, "latest"
}

type createContainerRequest struct {
	Image        string              `json:"Image"`
	Cmd          []string            `json:"Cmd,omitempty"`
	Entrypoint   []string            `json:"Entrypoint,omitempty"`
	Env          []string            `json:"Env,omitempty"`
	Labels       map[string]string   `json:"Labels,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	Healthcheck  *engineHealthcheck  `json:"Healthcheck,omitempty"`
	HostConfig   engineHostConfig    `json:"HostConfig"`
}

type engineHealthcheck struct {
	Test        []string `json:"Test"`
	Interval    int64    `json:"Interval,omitempty"`
	Timeout     int64    `json:"Timeout,omitempty"`
	StartPeriod int64    `json:"StartPeriod,omitempty"`
	Retries     int      `json:"Retries,omitempty"`
}

type engineHostConfig struct {
	Binds         []string                    `json:"Binds,omitempty"`
	PortBindings  map[string][]enginePortBind `json:"PortBindings,omitempty"`
	RestartPolicy *engineRestartPolicy        `json:"RestartPolicy,omitempty"`
	Memory        int64                       `json:"Memory,omitempty"`
	NanoCPUs      int64                       `json:"NanoCpus,omitempty"`
}

type enginePortBind struct {
	HostIP   string `json:"HostIp,omitempty"`
	HostPort string `json:"HostPort"`
}

type engineRestartPolicy struct {
	Name string `json:"Name"`
}

func (e *engine) createContainer(ctx context.Context, name string, request createContainerRequest) (string, error) {
	var out struct {
		ID       string   `json:"Id"`
		Warnings []string `json:"Warnings"`
	}
	err := e.decode(ctx, http.MethodPost, "/containers/create", url.Values{"name": {name}}, request, &out)
	return out.ID, err
}

func (e *engine) startContainer(ctx context.Context, id string) error {
	return e.decode(ctx, http.MethodPost, "/containers/"+id+"/start", nil, nil, nil)
}

func (e *engine) stopContainer(ctx context.Context, id string, timeout time.Duration) error {
	query := url.Values{"t": {fmt.Sprint(int(timeout.Seconds()))}}
	err := e.decode(ctx, http.MethodPost, "/containers/"+id+"/stop", query, nil, nil)
	// 304 is "already stopped", which the client treats as success; a 404 is
	// a container that is already gone, which is the desired end state.
	if isNotFound(err) {
		return nil
	}
	return err
}

func (e *engine) removeContainer(ctx context.Context, id string) error {
	query := url.Values{"force": {"true"}, "v": {"false"}}
	err := e.decode(ctx, http.MethodDelete, "/containers/"+id, query, nil, nil)
	if isNotFound(err) {
		return nil
	}
	return err
}

func (e *engine) createVolume(ctx context.Context, name string, labels map[string]string) error {
	return e.decode(ctx, http.MethodPost, "/volumes/create", nil, map[string]any{
		"Name": name, "Labels": labels,
	}, nil)
}

// containerStats reads one non-streaming stats sample.
func (e *engine) containerStats(ctx context.Context, id string) (statsSample, error) {
	var out statsSample
	query := url.Values{"stream": {"false"}, "one-shot": {"true"}}
	err := e.decode(ctx, http.MethodGet, "/containers/"+id+"/stats", query, nil, &out)
	return out, err
}

type statsSample struct {
	Read     time.Time `json:"read"`
	CPUStats struct {
		CPUUsage struct {
			TotalUsage  uint64   `json:"total_usage"`
			PerCPUUsage []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     uint32 `json:"online_cpus"`
		ThrottlingData struct {
			ThrottledPeriods uint64 `json:"throttled_periods"`
		} `json:"throttling_data"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes   uint64 `json:"rx_bytes"`
		TxBytes   uint64 `json:"tx_bytes"`
		RxErrors  uint64 `json:"rx_errors"`
		RxDropped uint64 `json:"rx_dropped"`
		TxErrors  uint64 `json:"tx_errors"`
		TxDropped uint64 `json:"tx_dropped"`
	} `json:"networks"`
	BlkioStats struct {
		IOServiceBytesRecursive []struct {
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
	PidsStats struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
}

// cpuPercent converts two cumulative counters into a percentage the way
// `docker stats` does. A first sample has no predecessor, so it reports 0
// rather than a meaningless spike.
func (s statsSample) cpuPercent() float64 {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(s.CPUStats.SystemCPUUsage) - float64(s.PreCPUStats.SystemCPUUsage)
	if cpuDelta <= 0 || systemDelta <= 0 {
		return 0
	}
	cpus := float64(s.CPUStats.OnlineCPUs)
	if cpus == 0 {
		cpus = float64(len(s.CPUStats.CPUUsage.PerCPUUsage))
	}
	if cpus == 0 {
		cpus = 1
	}
	return (cpuDelta / systemDelta) * cpus * 100
}

// memoryUsage subtracts the page cache, matching `docker stats`. Reporting
// raw usage makes every container look near its limit, which trains operators
// to ignore the metric.
func (s statsSample) memoryUsage() uint64 {
	usage := s.MemoryStats.Usage
	if cache, ok := s.MemoryStats.Stats["inactive_file"]; ok && cache < usage {
		return usage - cache
	}
	if cache, ok := s.MemoryStats.Stats["cache"]; ok && cache < usage {
		return usage - cache
	}
	return usage
}

func (s statsSample) network() (rx, tx uint64) {
	for _, iface := range s.Networks {
		rx += iface.RxBytes
		tx += iface.TxBytes
	}
	return rx, tx
}

// netErrors folds errors and drops in both directions into one counter: the
// operator's question is whether the network is eating packets at all.
func (s statsSample) netErrors() uint64 {
	var total uint64
	for _, iface := range s.Networks {
		total += iface.RxErrors + iface.RxDropped + iface.TxErrors + iface.TxDropped
	}
	return total
}

func (s statsSample) blockIO() (read, write uint64) {
	for _, entry := range s.BlkioStats.IOServiceBytesRecursive {
		switch strings.ToLower(entry.Op) {
		case "read":
			read += entry.Value
		case "write":
			write += entry.Value
		}
	}
	return read, write
}

// containerLogs opens a log stream. The Engine multiplexes stdout and stderr
// over one connection with an 8-byte frame header unless the container has a
// TTY; demuxReader strips those frames so callers get plain text.
func (e *engine) containerLogs(ctx context.Context, id string, query url.Values) (io.ReadCloser, error) {
	response, err := e.do(ctx, http.MethodGet, "/containers/"+id+"/logs", query, nil) //nolint:bodyclose // demuxReader owns the body; its Close closes the stream
	if err != nil {
		return nil, err
	}
	return &demuxReader{source: response.Body}, nil
}

// demuxReader strips Docker's stream-multiplexing frame headers. A header is
// [stream byte, 3 zero bytes, 4-byte big-endian length]; a body with no
// header is passed through unchanged, which is what a TTY container sends.
type demuxReader struct {
	source    io.ReadCloser
	remaining int
	buffered  []byte
	raw       bool
	checked   bool
}

func (d *demuxReader) Read(p []byte) (int, error) {
	if len(d.buffered) > 0 {
		n := copy(p, d.buffered)
		d.buffered = d.buffered[n:]
		return n, nil
	}
	if d.raw {
		return d.source.Read(p)
	}
	if d.remaining == 0 {
		var header [8]byte
		if _, err := io.ReadFull(d.source, header[:]); err != nil {
			return 0, err
		}
		if !d.checked {
			d.checked = true
			// A real header has a stream id of 0-2 and three zero bytes. If
			// this does not look like one, the stream is not multiplexed and
			// the bytes already read are payload.
			if header[0] > 2 || header[1] != 0 || header[2] != 0 || header[3] != 0 {
				d.raw = true
				d.buffered = append([]byte{}, header[:]...)
				n := copy(p, d.buffered)
				d.buffered = d.buffered[n:]
				return n, nil
			}
		}
		d.remaining = int(binary.BigEndian.Uint32(header[4:]))
		if d.remaining == 0 {
			return 0, nil
		}
	}
	limit := len(p)
	if limit > d.remaining {
		limit = d.remaining
	}
	n, err := d.source.Read(p[:limit])
	d.remaining -= n
	return n, err
}

func (d *demuxReader) Close() error { return d.source.Close() }

// engineEvents reads the Engine's event stream for a bounded window.
func (e *engine) engineEvents(ctx context.Context, since time.Time, filters map[string][]string) ([]engineEvent, error) {
	query := url.Values{
		"since": {fmt.Sprint(since.Unix())},
		"until": {fmt.Sprint(time.Now().Unix())},
	}
	if len(filters) > 0 {
		encoded, err := json.Marshal(filters)
		if err != nil {
			return nil, fmt.Errorf("HD0311: encode filters: %w", err)
		}
		query.Set("filters", string(encoded))
	}

	response, err := e.do(ctx, http.MethodGet, "/events", query, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	var events []engineEvent
	scanner := bufio.NewScanner(io.LimitReader(response.Body, maxBodyBytes))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		var event engineEvent
		if json.Unmarshal(scanner.Bytes(), &event) == nil {
			events = append(events, event)
		}
	}
	return events, scanner.Err()
}

type engineEvent struct {
	Type     string `json:"Type"`
	Action   string `json:"Action"`
	TimeNano int64  `json:"timeNano"`
	Actor    struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
}

// --- Swarm endpoints -------------------------------------------------------

// swarmService is a deployed service as the Engine reports it.
type swarmService struct {
	ID      string           `json:"ID"`
	Version swarmVersion     `json:"Version"`
	Spec    swarmServiceSpec `json:"Spec"`
}

type swarmVersion struct {
	Index uint64 `json:"Index"`
}

type swarmServiceSpec struct {
	Name         string            `json:"Name"`
	Labels       map[string]string `json:"Labels,omitempty"`
	TaskTemplate swarmTaskTemplate `json:"TaskTemplate"`
	Mode         swarmServiceMode  `json:"Mode"`
	EndpointSpec swarmEndpointSpec `json:"EndpointSpec,omitempty"`
}

type swarmTaskTemplate struct {
	ContainerSpec swarmContainerSpec `json:"ContainerSpec"`
}

type swarmContainerSpec struct {
	Image   string                 `json:"Image"`
	Env     []string               `json:"Env,omitempty"`
	Labels  map[string]string      `json:"Labels,omitempty"`
	Mounts  []swarmMount           `json:"Mounts,omitempty"`
	Secrets []swarmSecretReference `json:"Secrets,omitempty"`
}

// swarmSecretReference mounts one Swarm secret into a task at
// /run/secrets/<File.Name>.
type swarmSecretReference struct {
	SecretID   string          `json:"SecretID"`
	SecretName string          `json:"SecretName"`
	File       swarmSecretFile `json:"File"`
}

type swarmSecretFile struct {
	Name string `json:"Name"`
	UID  string `json:"UID"`
	GID  string `json:"GID"`
	Mode uint32 `json:"Mode"`
}

type swarmMount struct {
	Type   string `json:"Type"`
	Source string `json:"Source"`
	Target string `json:"Target"`
}

type swarmServiceMode struct {
	Replicated *swarmReplicated `json:"Replicated,omitempty"`
}

type swarmReplicated struct {
	Replicas uint64 `json:"Replicas"`
}

type swarmEndpointSpec struct {
	Ports []swarmPortConfig `json:"Ports,omitempty"`
}

type swarmPortConfig struct {
	Protocol      string `json:"Protocol,omitempty"`
	TargetPort    int    `json:"TargetPort"`
	PublishedPort int    `json:"PublishedPort,omitempty"`
}

// swarmTask is one scheduled unit of a service.
type swarmTask struct {
	ID        string          `json:"ID"`
	ServiceID string          `json:"ServiceID"`
	NodeID    string          `json:"NodeID"`
	CreatedAt time.Time       `json:"CreatedAt"`
	Status    swarmTaskStatus `json:"Status"`
}

type swarmTaskStatus struct {
	State   string `json:"State"`
	Message string `json:"Message,omitempty"`
}

func (e *engine) listServices(ctx context.Context, filters map[string][]string) ([]swarmService, error) {
	query := url.Values{}
	if len(filters) > 0 {
		encoded, err := json.Marshal(filters)
		if err != nil {
			return nil, fmt.Errorf("HD0311: encode filters: %w", err)
		}
		query.Set("filters", string(encoded))
	}
	var services []swarmService
	if err := e.decode(ctx, http.MethodGet, "/services", query, nil, &services); err != nil {
		return nil, err
	}
	return services, nil
}

// swarmSecret is one Swarm secret as the Engine lists it. Data never comes
// back — the Engine API withholds it by design, which is half the appeal.
type swarmSecret struct {
	ID   string          `json:"ID"`
	Spec swarmSecretSpec `json:"Spec"`
}

type swarmSecretSpec struct {
	Name   string            `json:"Name"`
	Labels map[string]string `json:"Labels,omitempty"`
	// Data is base64, sent on create and never returned by the Engine.
	Data string `json:"Data,omitempty"`
}

func (e *engine) listSecrets(ctx context.Context, filters map[string][]string) ([]swarmSecret, error) {
	query := url.Values{}
	if len(filters) > 0 {
		encoded, err := json.Marshal(filters)
		if err != nil {
			return nil, fmt.Errorf("HD0311: encode filters: %w", err)
		}
		query.Set("filters", string(encoded))
	}
	var secrets []swarmSecret
	if err := e.decode(ctx, http.MethodGet, "/secrets", query, nil, &secrets); err != nil {
		return nil, err
	}
	return secrets, nil
}

func (e *engine) createSecret(ctx context.Context, specification swarmSecretSpec) (string, error) {
	var created struct {
		ID string `json:"ID"`
	}
	if err := e.decode(ctx, http.MethodPost, "/secrets/create", nil, specification, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

func (e *engine) deleteSecret(ctx context.Context, id string) error {
	response, err := e.do(ctx, http.MethodDelete, "/secrets/"+id, nil, nil)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	return nil
}

func (e *engine) createService(ctx context.Context, specification swarmServiceSpec) error {
	// do() surfaces any >=400 as a typed engineError already.
	response, err := e.do(ctx, http.MethodPost, "/services/create", nil, specification)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	return nil
}

// updateService needs the service's current version index: the Engine uses it
// as optimistic concurrency, and a stale index means someone else changed the
// service since we listed it — failing is correct, the next plan re-reads.
func (e *engine) updateService(ctx context.Context, id string, version swarmVersion, specification swarmServiceSpec) error {
	query := url.Values{}
	query.Set("version", fmt.Sprint(version.Index))
	response, err := e.do(ctx, http.MethodPost, "/services/"+id+"/update", query, specification)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	return nil
}

func (e *engine) deleteService(ctx context.Context, id string) error {
	response, err := e.do(ctx, http.MethodDelete, "/services/"+id, nil, nil)
	if err != nil {
		var refusal *engineError
		// A service already gone is the desired end state, not a failure.
		if errors.As(err, &refusal) && refusal.notFound() {
			return nil
		}
		return err
	}
	_ = response.Body.Close()
	return nil
}

func (e *engine) listTasks(ctx context.Context, serviceID string) ([]swarmTask, error) {
	encoded, err := json.Marshal(map[string][]string{"service": {serviceID}})
	if err != nil {
		return nil, fmt.Errorf("HD0311: encode filters: %w", err)
	}
	query := url.Values{}
	query.Set("filters", string(encoded))
	var tasks []swarmTask
	if err := e.decode(ctx, http.MethodGet, "/tasks", query, nil, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (e *engine) serviceLogs(ctx context.Context, id string, query url.Values) (io.ReadCloser, error) {
	response, err := e.do(ctx, http.MethodGet, "/services/"+id+"/logs", query, nil) //nolint:bodyclose // demuxReader owns the body; its Close closes the stream
	if err != nil {
		return nil, err
	}
	return &demuxReader{source: response.Body}, nil
}
