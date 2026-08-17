// Package secrets resolves ${secret:...} references at apply time.
//
// A reference names its store with a scheme:
//
//	local/<name>                    — the deployment's sealed store
//	aws-sm/<region>/<name>          — AWS Secrets Manager
//	azure-kv/<vault>/<name>         — Azure Key Vault
//	gcp-sm/<project>/<name>         — GCP Secret Manager
//	sops/<path>#<key>               — a SOPS-encrypted file in the app's own
//	                                  repo, read at the applying revision
//
// Values exist for the duration of one call and are never persisted, logged,
// or returned by any route — the same rule every other credential in the
// system lives under. An unrecognised scheme is refused with the schemes
// named, because a typo that silently resolved from the wrong store would be
// a secret from the wrong environment inside a production container.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	secretsmanager "github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"google.golang.org/api/option"
	gcpsm "google.golang.org/api/secretmanager/v1"
)

// Resolver dispatches a reference to its store.
type Resolver struct {
	// Deployment is where the local sealed store lives.
	Deployment string

	// Overrides for tests: fake endpoints and static credentials.
	AWSEndpoint     string
	AWSCredentials  aws.CredentialsProvider
	AzureEndpoint   string
	AzureCredential azcore.TokenCredential
	GCPEndpoint     string
	GCPNoAuth       bool
}

// Resolve is the one entry point; its signature matches what the reconciler
// and every adapter already accept.
func (r *Resolver) Resolve(ctx context.Context, ref string) (string, error) {
	scheme, rest, found := strings.Cut(ref, "/")
	if !found {
		return "", fmt.Errorf(
			"HD0118: secret reference %q names no store; use local/, aws-sm/, azure-kv/, gcp-sm/, or sops/", ref)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	switch scheme {
	case "local":
		return r.local(rest)
	case "aws-sm":
		return r.awsSecretsManager(ctx, rest)
	case "azure-kv":
		return r.azureKeyVault(ctx, rest)
	case "gcp-sm":
		return r.gcpSecretManager(ctx, rest)
	case "sops":
		return r.sops(ctx, rest)
	default:
		return "", fmt.Errorf(
			"HD0118: unknown secret store %q in %q; use local/, aws-sm/, azure-kv/, gcp-sm/, or sops/", scheme, ref)
	}
}

// --- SOPS: encrypted files in the application's own repo -------------------

// Source is how a sops reference reads its ciphertext: from the applying
// revision of the application's repository, app-relative, via the reconcile
// engine's git mirror. It travels in the context because resolution happens
// deep inside an adapter's apply, and the revision is the apply's, not the
// resolver's.
type Source struct {
	Read func(ctx context.Context, path string) ([]byte, error)
}

type sourceKey struct{}

// WithSource attaches the applying revision's file reader for sops
// references. The reconcile engine calls this once per apply.
func WithSource(ctx context.Context, source Source) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}

