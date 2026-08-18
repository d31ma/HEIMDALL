// Package agent is the process that runs on a Docker Engine host.
//
// It opens no port. It connects outbound over mTLS, long-polls for work, runs
// the same Docker adapter the control plane would have run, and reports back.
// A customer host therefore exposes nothing new to the network, and the
// control plane needs no route into it.
//
// The agent holds no persistent authority of its own: its certificate names
// exactly one target, that name was chosen by the control plane at
// enrollment, and every job it receives is scoped to that target.
package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/d31ma/heimdall/internal/dispatch"
	"github.com/d31ma/heimdall/internal/enroll"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/provider/docker"
)

// Credentials are what enrollment produces and the agent stores. The private
// key never leaves the host, and the certificate names the one target this
// agent may act for.
type Credentials struct {
	URL         string `json:"url"`
	AgentID     string `json:"agent_id"`
	TargetID    string `json:"target_id"`
	Certificate string `json:"certificate_pem"`
	Key         string `json:"key_pem"`
	CA          string `json:"ca_pem"`
	// Fingerprint is what was pinned at enrollment. It is kept so a later
	// connection can notice the control plane's certificate changed.
	Fingerprint string `json:"fingerprint"`
}

// Files the agent keeps on the host.
const credentialsFile = "agent.json"

// pollWait is how long one long poll parks before returning empty. Under most
// proxies and NAT tables a minute is comfortably inside the idle timeout, and
// an empty return costs one round trip.
const pollWait = 55 * time.Second

// maxResponseBytes bounds what the control plane may return. The agent
// authenticated it, but a bounded read is still the difference between an
// error and an OOM on a small host.
const maxResponseBytes = 32 << 20

// Enroll exchanges an enrollment token for a client certificate.
//
// The fingerprint in the token is pinned *before* the token is sent. That
// ordering is the whole point: a machine-in-the-middle on the agent's first
// connection is refused having learned nothing.
func Enroll(ctx context.Context, dir, encodedToken string) (Credentials, error) {
	token, err := decodeToken(encodedToken)
	if err != nil {
		return Credentials{}, err
	}

	pinned, err := enroll.PinnedTLSConfig(token.Fingerprint)
	if err != nil {
		return Credentials{}, err
	}

	csr, key, err := enroll.NewCertificateRequest("heimdall-agent")
	if err != nil {
		return Credentials{}, err
	}

	body, err := json.Marshal(map[string]string{
		"token": encodedToken,
		"csr":   string(csr),
	})
	if err != nil {
		return Credentials{}, fmt.Errorf("HD0380: encode enrollment request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		token.URL+"/api/v1/agent/enroll", bytes.NewReader(body))
	if err != nil {
		return Credentials{}, fmt.Errorf("HD0380: build enrollment request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: pinned},
		Timeout:   30 * time.Second,
		// An enrollment must not follow a redirect: the token is only safe to
		// send to the host whose fingerprint was pinned.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("HD0381: the control plane redirected enrollment; refusing to follow")
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return Credentials{}, fmt.Errorf("HD0382: enrol with %s: %w", token.URL, err)
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Credentials{}, fmt.Errorf("HD0382: read enrollment response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &failure)
		if failure.Message == "" {
			failure.Message = fmt.Sprintf("the control plane returned %d", response.StatusCode)
		}
		return Credentials{}, fmt.Errorf("HD0383: enrollment refused: %s", failure.Message)
	}

	var issued struct {
		AgentID     string `json:"agent_id"`
		TargetID    string `json:"target_id"`
		Certificate string `json:"certificate_pem"`
		CA          string `json:"ca_pem"`
	}
	if err := json.Unmarshal(raw, &issued); err != nil {
		return Credentials{}, fmt.Errorf("HD0383: decode enrollment response: %w", err)
	}

	credentials := Credentials{
		URL: token.URL, AgentID: issued.AgentID, TargetID: issued.TargetID,
		Certificate: issued.Certificate, Key: string(key), CA: issued.CA,
		Fingerprint: token.Fingerprint,
	}
	if err := Save(dir, credentials); err != nil {
		return Credentials{}, err
	}
	return credentials, nil
}

// decodeToken reads a token's public fields without verifying it. The agent
// cannot verify — it holds no key — and does not need to: the token's value
// to the agent is the URL and the fingerprint, and a forged one simply fails
// to connect or is refused by the control plane.
func decodeToken(encoded string) (enroll.Token, error) {
	raw, err := decodeBase64URL(encoded)
	if err != nil {
		return enroll.Token{}, errors.New("HD0384: the enrollment token is not readable")
	}
	var token enroll.Token
	if err := json.Unmarshal(raw, &token); err != nil {
		return enroll.Token{}, errors.New("HD0384: the enrollment token is not readable")
	}
	if token.URL == "" || token.Fingerprint == "" {
		return enroll.Token{}, errors.New(
			"HD0384: the enrollment token carries no URL or fingerprint, so the first connection could not be protected")
	}
	return token, nil
}

// Save writes credentials to the host. The key is 0600; the directory is 0700.
func Save(dir string, credentials Credentials) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("HD0385: create agent directory: %w", err)
	}
	encoded, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return fmt.Errorf("HD0385: encode credentials: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, credentialsFile), append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("HD0385: write credentials: %w", err)
	}
	return nil
}

// Load reads stored credentials.
func Load(dir string) (Credentials, error) {
	raw, err := os.ReadFile(filepath.Join(dir, credentialsFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Credentials{}, fmt.Errorf(
				"HD0386: %s holds no agent credentials; run `heimdall agent enroll --token ...` first", dir)
		}
		return Credentials{}, fmt.Errorf("HD0386: read credentials: %w", err)
	}
	var credentials Credentials
	if err := json.Unmarshal(raw, &credentials); err != nil {
		return Credentials{}, fmt.Errorf("HD0386: parse credentials: %w", err)
	}
	return credentials, nil
}

// Agent is the running loop.
type Agent struct {
	Credentials Credentials
	// Endpoint is the local Docker Engine. Empty uses the default socket.
	Endpoint string
	Logger   *slog.Logger
	// PollWait overrides the long-poll duration, for tests.
	PollWait time.Duration
	// Backoff bounds reconnection attempts. Zero uses a sensible ramp.
	MaxBackoff time.Duration

	client   *http.Client
	provider *docker.Provider
}

// Run polls for work until the context is cancelled.
//
// A failed poll is never fatal. An agent whose control plane restarts, or
// whose link drops, must reconnect on its own — a host that needs someone to
// log in and restart the agent is a host that stays out of sync.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.start(); err != nil {
		return err
	}

	wait := a.PollWait
	if wait <= 0 {
		wait = pollWait
	}
	maxBackoff := a.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 30 * time.Second
	}
	backoff := time.Second

	a.log().Info("agent connected",
		"target", a.Credentials.TargetID, "agent", a.Credentials.AgentID, "url", a.Credentials.URL)

	// The metrics half runs beside the work loop and never blocks it: a
	// deploy must not wait on a stats scrape.
	metricsCtx, stopMetrics := context.WithCancel(ctx)
	defer stopMetrics()
	go a.runMetrics(metricsCtx)

	for {
		if ctx.Err() != nil {
			return nil
		}

		job, err := a.poll(ctx, wait)
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil:
			a.log().Warn("poll failed, retrying", "error", err, "in", backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = time.Second
		if job == nil {
			// An empty poll. Immediately ask again; that is what keeps the
			// connection warm through proxies.
			continue
		}

		outcome := a.run(ctx, *job)
		if err := a.report(ctx, outcome); err != nil {
			// The work happened; only the report failed. Say so plainly — the
			// control plane will time out and the operator needs to know the
			// host may have diverged from what the sync reported.
			a.log().Error("could not report a completed job; the control plane will report a timeout",
				"job", job.ID, "error", err)
		}
	}
}

