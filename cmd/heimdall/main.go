// Command heimdall is the control plane binary.
//
// Startup order is fixed and fail-closed: FYLO, then `sesame doctor`, then
// the HTTP listener. A child that will not start is fatal — the process never
// reaches a state where it serves a route it cannot authorize.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/d31ma/heimdall/internal/api"
	"github.com/d31ma/heimdall/internal/auth"
	"github.com/d31ma/heimdall/internal/dispatch"
	"github.com/d31ma/heimdall/internal/enroll"
	"github.com/d31ma/heimdall/internal/git"
	"github.com/d31ma/heimdall/internal/observe"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/provider/aca"
	"github.com/d31ma/heimdall/internal/provider/cloudrun"
	"github.com/d31ma/heimdall/internal/provider/docker"
	"github.com/d31ma/heimdall/internal/provider/ecs"
	"github.com/d31ma/heimdall/internal/reconcile"
	hdregistry "github.com/d31ma/heimdall/internal/registry"
	hdsecrets "github.com/d31ma/heimdall/internal/secrets"
	"github.com/d31ma/heimdall/internal/store"
	"github.com/d31ma/sesame/clients/go/sesame"
)

// version is stamped by the release build with -ldflags. The default marks a
// binary that did not come from CI, which is exactly what a user reporting a
// bug needs to know.
var version = "0.0.0-dev"