// sops decrypts a SOPS-encrypted file from the app's repo at the applying
// revision, by driving the real sops binary — never through a shell, with
// the plaintext crossing only an in-process pipe. Only the ciphertext ever
// touches disk. The reference is sops/<path>#<key> for one value out of an
// encrypted YAML/JSON file, or sops/<path> for the whole decrypted file
// (a PEM bundle, say). Key material is whatever SOPS understands from the
// environment (age, PGP, or a cloud KMS); if <deployment>/keys/age.key
// exists it is offered as SOPS_AGE_KEY_FILE, which puts the age key inside
// the deployment directory — the backup unit — beside every other key.
func (r *Resolver) sops(ctx context.Context, rest string) (string, error) {
	relPath, key, _ := strings.Cut(rest, "#")
	if relPath == "" {
		return "", fmt.Errorf("HD0162: sops reference needs sops/<path-in-app>#<key>")
	}
	if strings.Contains(relPath, "..") || strings.HasPrefix(relPath, "/") {
		return "", fmt.Errorf("HD0162: sops path %q must be relative to the application directory", relPath)
	}
	if strings.ContainsAny(key, `"[]`) {
		return "", fmt.Errorf("HD0162: sops key %q may not contain quotes or brackets", key)
	}

	source, ok := ctx.Value(sourceKey{}).(Source)
	if !ok || source.Read == nil {
		return "", fmt.Errorf(
			"HD0162: a sops reference resolves only during an apply, where the revision pins the ciphertext")
	}
	ciphertext, err := source.Read(ctx, relPath)
	if err != nil {
		return "", fmt.Errorf("HD0162: read sops file %q at the applying revision: %w", relPath, err)
	}

	// sops infers the format from the extension, so the temporary file keeps
	// it. The file holds ciphertext only; plaintext exists in the pipe.
	temp, err := os.CreateTemp("", "hd-sops-*"+filepath.Ext(relPath))
	if err != nil {
		return "", fmt.Errorf("HD0162: temp file for sops: %w", err)
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if _, err := temp.Write(ciphertext); err != nil {
		_ = temp.Close()
		return "", fmt.Errorf("HD0162: write sops ciphertext: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", err
	}

	arguments := []string{"--decrypt"}
	if key != "" {
		arguments = append(arguments, "--extract", `["`+key+`"]`)
	}
	arguments = append(arguments, temp.Name())

	command := exec.CommandContext(ctx, "sops", arguments...)
	command.Env = r.sopsEnvironment()
	var stderr strings.Builder
	command.Stderr = &stderr
	plaintext, err := command.Output()
	if err != nil {
		// sops writes its own diagnostics to stderr; the plaintext never
		// appears there. Bounded so a hostile file cannot flood the error.
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 512 {
			detail = detail[:512]
		}
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("HD0162: sops could not decrypt %q: %s", relPath, detail)
	}
	if len(plaintext) > 1<<20 {
		return "", fmt.Errorf("HD0162: decrypted value of %q exceeds 1MiB", relPath)
	}
	return string(plaintext), nil
}

// sopsEnvironment passes the process environment through and offers the
// deployment's age key when the operator has not configured SOPS themselves.
func (r *Resolver) sopsEnvironment() []string {
	environment := os.Environ()
	if os.Getenv("SOPS_AGE_KEY_FILE") != "" || os.Getenv("SOPS_AGE_KEY") != "" {
		return environment
	}
	ageKey := filepath.Join(r.Deployment, "keys", "age.key")
	if _, err := os.Stat(ageKey); err == nil {
		environment = append(environment, "SOPS_AGE_KEY_FILE="+ageKey)
	}
	return environment
}

// --- local sealed store ----------------------------------------------------

// The local store is one AES-256-GCM sealed file per secret under
// <deployment>/secrets, keyed by <deployment>/keys/secrets.key. It exists so
// a deployment with no cloud has somewhere honest to keep a registry password
// — encrypted at rest, outside every FYLO root, covered by the same backup
// unit as the TLS keys.

func (r *Resolver) keyPath() string {
	return filepath.Join(r.Deployment, "keys", "secrets.key")
}

func (r *Resolver) sealedPath(name string) (string, error) {
	if strings.Contains(name, "..") || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("HD0118: local secret name %q may not contain path separators", name)
	}
	return filepath.Join(r.Deployment, "secrets", name+".sealed"), nil
}

func (r *Resolver) sealKey() ([]byte, error) {
	if key, err := os.ReadFile(r.keyPath()); err == nil && len(key) == 32 {
		return key, nil
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(r.keyPath()), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(r.keyPath(), key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// Put seals a value. It is the write half `heimdall secret set` uses; the
// value arrives via stdin or environment, never argv.
func (r *Resolver) Put(name, value string) error {
	key, err := r.sealKey()
	if err != nil {
		return fmt.Errorf("HD0118: local seal key: %w", err)
	}
	path, err := r.sealedPath(name)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	sealed, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, sealed.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(nonce, sealed.Seal(nil, nonce, []byte(value), nil)...), 0o600)
}

func (r *Resolver) local(name string) (string, error) {
	key, err := os.ReadFile(r.keyPath())
	if err != nil {
		return "", fmt.Errorf("HD0118: no local secret store exists yet; `heimdall secret set` creates it")
	}
	path, err := r.sealedPath(name)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("HD0118: no local secret %q", name)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	sealed, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < sealed.NonceSize() {
		return "", fmt.Errorf("HD0118: local secret %q is truncated", name)
	}
	value, err := sealed.Open(nil, raw[:sealed.NonceSize()], raw[sealed.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("HD0118: local secret %q does not unseal; the key and the file disagree", name)
	}
	return string(value), nil
}

// --- cloud stores ----------------------------------------------------------

func (r *Resolver) awsSecretsManager(ctx context.Context, rest string) (string, error) {
	region, name, found := strings.Cut(rest, "/")
	if !found {
		return "", fmt.Errorf("HD0118: aws-sm reference needs aws-sm/<region>/<name>")
	}
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if r.AWSCredentials != nil {
		options = append(options, awsconfig.WithCredentialsProvider(r.AWSCredentials))
	}
	configuration, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return "", fmt.Errorf("HD0118: AWS configuration: %w", err)
	}
	client := secretsmanager.NewFromConfig(configuration, func(o *secretsmanager.Options) {
		if r.AWSEndpoint != "" {
			o.BaseEndpoint = aws.String(r.AWSEndpoint)
		}
	})
	value, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(name),
	})
	if err != nil {
		return "", fmt.Errorf("HD0118: AWS Secrets Manager %q: %w", name, err)
	}
	if value.SecretString != nil {
		return *value.SecretString, nil
	}
	return string(value.SecretBinary), nil
}

func (r *Resolver) azureKeyVault(ctx context.Context, rest string) (string, error) {
	vault, name, found := strings.Cut(rest, "/")
	if !found {
		return "", fmt.Errorf("HD0118: azure-kv reference needs azure-kv/<vault>/<name>")
	}
	credential := r.AzureCredential
	if credential == nil {
		chain, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return "", fmt.Errorf("HD0118: Azure credential: %w", err)
		}
		credential = chain
	}
	vaultURL := "https://" + vault + ".vault.azure.net"
	if r.AzureEndpoint != "" {
		vaultURL = r.AzureEndpoint
	}
	client, err := azsecrets.NewClient(vaultURL, credential, nil)
	if err != nil {
		return "", fmt.Errorf("HD0118: Key Vault client: %w", err)
	}
	value, err := client.GetSecret(ctx, name, "", nil)
	if err != nil {
		return "", fmt.Errorf("HD0118: Key Vault %q: %w", name, err)
	}
	if value.Value == nil {
		return "", fmt.Errorf("HD0118: Key Vault %q has no value", name)
	}
	return *value.Value, nil
}