func (a *Agent) start() error {
	tlsConfig, err := enroll.AgentTLSConfig(
		[]byte(a.Credentials.Certificate), []byte(a.Credentials.Key), []byte(a.Credentials.CA))
	if err != nil {
		return err
	}
	a.client = &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
		// No client-side timeout: a long poll is meant to block. The context
		// and the server's own deadline bound it.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("HD0387: the control plane redirected an agent request; refusing to follow")
		},
	}
	a.provider = &docker.Provider{}

	// Fail at startup rather than on the first job if the Engine is not
	// reachable: an agent that cannot run anything should say so immediately.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	version, err := a.provider.Ping(ctx, provider.Target{Endpoint: a.Endpoint})
	if err != nil {
		return fmt.Errorf("HD0388: the local Docker Engine is not reachable: %w", err)
	}
	a.log().Info("docker engine reachable", "version", version)
	return nil
}

func (a *Agent) log() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.Default()
}

func (a *Agent) poll(ctx context.Context, wait time.Duration) (*dispatch.Job, error) {
	url := fmt.Sprintf("%s/api/v1/agent/work?wait=%d", a.Credentials.URL, int(wait.Seconds()))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := a.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	switch response.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusOK:
	default:
		return nil, fmt.Errorf("HD0389: the control plane answered a poll with %d", response.StatusCode)
	}

	var job dispatch.Job
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&job); err != nil {
		return nil, fmt.Errorf("HD0389: decode job: %w", err)
	}
	// Defence in depth: the certificate already scopes this agent to one
	// target, and the control plane dispatches by that. A job for another
	// target means something is wrong, and running it would be worse.
	if job.TargetID != a.Credentials.TargetID {
		return nil, fmt.Errorf("HD0390: received a job for target %s, but this agent serves %s",
			job.TargetID, a.Credentials.TargetID)
	}
	return &job, nil
}

