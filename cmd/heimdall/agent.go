package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"strings"

	"github.com/d31ma/heimdall/internal/agent"
	"github.com/d31ma/heimdall/internal/secrets"
	"github.com/d31ma/heimdall/internal/store"
)

// runEnroll asks the control plane for an enrollment token.
//
// It goes through the API rather than reading the deployment directly for two
// reasons: `heimdall serve` holds FYLO's exclusive root lock, so nothing else
// on the host can open the store while it runs; and minting a credential for
// a host is a mutating action that should be authorized and audited like any
// other.
func runEnroll(args []string) error {
	var deploymentFlag, targetFlag, lifetimeFlag string
	parseFlags(args, map[string]*string{
		"--deployment": &deploymentFlag,
		"--target":     &targetFlag,
		"--lifetime":   &lifetimeFlag,
	})

	deployment, err := deploymentDir(deploymentFlag)
	if err != nil {
		return err
	}
	if targetFlag == "" {
		return errors.New("HD0131: --target is required")
	}
	stored, err := loadSession(deployment)
	if err != nil {
		return err
	}

	body := map[string]string{}
	if lifetimeFlag != "" {
		body["lifetime"] = lifetimeFlag
	}

	var issued struct {
		Token       string `json:"token"`
		TargetID    string `json:"target_id"`
		TargetName  string `json:"target_name"`
		URL         string `json:"url"`
		Fingerprint string `json:"fingerprint"`
		ExpiresIn   string `json:"expires_in"`
	}
	if err := call(stored, http.MethodPost,
		"/api/v1/targets/"+targetFlag+"/enroll", body, &issued); err != nil {
		return err
	}

	fmt.Printf("Enrollment token for target %s (%s), valid for %s:\n\n",
		issued.TargetName, issued.TargetID, issued.ExpiresIn)
	fmt.Println("  " + issued.Token)
	fmt.Printf("\nOn the Docker host, run:\n\n  heimdall agent enroll --token '%s'\n  heimdall agent run\n\n",
		issued.Token)
	fmt.Printf("The token carries this control plane's certificate fingerprint:\n  %s\n", issued.Fingerprint)
	fmt.Println("The agent pins it before sending the token, so its first connection cannot be intercepted.")
	return nil
}

// agentDir is where an agent keeps its credentials on a host. It is not the
// control plane's deployment directory: an agent host usually has neither.
func agentDir(flagValue string) (string, error) {
	if flagValue != "" {
		return filepath.Abs(flagValue)
	}
	if fromEnv := os.Getenv("HD_AGENT_DIR"); fromEnv != "" {
		return filepath.Abs(fromEnv)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("HD0135: resolve home directory: %w", err)
	}
	return filepath.Join(home, ".heimdall-agent"), nil
}

