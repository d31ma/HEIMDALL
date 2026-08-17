package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The CLI is a client of the same public API the web tier and CI use. It has
// no privileged path into the control plane: `heimdall sync` makes the same
// authorized call a browser does, so there is no second authorization surface
// to keep correct.

// session is the credential `heimdall login` stores. It holds a session id
// and secret issued by SESAME — HEIMDALL neither mints nor validates it
// locally.
type session struct {
	Addr      string `json:"addr"`
	SessionID string `json:"session_id,omitempty"`
	Secret    string `json:"session_secret,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	// AccessToken and RefreshToken are what `heimdall login --device` stores
	// instead of a session pair. The access token is short-lived; the CLI
	// refreshes it through the control plane when it expires.
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	// TokenExpiry is a unix timestamp for the access token, kept so the CLI
	// refreshes proactively instead of burning one request on a 401.
	TokenExpiry int64 `json:"token_expiry,omitempty"`
}

// bearer is whichever credential this session holds.
func (s session) bearer() string {
	if s.AccessToken != "" {
		return s.AccessToken
	}
	if s.SessionID != "" {
		return s.SessionID + "." + s.Secret
	}
	return ""
}

func sessionPath(deployment string) string { return filepath.Join(deployment, "session.json") }

func loadSession(deployment string) (session, error) {
	raw, err := os.ReadFile(sessionPath(deployment))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return session{}, errors.New("HD0120: not logged in; run `heimdall login`")
		}
		return session{}, fmt.Errorf("HD0120: read session: %w", err)
	}
	var stored session
	if err := json.Unmarshal(raw, &stored); err != nil {
		return session{}, fmt.Errorf("HD0120: parse session: %w", err)
	}

	// A device-grant session refreshes itself when the access token is at or
	// near expiry. SESAME rotates the refresh token on every use, so the
	// stored pair is replaced whether or not anything else changes.
	if stored.RefreshToken != "" && time.Now().Unix() >= stored.TokenExpiry-30 {
		var tokens struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int64  `json:"expires_in"`
		}
		if err := call(session{Addr: stored.Addr}, http.MethodPost, "/api/v1/auth/device/refresh",
			map[string]string{"refresh_token": stored.RefreshToken}, &tokens); err != nil {
			return session{}, fmt.Errorf("HD0129: the session expired and could not refresh; run `heimdall login --device` again: %w", err)
		}
		stored.AccessToken = tokens.AccessToken
		stored.RefreshToken = tokens.RefreshToken
		stored.TokenExpiry = time.Now().Unix() + tokens.ExpiresIn
		if err := saveSession(deployment, stored); err != nil {
			return session{}, err
		}
	}
	return stored, nil
}

func saveSession(deployment string, stored session) error {
	raw, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("HD0121: encode session: %w", err)
	}
	// 0600: this file is a bearer credential.
	if err := os.WriteFile(sessionPath(deployment), append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("HD0121: write session: %w", err)
	}
	return nil
}

// httpClient builds the CLI's client.
//
// A control plane initialised by `heimdall init` serves a certificate signed
// by its own agent CA, which no system trust store knows. HD_CA_FILE points
// at that CA. It is not an escape hatch — there is deliberately no "skip
// verification" option, because the one thing an operator reaches for under
// time pressure should not be the one that disables the check.
func httpClient() (*http.Client, error) {
	transport := &http.Transport{}
	if caFile := os.Getenv("HD_CA_FILE"); caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("HD0140: read HD_CA_FILE: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("HD0140: %s holds no usable certificate", caFile)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: transport, Timeout: 5 * time.Minute}, nil
}

// call makes one authenticated API request and decodes the result.
func call(stored session, method, path string, body, into any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("HD0122: encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequest(method, strings.TrimSuffix(stored.Addr, "/")+path, reader)
	if err != nil {
		return fmt.Errorf("HD0122: build request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer := stored.bearer(); bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}

	client, err := httpClient()
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		// A self-signed control plane is the common case, and the stdlib's
		// message does not mention the fix.
		if strings.Contains(err.Error(), "certificate") && os.Getenv("HD_CA_FILE") == "" {
			return fmt.Errorf(
				"HD0141: %s %s: %w\n\nThe control plane serves its own certificate. "+
					"Point HD_CA_FILE at <deployment>/keys/agent-ca.crt to trust it",
				method, path, err)
		}
		return fmt.Errorf("HD0123: %s %s: %w", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("HD0123: read response: %w", err)
	}

	// Decode first, whatever the status. A 409 from a sync carries the whole
	// operation document, and the caller needs to print the plan and the
	// per-service failures — not just "conflict".
	decoded := into != nil && json.Unmarshal(raw, into) == nil

	if response.StatusCode >= 400 {
		var failure struct {
			Code       string `json:"code"`
			Message    string `json:"message"`
			ReasonCode string `json:"reason_code"`
		}
		_ = json.Unmarshal(raw, &failure)
		switch {
		// reason_code is only a denial on a 403. Other documents carry a
		// reason_code field of their own — an operation records the code its
		// authorization *allowed* under — and reading that as a refusal turns
		// a successful decision into a confusing error.
		case response.StatusCode == http.StatusForbidden:
			return fmt.Errorf("%s: denied by policy (%s)", failure.Code, failure.ReasonCode)
		case failure.Message != "":
			return errors.New(failure.Message)
		case decoded:
			// The body was a document rather than an error envelope; the
			// caller will report what it says.
			return fmt.Errorf("HD0123: %s %s returned %d", method, path, response.StatusCode)
		default:
			return fmt.Errorf("HD0123: %s %s returned %d", method, path, response.StatusCode)
		}
	}

	if into != nil && !decoded {
		return fmt.Errorf("HD0124: %s %s returned a body that is not the expected shape", method, path)
	}
	return nil
}

func runLogin(args []string) error {
	var deploymentFlag, addrFlag, userFlag, namespaceFlag string
	device := false
	for _, arg := range args {
		if arg == "--device" {
			device = true
		}
	}
	parseFlags(args, map[string]*string{
		"--deployment": &deploymentFlag,
		"--addr":       &addrFlag,
		"--user":       &userFlag,
		"--namespace":  &namespaceFlag,
	})

	deployment, err := deploymentDir(deploymentFlag)
	if err != nil {
		return err
	}
	addr := envOr("HD_ADDR_URL", addrFlag)
	if addr == "" {
		addr = "http://127.0.0.1:8080"
	}
	if device {
		return deviceLogin(deployment, addr)
	}
	if userFlag == "" {
		return errors.New("HD0125: --user is required")
	}

	// The password is read from the environment and never from a flag, so it
	// stays out of shell history and out of `ps` output. This is SESAME's own
	// rule, adopted here.
	password := os.Getenv("HD_PASSWORD")
	if password == "" {
		return errors.New(
			"HD0126: set HD_PASSWORD in the environment; HEIMDALL never accepts a password as a flag, " +
				"because a flag lands in shell history and in ps output")
	}
	namespace := namespaceFlag
	if namespace == "" {
		namespace = "username"
	}

	var issued struct {
		SessionID string `json:"session_id"`
		Secret    string `json:"session_secret"`
		ExpiresAt string `json:"expires_at"`
		Assurance string `json:"assurance"`
	}
	if err := call(session{Addr: addr}, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"namespace":  namespace,
		"identifier": userFlag,
		"password":   password,
		"totp":       os.Getenv("HD_TOTP"),
	}, &issued); err != nil {
		return err
	}

	if err := saveSession(deployment, session{
		Addr: addr, SessionID: issued.SessionID, Secret: issued.Secret, ExpiresAt: issued.ExpiresAt,
	}); err != nil {
		return err
	}
	fmt.Printf("logged in to %s as %s (assurance %s, expires %s)\n",
		addr, userFlag, issued.Assurance, issued.ExpiresAt)
	return nil
}

// deviceLogin runs the RFC 8628 flow: show a short code, wait for a person
// to approve it in the web UI, receive tokens. No password ever touches this
// terminal — which is the point, because a terminal is where shoulder
// surfing, shell history, and SSH jump hosts live.
func deviceLogin(deployment, addr string) error {
	var grant struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int64  `json:"expires_in"`
		Interval        int64  `json:"interval"`
	}
	if err := call(session{Addr: addr}, http.MethodPost, "/api/v1/auth/device/start", map[string]string{}, &grant); err != nil {
		return err
	}

	fmt.Printf("To sign in, open the HEIMDALL web UI and approve this device:\n\n")
	fmt.Printf("    code    %s\n", grant.UserCode)
	fmt.Printf("    expires in %d minutes\n\n", grant.ExpiresIn/60)
	fmt.Println("Waiting for approval…")

	interval := time.Duration(grant.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(grant.ExpiresIn) * time.Second)

	for time.Now().Before(deadline) {
		time.Sleep(interval)

		var tokens struct {
			Status       string `json:"status"`
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int64  `json:"expires_in"`
		}
		err := call(session{Addr: addr}, http.MethodPost, "/api/v1/auth/device/token",
			map[string]string{"device_code": grant.DeviceCode}, &tokens)
		if err != nil {
			// 401 is the terminal answer: refused, expired, or never right.
			if strings.Contains(err.Error(), "HD0401") || strings.Contains(err.Error(), "not approved") {
				return errors.New("HD0128: the device was not approved")
			}
			return err
		}
		if tokens.AccessToken == "" {
			continue // authorization_pending
		}

		if err := saveSession(deployment, session{
			Addr:         addr,
			AccessToken:  tokens.AccessToken,
			RefreshToken: tokens.RefreshToken,
			TokenExpiry:  time.Now().Unix() + tokens.ExpiresIn,
		}); err != nil {
			return err
		}
		fmt.Printf("approved — logged in to %s\n", addr)
		return nil
	}
	return errors.New("HD0128: the code expired before anyone approved it")
}

func runApp(args []string) error {
	if len(args) == 0 {
		return errors.New("HD0127: usage: heimdall app list --project NAME | heimdall app get --project NAME --app NAME")
	}
	command, rest := args[0], args[1:]

	var deploymentFlag, projectFlag, appFlag string
	parseFlags(rest, map[string]*string{
		"--deployment": &deploymentFlag,
		"--project":    &projectFlag,
		"--app":        &appFlag,
	})
	deployment, err := deploymentDir(deploymentFlag)
	if err != nil {
		return err
	}
	stored, err := loadSession(deployment)
	if err != nil {
		return err
	}
	if projectFlag == "" {
		return errors.New("HD0127: --project is required")
	}

	switch command {
	case "list":
		var result struct {
			Applications []struct {
				Name      string `json:"name"`
				Path      string `json:"path"`
				Suspended bool   `json:"suspended"`
			} `json:"applications"`
		}
		if err := call(stored, http.MethodGet, "/api/v1/projects/"+projectFlag+"/apps", nil, &result); err != nil {
			return err
		}
		if len(result.Applications) == 0 {
			fmt.Println("no applications in project " + projectFlag)
			return nil
		}
		for _, application := range result.Applications {
			state := ""
			if application.Suspended {
				state = "  (suspended)"
			}
			fmt.Printf("%-24s %s%s\n", application.Name, application.Path, state)
		}
		return nil

	case "get":
		if appFlag == "" {
			return errors.New("HD0127: --app is required")
		}
		var result map[string]any
		if err := call(stored, http.MethodGet,
			"/api/v1/projects/"+projectFlag+"/apps/"+appFlag, nil, &result); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)

	default:
		return fmt.Errorf("HD0127: unknown app subcommand %q", command)
	}
}

// runDiff shows what a sync would do, without doing it. It is the dry run
// rendered for a terminal.
func runDiff(args []string) error {
	var deploymentFlag, projectFlag, appFlag string
	parseFlags(args, map[string]*string{
		"--deployment": &deploymentFlag,
		"--project":    &projectFlag,
		"--app":        &appFlag,
	})
	deployment, err := deploymentDir(deploymentFlag)
	if err != nil {
		return err
	}
	stored, err := loadSession(deployment)
	if err != nil {
		return err
	}
	if projectFlag == "" || appFlag == "" {
		return errors.New("HD0128: --project and --app are required")
	}

	var result struct {
		Status struct {
			SyncStatus string `json:"sync_status"`
			Health     string `json:"health"`
			Desired    string `json:"desired_revision"`
			Live       string `json:"live_revision"`
			Services   []struct {
				Service string `json:"service"`
				Kind    string `json:"kind"`
				Health  string `json:"health"`
				Message string `json:"message"`
				Changes []struct {
					Field   string `json:"field"`
					Kind    string `json:"kind"`
					Desired string `json:"desired"`
					Live    string `json:"live"`
					Secret  bool   `json:"secret"`
				} `json:"changes"`
			} `json:"services"`
		} `json:"status"`
	}
	if err := call(stored, http.MethodGet,
		"/api/v1/projects/"+projectFlag+"/apps/"+appFlag, nil, &result); err != nil {
		return err
	}

	status := result.Status
	fmt.Printf("%s/%s  %s  %s\n", projectFlag, appFlag, status.SyncStatus, status.Health)
	fmt.Printf("  desired %s\n", short(status.Desired))
	if status.Live != "" {
		fmt.Printf("  live    %s\n", short(status.Live))
	}
	for _, service := range status.Services {
		header := "  " + service.Service
		if service.Kind != "" {
			header += " (" + service.Kind + ")"
		}
		if service.Health != "" {
			header += " — " + service.Health
		}
		fmt.Println(header)
		if service.Message != "" {
			fmt.Println("      " + service.Message)
		}
		for _, change := range service.Changes {
			// A secret is a reference in both columns; there is no value to
			// hide because none was ever rendered.
			fmt.Printf("      %-24s %s -> %s\n", change.Field, dash(change.Live), dash(change.Desired))
		}
	}
	return nil
}

func runSync(args []string) error {
	var deploymentFlag, projectFlag, appFlag, servicesFlag, revisionFlag, dryRunFlag string
	parseFlags(args, map[string]*string{
		"--deployment": &deploymentFlag,
		"--project":    &projectFlag,
		"--app":        &appFlag,
		"--services":   &servicesFlag,
		"--revision":   &revisionFlag,
		"--dry-run":    &dryRunFlag,
	})
	deployment, err := deploymentDir(deploymentFlag)
	if err != nil {
		return err
	}
	stored, err := loadSession(deployment)
	if err != nil {
		return err
	}
	if projectFlag == "" || appFlag == "" {
		return errors.New("HD0129: --project and --app are required")
	}

	path := "/api/v1/projects/" + projectFlag + "/apps/" + appFlag + "/sync"
	body := map[string]any{}
	if revisionFlag != "" {
		path = "/api/v1/projects/" + projectFlag + "/apps/" + appFlag + "/rollback"
		body = map[string]any{"revision": revisionFlag}
	} else {
		if servicesFlag != "" {
			body["services"] = strings.Split(servicesFlag, ",")
		}
		// --dry-run takes no value; parseFlags is string-only, so its presence
		// is what counts.
		for _, argument := range args {
			if argument == "--dry-run" {
				body["dry_run"] = true
			}
		}
	}

	var operation struct {
		ID         string            `json:"id"`
		Phase      string            `json:"phase"`
		Revision   string            `json:"revision"`
		Message    string            `json:"message"`
		Failures   map[string]string `json:"failures"`
		Operations []struct {
			Kind    string `json:"kind"`
			Service string `json:"service"`
			Reason  string `json:"reason"`
		} `json:"operations"`
		Applied []struct {
			Kind    string `json:"kind"`
			Service string `json:"service"`
		} `json:"applied"`
	}
	callErr := call(stored, http.MethodPost, path, body, &operation)

	// The operation document is printed even when the call reported a
	// failure: a failed sync is still a real record, and its per-service
	// detail is what an operator needs.
	fmt.Printf("%s/%s  %s  %s\n", projectFlag, appFlag, dash(operation.Phase), short(operation.Revision))
	for _, planned := range operation.Operations {
		fmt.Printf("  plan   %-8s %-20s %s\n", planned.Kind, planned.Service, planned.Reason)
	}
	for _, applied := range operation.Applied {
		fmt.Printf("  done   %-8s %s\n", applied.Kind, applied.Service)
	}
	for service, failure := range operation.Failures {
		fmt.Printf("  FAILED %-8s %s\n", service, failure)
	}
	if operation.Message != "" {
		fmt.Println("  " + operation.Message)
	}

	if operation.Phase == "failed" {
		return errors.New("HD0130: the sync completed with failures")
	}
	return callErr
}

func short(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	if revision == "" {
		return "(none)"
	}
	return revision
}

func dash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