// run executes one job against the local Engine.
func (a *Agent) run(ctx context.Context, job dispatch.Job) dispatch.Outcome {
	return ExecuteJob(ctx, a.Endpoint, job)
}

// ExecuteJob runs one job against a Docker Engine endpoint. It is the whole
// of what an agent does with work; exported so a test can be the agent
// without standing up the mTLS loop that internal/agent's own tests prove.
func ExecuteJob(ctx context.Context, endpoint string, job dispatch.Job) dispatch.Outcome {
	outcome := dispatch.Outcome{JobID: job.ID}
	target := provider.Target{
		ID: job.TargetID, Provider: "docker",
		// The job's App carries the real project; it lands in both fields so
		// projectOf takes the Project branch and older readers of Region
		// agree with it.
		Project: job.App.Project, Region: job.App.Project,
		Endpoint: endpoint,
	}

	// The resolved values arrived with this job and live only in these
	// closures, for the duration of this call. Nothing writes them down.
	secrets := func(_ context.Context, reference string) (string, error) {
		value, ok := job.Secrets[reference]
		if !ok {
			return "", fmt.Errorf("HD0391: the control plane sent no value for secret %q", reference)
		}
		return value, nil
	}
	registries := func(_ context.Context, image string) (*provider.RegistryCredential, error) {
		for server, credential := range job.Registries {
			if provider.RegistryMatches(server, image) {
				found := credential
				return &found, nil
			}
		}
		return nil, nil
	}

	adapter := &docker.Provider{SecretResolver: secrets}

	switch job.Kind {
	case dispatch.KindObserve:
		live, err := adapter.Observe(ctx, target, job.App)
		if err != nil {
			outcome.Error = err.Error()
			return outcome
		}
		outcome.Live = live

	case dispatch.KindInstances:
		instances, err := adapter.Instances(ctx, target, job.App)
		if err != nil {
			outcome.Error = err.Error()
			return outcome
		}
		outcome.Instances = instances

	case dispatch.KindPlan:
		plan, err := adapter.Plan(ctx, target, job.Spec)
		if err != nil {
			outcome.Error = err.Error()
			return outcome
		}
		outcome.Plan = plan

	case dispatch.KindSync:
		// Plan locally rather than trusting one computed elsewhere: live
		// state may have moved since, and this process is the only one that
		// can see it.
		plan, err := adapter.Plan(ctx, target, job.Spec)
		if err != nil {
			outcome.Error = err.Error()
			return outcome
		}
		outcome.Plan = plan

		result, err := adapter.Apply(docker.WithApply(ctx, docker.ApplyOptions{
			Spec: job.Spec, Prune: job.Prune, Registries: registries,
		}), target, plan)
		if err != nil {
			outcome.Error = err.Error()
			outcome.Result = result
			return outcome
		}
		outcome.Result = result

	case dispatch.KindMetrics:
		series, err := adapter.Metrics(ctx, target, provider.InstanceRef{
			AppRef: job.App, Service: job.Service, Instance: job.Instance,
		}, provider.Window{})
		if err != nil {
			outcome.Error = err.Error()
			return outcome
		}
		outcome.Series = series

	case dispatch.KindLogs:
		tail := job.Tail
		if tail <= 0 || tail > 2000 {
			tail = 200
		}
		stream, err := adapter.Logs(ctx, target, provider.InstanceRef{
			AppRef: job.App, Service: job.Service, Instance: job.Instance,
		}, provider.LogFilter{Tail: tail})
		if err != nil {
			outcome.Error = err.Error()
			return outcome
		}
		// Bounded read: the tail already limits lines, this limits bytes, so
		// a container logging megabyte lines cannot flood the rendezvous.
		logs, err := io.ReadAll(io.LimitReader(stream, 512<<10))
		_ = stream.Close()
		if err != nil {
			outcome.Error = err.Error()
			return outcome
		}
		outcome.Logs = logs

	case dispatch.KindEvents:
		events, err := adapter.Events(ctx, target, job.App)
		if err != nil {
			outcome.Error = err.Error()
			return outcome
		}
		outcome.Events = events

	default:
		outcome.Error = fmt.Sprintf("HD0392: unsupported job kind %q", job.Kind)
	}
	return outcome
}

func (a *Agent) report(ctx context.Context, outcome dispatch.Outcome) error {
	body, err := json.Marshal(outcome)
	if err != nil {
		return fmt.Errorf("HD0393: encode outcome: %w", err)
	}
	// A short independent context: the job's context may already be done, and
	// the result is worth one more attempt regardless.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.Credentials.URL+"/api/v1/agent/result", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := a.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))

	if response.StatusCode >= 400 {
		return fmt.Errorf("HD0393: the control plane answered a result with %d", response.StatusCode)
	}
	return nil
}

func decodeBase64URL(encoded string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
}