func runAgent(args []string) error {
	if len(args) == 0 {
		return errors.New("HD0136: usage: heimdall agent enroll --token TOKEN | heimdall agent run")
	}
	command, rest := args[0], args[1:]

	var dirFlag, tokenFlag, dockerFlag string
	parseFlags(rest, map[string]*string{
		"--dir":    &dirFlag,
		"--token":  &tokenFlag,
		"--docker": &dockerFlag,
	})
	dir, err := agentDir(dirFlag)
	if err != nil {
		return err
	}

	switch command {
	case "enroll":
		// The token is accepted as a flag rather than an environment
		// variable, unlike a password: it is single-use, short-lived, and
		// bound to one target, and an operator pasting it into a terminal is
		// the workflow. It is not a standing credential.
		token := tokenFlag
		if token == "" {
			token = os.Getenv("HD_ENROLLMENT_TOKEN")
		}
		if token == "" {
			return errors.New("HD0137: --token is required (or set HD_ENROLLMENT_TOKEN)")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		credentials, err := agent.Enroll(ctx, dir, token)
		if err != nil {
			return err
		}
		fmt.Printf("enrolled as %s for target %s\n  credentials: %s\n",
			credentials.AgentID, credentials.TargetID, filepath.Join(dir, "agent.json"))
		fmt.Println("Run `heimdall agent run` to start serving work.")
		return nil

	case "run":
		credentials, err := agent.Load(dir)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		running := &agent.Agent{
			Credentials: credentials,
			Endpoint:    envOr("HD_DOCKER_ENDPOINT", dockerFlag),
			Logger:      slog.New(slog.NewJSONHandler(os.Stderr, nil)),
		}
		return running.Run(ctx)

	default:
		return fmt.Errorf("HD0136: unknown agent subcommand %q", command)
	}
}

// runSecret manages the local sealed store. The value arrives via the
// environment or stdin, never argv — argv is world-readable in ps.
func runSecret(args []string) error {
	if len(args) == 0 || args[0] != "set" {
		return errors.New("HD0138: usage: heimdall secret set --name NAME")
	}
	var deploymentFlag, nameFlag string
	parseFlags(args[1:], map[string]*string{
		"--deployment": &deploymentFlag,
		"--name":       &nameFlag,
	})
	deployment, err := deploymentDir(deploymentFlag)
	if err != nil {
		return err
	}
	if nameFlag == "" {
		return errors.New("HD0138: --name is required")
	}
	value := os.Getenv("HD_SECRET_VALUE")
	if value == "" {
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		if err != nil {
			return fmt.Errorf("HD0138: read value from stdin: %w", err)
		}
		value = strings.TrimRight(string(raw), "\n")
	}
	if value == "" {
		return errors.New("HD0138: provide the value in HD_SECRET_VALUE or on stdin")
	}
	if err := (&secrets.Resolver{Deployment: deployment}).Put(nameFlag, value); err != nil {
		return err
	}
	fmt.Printf("sealed local/%s\n", nameFlag)
	return nil
}

// runBackup archives one deployment directory: both FYLO roots, SESAME's
// keys, HEIMDALL's keys, and the sealed secrets — the whole DR unit ADR 0004
// defines. It refuses while `heimdall serve` runs, because a copy taken under
// a live writer is a corrupt copy that looks fine until restore day.
func runBackup(args []string) error {
	var deploymentFlag, outputFlag string
	parseFlags(args, map[string]*string{
		"--deployment": &deploymentFlag,
		"--output":     &outputFlag,
	})
	deployment, err := deploymentDir(deploymentFlag)
	if err != nil {
		return err
	}
	if outputFlag == "" {
		return errors.New("HD0139: --output FILE.tar.gz is required")
	}

	// The root lock is the liveness probe: if the store opens, nothing else
	// holds it. Opened exclusively and closed immediately — this is a check,
	// not a read.
	probe, err := store.Open(filepath.Join(deployment, "fylo-root"), os.Getenv("FYLO_BINARY"))
	if err != nil {
		return fmt.Errorf(
			"HD0139: the deployment is in use (is `heimdall serve` running?); stop it before backing up: %w", err)
	}
	_ = probe.Close()

	file, err := os.Create(outputFlag)
	if err != nil {
		return fmt.Errorf("HD0139: create archive: %w", err)
	}
	defer func() { _ = file.Close() }()

	compressor := gzip.NewWriter(file)
	archive := tar.NewWriter(compressor)

	err = filepath.WalkDir(deployment, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(deployment, path)
		if err != nil || relative == "." {
			return err
		}
		// The CLI session is a live credential for whoever runs the backup,
		// not part of the control plane's state.
		if relative == "session.json" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = source.Close() }()
		_, err = io.Copy(archive, source)
		return err
	})
	if err != nil {
		return fmt.Errorf("HD0139: archive %s: %w", deployment, err)
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := compressor.Close(); err != nil {
		return err
	}
	fmt.Printf("backed up %s to %s\n", deployment, outputFlag)
	fmt.Println("The archive holds key material and sealed secrets; store it accordingly.")
	return nil
}

// runRestore unpacks a backup into an empty deployment directory.
func runRestore(args []string) error {
	var deploymentFlag, inputFlag string
	parseFlags(args, map[string]*string{
		"--deployment": &deploymentFlag,
		"--input":      &inputFlag,
	})
	deployment, err := deploymentDir(deploymentFlag)
	if err != nil {
		return err
	}
	if inputFlag == "" {
		return errors.New("HD0139: --input FILE.tar.gz is required")
	}
	if entries, err := os.ReadDir(deployment); err == nil && len(entries) > 0 {
		return fmt.Errorf(
			"HD0139: %s is not empty; restore refuses to merge into an existing deployment", deployment)
	}

	file, err := os.Open(inputFlag)
	if err != nil {
		return fmt.Errorf("HD0139: open archive: %w", err)
	}
	defer func() { _ = file.Close() }()
	decompressor, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("HD0139: %s is not a gzip archive: %w", inputFlag, err)
	}
	archive := tar.NewReader(decompressor)

	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("HD0139: read archive: %w", err)
		}
		// Traversal-proof: every path must resolve inside the deployment.
		cleaned := filepath.Clean(header.Name)
		if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
			return fmt.Errorf("HD0139: archive entry %q escapes the deployment", header.Name)
		}
		destination := filepath.Join(deployment, cleaned)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destination, header.FileInfo().Mode()); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				return err
			}
			target, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, header.FileInfo().Mode())
			if err != nil {
				return err
			}
			// Bounded per file, so a crafted archive cannot fill the disk
			// through one entry.
			if _, err := io.Copy(target, io.LimitReader(archive, 1<<31)); err != nil {
				_ = target.Close()
				return err
			}
			if err := target.Close(); err != nil {
				return err
			}
		}
	}
	fmt.Printf("restored %s from %s\n", deployment, inputFlag)
	fmt.Println("Run `heimdall doctor` and then `heimdall serve`.")
	return nil
}