func (r *Resolver) gcpSecretManager(ctx context.Context, rest string) (string, error) {
	project, name, found := strings.Cut(rest, "/")
	if !found {
		return "", fmt.Errorf("HD0118: gcp-sm reference needs gcp-sm/<project>/<name>")
	}
	options := []option.ClientOption{}
	if r.GCPEndpoint != "" {
		options = append(options, option.WithEndpoint(r.GCPEndpoint))
	}
	if r.GCPNoAuth {
		options = append(options, option.WithoutAuthentication())
	}
	service, err := gcpsm.NewService(ctx, options...)
	if err != nil {
		return "", fmt.Errorf("HD0118: GCP Secret Manager client: %w", err)
	}
	resource := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", project, name)
	value, err := service.Projects.Secrets.Versions.Access(resource).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("HD0118: GCP Secret Manager %q: %w", name, err)
	}
	if value.Payload == nil {
		return "", fmt.Errorf("HD0118: GCP secret %q has no payload", name)
	}
	decoded, err := decodeBase64(value.Payload.Data)
	if err != nil {
		return "", fmt.Errorf("HD0118: GCP secret %q payload: %w", name, err)
	}
	return decoded, nil
}

func decodeBase64(data string) (string, error) {
	var out []byte
	if err := json.Unmarshal([]byte(`"`+data+`"`), &out); err != nil {
		return "", err
	}
	return string(out), nil
}