const usage = `heimdall — GitOps continuous delivery for Docker Compose workloads

Usage:
  heimdall init     [--deployment DIR] [--issuer URL] [--tenant NAME] [--admin NAME]
  heimdall doctor   [--deployment DIR]
  heimdall serve    [--deployment DIR] [--addr HOST:PORT] [--standby]
  heimdall version  [--output text|json]
  heimdall contract [--write PATH]
  heimdall login    --user NAME [--addr URL] [--namespace NS] | --device [--addr URL]
  heimdall app      list|get --project NAME [--app NAME]
  heimdall diff     --project NAME --app NAME
  heimdall sync     --project NAME --app NAME [--dry-run] [--services a,b] [--revision SHA]
  heimdall enroll   --target ID [--url https://host:port] [--lifetime 1h]
  heimdall agent    enroll --token TOKEN | run [--dir DIR] [--docker ENDPOINT]
  heimdall secret   set --name NAME   (value from HD_SECRET_VALUE or stdin)
  heimdall backup   --output FILE.tar.gz [--deployment DIR]   (serve must be stopped)
  heimdall restore  --input FILE.tar.gz [--deployment DIR]

Environment (one namespace, HD_):
  HD_DEPLOYMENT   deployment directory                 (default ~/.heimdall)
  HD_ADDR         listen address                       (default 127.0.0.1:8080)
  HD_FYLO_ROOT    HEIMDALL document root, local FS only (default <deployment>/fylo-root)
  HD_ISSUER       token issuer base URL, https only    (default https://localhost:8443)
  HD_TENANT       installation tenant name             (default heimdall)
  HD_ADDR_URL     control-plane URL the CLI talks to    (default http://127.0.0.1:8080)
  HD_PASSWORD     password for heimdall login
  HD_ADMIN_PASSWORD  password for the administrator heimdall init --admin creates
  HD_TOTP         TOTP code for heimdall login
  HD_PUBLIC_URL   URL agents connect to, bound into enrollment tokens
  HD_CA_FILE      CA the CLI trusts   (usually <deployment>/keys/agent-ca.crt)
  HD_AGENT_DIR    agent credential directory           (default ~/.heimdall-agent)
  HD_METRICS_RETENTION  rollup retention, 24h to 336h  (default 25h)
  HD_DOCKER_ENDPOINT  local Docker Engine on an agent host
  HD_TLS          serve over TLS with the deployment's certificate (default true)
  HD_SYNC_INTERVAL  how often auto-sync and self-heal look  (default 1m)
  SESAME_BINARY   sesame executable                    (default: PATH)
  FYLO_BINARY     fylo executable                      (default: PATH)
  CHEX_BINARY     chex executable                      (default: PATH)

Credentials are read from the environment, never from flags, so they stay out
of shell history and ps output.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "heimdall: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return errors.New("HD0100: a subcommand is required")
	}
	command, rest := args[0], args[1:]
	switch command {
	case "version":
		return runVersion(rest)
	case "init":
		return runInit(rest)
	case "doctor":
		return runDoctor(rest)
	case "serve":
		return runServe(rest)
	case "contract":
		return runContract(rest)
	case "login":
		return runLogin(rest)
	case "app":
		return runApp(rest)
	case "diff":
		return runDiff(rest)
	case "sync":
		return runSync(rest)
	case "enroll":
		return runEnroll(rest)
	case "agent":
		return runAgent(rest)
	case "secret":
		return runSecret(rest)
	case "backup":
		return runBackup(rest)
	case "restore":
		return runRestore(rest)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Print(usage)
		return fmt.Errorf("HD0101: unknown subcommand %q", command)
	}
}

// config is the small amount of state `heimdall init` resolves once and
// `serve` reads back. The tenant id in particular must not be re-derived at
// boot: a name lookup that silently created a second tenant would split the
// installation's grants in half.
type config struct {
	ConfigVersion int    `json:"config_version"`
	TenantID      string `json:"tenant_id"`
	TenantName    string `json:"tenant_name"`
	Issuer        string `json:"issuer"`
	SesameDir     string `json:"sesame_deployment"`
	FyloRoot      string `json:"fylo_root"`
	// PublicURL is where agents connect. It is part of an enrollment token's
	// signature, so a token minted for one control plane cannot enrol against
	// another.
	PublicURL string `json:"public_url,omitempty"`
}

func deploymentDir(flagValue string) (string, error) {
	if flagValue != "" {
		return filepath.Abs(flagValue)
	}
	if fromEnv := os.Getenv("HD_DEPLOYMENT"); fromEnv != "" {
		return filepath.Abs(fromEnv)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("HD0102: resolve home directory: %w", err)
	}
	return filepath.Join(home, ".heimdall"), nil
}

func configPath(deployment string) string { return filepath.Join(deployment, "heimdall.json") }

func loadConfig(deployment string) (config, error) {
	raw, err := os.ReadFile(configPath(deployment))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config{}, fmt.Errorf("HD0103: %s is not initialized; run `heimdall init --deployment %s`", deployment, deployment)
		}
		return config{}, fmt.Errorf("HD0103: read config: %w", err)
	}
	var loaded config
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return config{}, fmt.Errorf("HD0104: parse config: %w", err)
	}
	// Paths are stored relative to the deployment so a backup restores to
	// any directory. Absolute entries from older configs are rebased when
	// their recorded location no longer exists — which is exactly the
	// restored-to-a-new-path case the DR drill exercises.
	loaded.SesameDir = rebase(deployment, loaded.SesameDir, "sesame")
	loaded.FyloRoot = rebase(deployment, loaded.FyloRoot, "fylo-root")
	return loaded, nil
}

func rebase(deployment, recorded, fallback string) string {
	if recorded == "" {
		return filepath.Join(deployment, fallback)
	}
	if !filepath.IsAbs(recorded) {
		return filepath.Join(deployment, recorded)
	}
	if _, err := os.Stat(recorded); err == nil {
		return recorded
	}
	moved := filepath.Join(deployment, filepath.Base(recorded))
	if _, err := os.Stat(moved); err == nil {
		return moved
	}
	return recorded
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func runVersion(args []string) error {
	output := "text"
	for i := 0; i < len(args); i++ {
		if args[i] == "--output" && i+1 < len(args) {
			output = args[i+1]
		}
	}
	if output == "json" {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{
			"name": "heimdall", "version": version,
		})
	}
	fmt.Println("heimdall " + version)
	return nil
}

// runContract regenerates the public API contract from the route table. The
// file under api/ is generated output: a test fails the build when the
// committed copy drifts from the routes it describes.
func runContract(args []string) error {
	var write string
	parseFlags(args, map[string]*string{"--write": &write})

	document, err := api.OpenAPI(version)
	if err != nil {
		return err
	}
	if write == "" {
		_, err := os.Stdout.Write(document)
		return err
	}
	if err := os.WriteFile(write, document, 0o644); err != nil {
		return fmt.Errorf("HD0117: write contract: %w", err)
	}
	fmt.Println("wrote " + write)

	// The capability matrix is generated beside the contract, from the same
	// Capabilities() answers plan-time validation enforces — so what the
	// docs promise and what the planner rejects cannot drift.
	adapters, _ := providers(nil)
	matrix := capabilityMatrix(adapters)
	matrixPath := filepath.Join(filepath.Dir(write), "capabilities.md")
	if err := os.WriteFile(matrixPath, []byte(matrix), 0o644); err != nil {
		return fmt.Errorf("HD0117: write capability matrix: %w", err)
	}
	fmt.Println("wrote " + matrixPath)
	return nil
}

// capabilityMatrix renders every adapter's answer for every feature.
func capabilityMatrix(adapters map[string]provider.Provider) string {
	names := make([]string, 0, len(adapters))
	for name := range adapters {
		names = append(names, name)
	}
	sort.Strings(names)

	var builder strings.Builder
	builder.WriteString("# Capability matrix\n\n")
	builder.WriteString("Generated by `heimdall contract` from each adapter's `Capabilities()` — the\n")
	builder.WriteString("same answers plan-time validation enforces. Do not edit.\n\n")
	builder.WriteString("| Feature |")
	for _, name := range names {
		builder.WriteString(" " + name + " |")
	}
	builder.WriteString("\n|---|")
	for range names {
		builder.WriteString("---|")
	}
	builder.WriteString("\n")

	for _, feature := range provider.Features {
		builder.WriteString("| " + string(feature) + " |")
		for _, name := range names {
			capabilities := adapters[name].Capabilities()
			support := capabilities.Support[feature]
			cell := string(support)
			if caveat := capabilities.Caveats[feature]; caveat != "" && support != provider.Full {
				cell += " — " + caveat
			}
			builder.WriteString(" " + cell + " |")
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

func runInit(args []string) error {
	var deploymentFlag, issuerFlag, tenantFlag, adminFlag string
	parseFlags(args, map[string]*string{
		"--deployment": &deploymentFlag,
		"--issuer":     &issuerFlag,
		"--tenant":     &tenantFlag,
		"--admin":      &adminFlag,
	})

	deployment, err := deploymentDir(deploymentFlag)
	if err != nil {
		return err
	}
	issuer := envOr("HD_ISSUER", issuerFlag)
	if issuer == "" {
		issuer = "https://localhost:8443"
	}
	// SESAME requires an absolute https issuer because it is baked into every
	// token it signs. Rejecting here names the flag; letting the engine reject
	// names only the engine.
	if !strings.HasPrefix(issuer, "https://") {
		return fmt.Errorf("HD0116: issuer %q must be an absolute https URL", issuer)
	}
	tenantName := envOr("HD_TENANT", tenantFlag)
	if tenantName == "" {
		tenantName = "heimdall"
	}

	if err := os.MkdirAll(deployment, 0o700); err != nil {
		return fmt.Errorf("HD0105: create deployment directory: %w", err)
	}
	sesameDir := filepath.Join(deployment, "sesame")

	// `sesame init` writes the ES256 signing key, the snapshot key, and the
	// sealed-secrets key into sesameDir/keys, which is deliberately outside
	// the FYLO root beside it. A FYLO snapshot alone restores nothing that
	// can verify a session, so the DR runbook backs up both or neither.
	if _, err := os.Stat(filepath.Join(sesameDir, "config.json")); errors.Is(err, os.ErrNotExist) {
		// SESAME pins the FYLO executable it drives, by absolute path, so an
		// upgrade of one cannot silently change the storage engine of the
		// other. Resolving it here keeps that pin honest.
		fyloBinary, err := resolveFylo()
		if err != nil {
			return err
		}
		if out, err := runSesame("init",
			"--deployment", sesameDir, "--fylo-binary", fyloBinary, "--issuer", issuer); err != nil {
			return fmt.Errorf("HD0106: sesame init: %w: %s", err, out)
		}
	}
	if out, err := runSesame("doctor", "--deployment", sesameDir); err != nil {
		return fmt.Errorf("HD0107: sesame doctor: %w: %s", err, out)
	}

	// HEIMDALL gets its own FYLO root beside SESAME's rather than sharing it:
	// FYLO takes an exclusive lock per root (EROOTLOCKED), so one root cannot
	// have two live engines. The deployment directory remains the single
	// backup and restore unit, now covering two roots plus the key material.
	fyloRoot := envOr("HD_FYLO_ROOT", filepath.Join(deployment, "fylo-root"))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := sesame.Start(ctx, sesame.Options{Deployment: sesameDir})
	if err != nil {
		return fmt.Errorf("HD0108: start sesame engine: %w", err)
	}
	defer func() { _ = client.Close() }()

	// TenantBootstrap is create-exactly-once and returns the existing tenant
	// on a repeat, which is what makes re-running init safe.
	bootstrap, err := client.TenantBootstrap(ctx, tenantName)
	if err != nil {
		return fmt.Errorf("HD0109: bootstrap tenant %q: %w", tenantName, err)
	}

	engine := auth.Adopt(client, bootstrap.Tenant.ID)
	if _, err := engine.SeedRoles(ctx); err != nil {
		return err
	}

	// The first administrator has to be created here, while init holds the
	// engine. Once `heimdall serve` is running it owns the only SESAME engine
	// and FYLO's exclusive root lock means the `sesame` CLI cannot run
	// alongside it — so without this there is no way in.
	oidcClients, err := ensureOIDCClients(ctx, engine, deployment)
	if err != nil {
		return err
	}
	_ = oidcClients

	administrator := ""
	if adminFlag != "" {
		administrator, err = bootstrapAdministrator(ctx, client, tenantName, adminFlag)
		if err != nil {
			return err
		}
	}

	// Collections are created before the config is written, so a config file
	// on disk always implies a usable store.
	storage, err := store.Open(fyloRoot, os.Getenv("FYLO_BINARY"))
	if err != nil {
		return err
	}
	if err := storage.Close(); err != nil {
		return fmt.Errorf("HD0110: close store: %w", err)
	}

	// Key material for agent enrollment: the CA that signs agent
	// certificates, the server certificate agents pin, and the token-signing
	// key. All 0600, all outside every FYLO root, all idempotent.
	publicURL := envOr("HD_PUBLIC_URL", "")
	material, err := enroll.Ensure(deployment, enroll.HostsFor(envOr("HD_ADDR", "127.0.0.1:8080"), hostOf(publicURL)))
	if err != nil {
		return err
	}

	written := config{
		ConfigVersion: 1,
		TenantID:      bootstrap.Tenant.ID,
		TenantName:    bootstrap.Tenant.Name,
		Issuer:        issuer,
		// Relative, so a backup restores to any directory.
		SesameDir: "sesame",
		FyloRoot:  "fylo-root",
		PublicURL: publicURL,
	}
	raw, err := json.MarshalIndent(written, "", "  ")
	if err != nil {
		return fmt.Errorf("HD0111: encode config: %w", err)
	}
	if err := os.WriteFile(configPath(deployment), append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("HD0111: write config: %w", err)
	}

	fmt.Printf("initialized %s\n  tenant   %s (%s)\n  sesame   %s\n  fylo     %s\n  keys     %s\n  roles    %s\n",
		deployment, written.TenantName, written.TenantID, sesameDir, written.FyloRoot,
		enroll.KeysDir(deployment), "viewer, operator, admin, owner")
	fmt.Printf("  agents   pin %s\n", material.ServerFingerprint())
	if administrator != "" {
		fmt.Printf("  admin    %s (%s)\n", adminFlag, administrator)
	} else {
		fmt.Println("\nNo administrator was created. Re-run with --admin NAME and HD_ADMIN_PASSWORD set;")
		fmt.Println("once `heimdall serve` is running it holds the only SESAME engine, and the")
		fmt.Println("`sesame` CLI cannot open the same FYLO root alongside it.")
	}
	return nil
}

// oidcClientsPath is where the device-grant client credential lives: beside
// the TLS keys, 0600, outside every FYLO root. The secret is returned by
// SESAME exactly once, at registration, which is why it is persisted at all.
func oidcClientsPath(deployment string) string {
	return filepath.Join(enroll.KeysDir(deployment), "oidc-client.json")
}

func loadOIDCClients(deployment string) (auth.OIDCClients, error) {
	var clients auth.OIDCClients
	raw, err := os.ReadFile(oidcClientsPath(deployment))
	if err != nil {
		return clients, err
	}
	return clients, json.Unmarshal(raw, &clients)
}

// ensureOIDCClients registers the device-grant client on first init and
// verifies the stored one on re-runs.
func ensureOIDCClients(ctx context.Context, engine *auth.Engine, deployment string) (auth.OIDCClients, error) {
	stored, _ := loadOIDCClients(deployment)
	clients, err := engine.EnsureDeviceClient(ctx, stored)
	if err != nil {
		return clients, err
	}
	encoded, err := json.MarshalIndent(clients, "", "  ")
	if err != nil {
		return clients, err
	}
	// Init calls this before the key material step has made keys/.
	if err := os.MkdirAll(enroll.KeysDir(deployment), 0o700); err != nil {
		return clients, fmt.Errorf("HD0150: create key directory: %w", err)
	}
	if err := os.WriteFile(oidcClientsPath(deployment), encoded, 0o600); err != nil {
		return clients, fmt.Errorf("HD0150: persist the OIDC client: %w", err)
	}
	return clients, nil
}

// bootstrapAdministrator creates the first principal and gives it the
// administrator grant SESAME's own admin.bootstrap defines. The password is
// read from the environment, never a flag, so it stays out of shell history
// and ps output.
func bootstrapAdministrator(ctx context.Context, client *sesame.Client, tenantName, name string) (string, error) {
	password := os.Getenv("HD_ADMIN_PASSWORD")
	if password == "" {
		return "", errors.New(
			"HD0119: set HD_ADMIN_PASSWORD in the environment to create an administrator; " +
				"HEIMDALL never accepts a password as a flag")
	}

	result, err := client.AdminBootstrap(ctx, tenantName, sesame.PrincipalIdentifier{
		Namespace: "username", Value: name,
	})
	if err != nil {
		return "", fmt.Errorf("HD0119: bootstrap administrator %q: %w", name, err)
	}
	// SetPassword is idempotent, so re-running init with the same
	// administrator rotates the password rather than failing.
	if err := client.SetPassword(ctx, result.Administrator.ID, password); err != nil {
		return "", fmt.Errorf("HD0119: set the administrator password: %w", err)
	}
	return result.Administrator.ID, nil
}

func runDoctor(args []string) error {
	var deploymentFlag string
	parseFlags(args, map[string]*string{"--deployment": &deploymentFlag})
	deployment, err := deploymentDir(deploymentFlag)
	if err != nil {
		return err
	}
	loaded, err := loadConfig(deployment)
	if err != nil {
		return err
	}

	// One verdict over all four binaries, because an operator debugging a
	// startup failure should not have to know which child broke.
	report := map[string]any{"deployment": deployment, "heimdall": version}
	verdict := "ok"

	sesameReport, err := runSesame("doctor", "--deployment", loaded.SesameDir)
	if err != nil {
		verdict, report["sesame"] = "failed", map[string]string{"status": "failed", "error": err.Error()}
	} else {
		var parsed map[string]any
		if json.Unmarshal(sesameReport, &parsed) == nil {
			report["sesame"] = parsed
		} else {
			verdict, report["sesame"] = "failed", map[string]string{"status": "unreadable"}
		}
	}

	storage, err := store.Open(loaded.FyloRoot, os.Getenv("FYLO_BINARY"))
	if err != nil {
		verdict, report["fylo"] = "failed", map[string]string{"status": "failed", "error": err.Error()}
	} else {
		report["fylo"] = map[string]any{"status": "ok", "root": storage.Root(), "collections": len(store.Collections)}
		_ = storage.Close()
	}

	if version, err := git.Available(context.Background()); err != nil {
		verdict, report["git"] = "failed", map[string]string{"status": "failed", "error": err.Error()}
	} else {
		report["git"] = map[string]string{"status": "ok", "version": version}
	}

	report["status"] = verdict
	_ = json.NewEncoder(os.Stdout).Encode(report)
	if verdict != "ok" {
		return errors.New("HD0112: doctor reported a failure")
	}
	return nil
}

func runServe(args []string) error {
	var deploymentFlag, addrFlag string
	standby := false
	for _, arg := range args {
		if arg == "--standby" {
			standby = true
		}
	}
	parseFlags(args, map[string]*string{
		"--deployment": &deploymentFlag,
		"--addr":       &addrFlag,
	})
	deployment, err := deploymentDir(deploymentFlag)
	if err != nil {
		return err
	}
	// Config typos fail before the store opens: a serve that dies on
	// HD_METRICS_RETENTION must not have taken the FYLO root lock first.
	retention, err := metricsRetention()
	if err != nil {
		return err
	}
	loaded, err := loadConfig(deployment)
	if err != nil {
		return err
	}
	addr := envOr("HD_ADDR", addrFlag)
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	// Standby: FYLO's exclusive root locks are the leader election — exactly
	// one process can hold a root (EROOTLOCKED) — so active/passive failover
	// is a standby probing until the active dies and the locks free. No
	// consensus protocol and no split brain: the storage engine's own
	// exclusivity is the arbiter, and both processes share the deployment
	// directory on block storage. `sesame doctor` is the probe, because
	// SESAME's root is the first lock startup takes.
	for standby {
		out, err := runSesame("doctor", "--deployment", loaded.SesameDir)
		if err == nil {
			logger.Info("standby: the deployment is free; taking over")
			break
		}
		if !strings.Contains(string(out), "EROOTLOCKED") && !strings.Contains(err.Error(), "EROOTLOCKED") {
			return fmt.Errorf("HD0113: sesame doctor: %w: %s", err, out)
		}
		logger.Info("standby: an active control plane holds the deployment; waiting", "retry_in", "5s")
		time.Sleep(5 * time.Second)
	}

	// 1. FYLO.
	storage, err := store.Open(loaded.FyloRoot, os.Getenv("FYLO_BINARY"))
	if err != nil {
		return err
	}
	defer func() { _ = storage.Close() }()

	// 2. sesame doctor, before the engine, so a broken deployment fails here
	//    rather than on the first authorized request.
	if out, err := runSesame("doctor", "--deployment", loaded.SesameDir); err != nil {
		return fmt.Errorf("HD0113: sesame doctor: %w: %s", err, out)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	engine, err := auth.Start(ctx, auth.Options{
		Deployment: loaded.SesameDir,
		TenantID:   loaded.TenantID,
		Stderr:     os.Stderr,
	})
	if err != nil {
		return err
	}
	defer func() { _ = engine.Close() }()

	// The device-grant client, if init registered one. An older deployment
	// without it still serves; only `heimdall login --device` is off.
	deviceEnabled := false
	if clients, err := loadOIDCClients(deployment); err == nil && clients.DeviceClientID != "" {
		engine.UseOIDCClients(clients)
		deviceEnabled = true
	}

	// 3. Key material and the agent rendezvous.
	material, err := enroll.Load(deployment)
	if err != nil {
		return err
	}
	dispatcher := dispatch.New(0)

	// 4. The reconcile engine and the adapters it drives.
	//
	// References resolve through the scheme-dispatched resolver: the local
	// sealed store, AWS Secrets Manager, Key Vault, or GCP Secret Manager.
	// Values exist inside one call and are never persisted.
	resolver := &hdsecrets.Resolver{Deployment: deployment}
	secrets := resolver.Resolve
	adapters, applyContexts := providers(secrets)
	reconciler := &reconcile.Engine{
		Store:        storage,
		Providers:    adapters,
		ApplyContext: applyContexts,
		Secrets:      secrets,
		Dispatcher:   dispatcher,
		CacheDir:     filepath.Join(deployment, "git"),
	}

	// The loop that syncs without a human. It reads each application's own
	// policy, so a deployment with no automated application does nothing.
	autoCtx, stopAuto := context.WithCancel(context.Background())
	defer stopAuto()
	registryEngine := &hdregistry.Engine{
		Store: storage, CacheDir: filepath.Join(deployment, "git"), Logger: logger,
	}
	auto := &reconcile.Auto{
		Engine: reconciler, Interval: autoInterval(), Logger: logger,
		RegistrySync: registryEngine.SyncIfBound,
	}
	go auto.Run(autoCtx)

	// The metrics pipeline: scrape → ring → minute rollups, bounded to a day
	// unless HD_METRICS_RETENTION extends it (up to two weeks).
	collector := &observe.Collector{Store: storage, Providers: adapters, Logger: logger, Retention: retention}
	go collector.Run(autoCtx)

	// Parked syncs drain when their agent reconnects, and the parking is
	// rebuilt from the store on restart — only the references were in
	// memory; the Pending operations are durable.
	dispatcher.SetDrain(reconciler.Resume, reconciler.Expire)
	if err := reconciler.Repark(); err != nil {
		return fmt.Errorf("HD0263: rebuild parked syncs: %w", err)
	}

	publicURL := loaded.PublicURL
	if publicURL == "" {
		publicURL = defaultPublicURL(addr, useTLS())
	}

	// 5. The listener, last.
	server := &api.Server{
		Engine:        engine,
		Audit:         auditor{store: storage, logger: logger},
		Version:       version,
		Store:         storage,
		Reconcile:     reconciler,
		Providers:     adapters,
		Dispatcher:    dispatcher,
		Material:      material,
		PublicURL:     publicURL,
		Auto:          auto,
		Registry:      registryEngine,
		Secrets:       secrets,
		Observe:       collector,
		DeviceEnabled: deviceEnabled,
	}
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           server.RateLimit(server.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
		// A long poll parks for up to 90s, so a write deadline shorter than
		// that would cut an idle agent off mid-wait.
		WriteTimeout: 3 * time.Minute,
	}
	if useTLS() {
		// Client certificates are requested and verified when offered, but
		// not required: this same listener serves browsers and the CLI, which
		// authenticate with a SESAME session instead.
		httpServer.TLSConfig = material.ServerTLSConfig()
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	logger.Info("heimdall serving",
		"addr", addr, "version", version, "tenant", loaded.TenantID,
		"tls", useTLS(), "public_url", publicURL,
		"fingerprint", material.ServerFingerprint())

	serve := httpServer.ListenAndServe
	if useTLS() {
		serve = func() error { return httpServer.ListenAndServeTLS("", "") }
	}
	if err := serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HD0114: listen: %w", err)
	}
	return nil
}

// autoInterval is how often the auto-sync loop looks. A minute is the
// ArgoCD default and the same tradeoff applies: shorter means more git and
// provider calls for drift that is usually not there.
func autoInterval() time.Duration {
	if parsed, err := time.ParseDuration(envOr("HD_SYNC_INTERVAL", "1m")); err == nil && parsed > 0 {
		return parsed
	}
	return time.Minute
}

// metricsRetention reads HD_METRICS_RETENTION. Unlike the sync interval, a
// bad value here fails startup rather than falling back: the consequence of
// a typo would be a week of history silently pruned to a day, discovered
// during exactly the incident the history was kept for.
func metricsRetention() (time.Duration, error) {
	raw := envOr("HD_METRICS_RETENTION", "")
	if raw == "" {
		return 0, nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("HD0395: HD_METRICS_RETENTION %q is not a duration (try 72h)", raw)
	}
	if parsed < observe.MinRetention || parsed > observe.MaxRetention {
		return 0, fmt.Errorf("HD0395: HD_METRICS_RETENTION %s is outside %s–%s; for longer history point a real time-series store at the workloads",
			parsed, observe.MinRetention, observe.MaxRetention)
	}
	return parsed, nil
}

// useTLS reports whether the listener should serve TLS. It is on by default:
// agent enrollment pins a certificate fingerprint, and there is no
// fingerprint without TLS. HD_TLS=false exists for a local development loop
// behind a terminating proxy.
func useTLS() bool { return envOr("HD_TLS", "true") != "false" }

// defaultPublicURL guesses where agents should connect when nothing said. A
// guess is fine for a local loop and wrong for a real deployment, which is
// why `heimdall enroll` refuses to mint a token without one configured.
func defaultPublicURL(addr string, tls bool) string {
	scheme := "http"
	if tls {
		scheme = "https"
	}
	if strings.HasPrefix(addr, ":") {
		return scheme + "://127.0.0.1" + addr
	}
	return scheme + "://" + addr
}

// hostOf extracts the host from a URL for certificate naming, ignoring
// anything malformed rather than failing init over it.
func hostOf(rawURL string) string {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://")
	host, _, found := strings.Cut(trimmed, ":")
	if !found {
		return trimmed
	}
	return host
}

// providers registers the adapters this build ships. Adding one is an edit
// here and a package under internal/provider/; nothing else in the control
// plane learns a provider name.
func providers(secrets func(context.Context, string) (string, error)) (
	map[string]provider.Provider,
	map[string]func(context.Context, reconcile.ApplyParams) context.Context,
) {
	dockerAdapter := &docker.Provider{SecretResolver: secrets}
	swarmAdapter := &docker.Swarm{SecretResolver: secrets}
	ecsAdapter := &ecs.Provider{SecretResolver: secrets}
	runAdapter := &cloudrun.Provider{SecretResolver: secrets}
	acaAdapter := &aca.Provider{SecretResolver: secrets}

	applyContext := func(ctx context.Context, params reconcile.ApplyParams) context.Context {
		return docker.WithApply(ctx, docker.ApplyOptions{
			Spec: params.Spec, Prune: params.Prune, Registries: params.Registries,
		})
	}
	return map[string]provider.Provider{
			dockerAdapter.Name(): dockerAdapter,
			swarmAdapter.Name():  swarmAdapter,
			ecsAdapter.Name():    ecsAdapter,
			runAdapter.Name():    runAdapter,
			acaAdapter.Name():    acaAdapter,
		}, map[string]func(context.Context, reconcile.ApplyParams) context.Context{
			dockerAdapter.Name(): applyContext,
			// Swarm shares the standalone adapter's apply-context shape.
			swarmAdapter.Name(): applyContext,
			ecsAdapter.Name(): func(ctx context.Context, params reconcile.ApplyParams) context.Context {
				return ecs.WithApply(ctx, ecs.ApplyOptions{
					Spec: params.Spec, Prune: params.Prune, Registries: params.Registries,
				})
			},
			runAdapter.Name(): func(ctx context.Context, params reconcile.ApplyParams) context.Context {
				return cloudrun.WithApply(ctx, cloudrun.ApplyOptions{
					Spec: params.Spec, Prune: params.Prune, Registries: params.Registries,
				})
			},
			acaAdapter.Name(): func(ctx context.Context, params reconcile.ApplyParams) context.Context {
				return aca.WithApply(ctx, aca.ApplyOptions{
					Spec: params.Spec, Prune: params.Prune, Registries: params.Registries,
				})
			},
		}
}

// auditor writes every authorization decision to hd-audit and mirrors it to
// the log. hd-audit is authoritative and append-only — unlike hd-livestate it
// is never rebuilt from operations, because an audit log you can regenerate
// is one you can rewrite.
type auditor struct {
	store  *store.Store
	logger *slog.Logger
}

func (a auditor) Record(entry api.Entry) {
	a.logger.Info("authorization",
		"principal_id", entry.PrincipalID,
		"action", entry.Action,
		"resource", entry.Resource,
		"outcome", entry.Outcome,
		"reason_code", entry.ReasonCode,
		"policy_version", entry.PolicyVersion,
		"decision_id", entry.DecisionID,
		"method", entry.Method,
		"path", entry.Path,
	)
	if a.store == nil {
		return
	}
	if _, err := store.In[store.AuditRecord](a.store, store.Audit).Put(store.AuditRecord{
		At:            time.Now().UTC(),
		PrincipalID:   entry.PrincipalID,
		Action:        entry.Action,
		Resource:      entry.Resource,
		Outcome:       entry.Outcome,
		ReasonCode:    entry.ReasonCode,
		PolicyVersion: entry.PolicyVersion,
		DecisionID:    entry.DecisionID,
		Method:        entry.Method,
		Path:          entry.Path,
	}); err != nil {
		// An unwritable audit log is a serious condition, but failing the
		// request would not make the record exist. It is logged loudly and
		// the operator's log pipeline is the backstop.
		a.logger.Error("audit write failed", "error", err, "action", entry.Action)
	}
}

// resolveFylo returns an absolute path to the FYLO executable.
func resolveFylo() (string, error) {
	if fromEnv := os.Getenv("FYLO_BINARY"); fromEnv != "" {
		return filepath.Abs(fromEnv)
	}
	resolved, err := exec.LookPath("fylo")
	if err != nil {
		return "", fmt.Errorf("HD0115: fylo executable not found; install it or set FYLO_BINARY: %w", err)
	}
	return filepath.Abs(resolved)
}

// runSesame invokes the engine's CLI directly. No shell: arguments are passed
// as argv so nothing in a path or an issuer can be interpreted.
func runSesame(args ...string) ([]byte, error) {
	binary := envOr("SESAME_BINARY", "sesame")
	command := exec.Command(binary, args...)
	command.Stderr = os.Stderr
	return command.Output()
}

// parseFlags is a deliberately tiny `--name value` reader. The flag package
// would need a FlagSet per subcommand for four string options; this is the
// whole requirement.
//
// ponytail: positional-free, string-only. Switch to flag.FlagSet the moment a
// subcommand needs a boolean or a repeated option.
func parseFlags(args []string, into map[string]*string) {
	for i := 0; i < len(args); i++ {
		target, ok := into[args[i]]
		if !ok || i+1 >= len(args) {
			continue
		}
		*target = args[i+1]
		i++
	}
}
