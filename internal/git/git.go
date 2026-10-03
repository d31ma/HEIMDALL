// Package git resolves a repository reference to a commit and reads files out
// of it.
//
// It drives the `git` executable directly, with arguments passed as argv and
// never through a shell. The alternative — a pure-Go git implementation — is a
// large dependency to reimplement something every host running a deployment
// tool already has, and it would not support the credential helpers and SSH
// configuration operators already rely on.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// maxOutputBytes bounds what a single git invocation may return. A repository
// is untrusted input.
const maxOutputBytes = 8 << 20

// defaultTimeout bounds a clone or fetch. Git over the network is the slowest
// thing in a sync, and an unbounded one wedges a reconcile worker.
const defaultTimeout = 3 * time.Minute

// Repo is a local mirror of one remote repository.
type Repo struct {
	// Dir is the local cache directory. It holds a bare mirror, so many
	// revisions can be read without a working tree per revision.
	Dir string
	URL string
	// Env supplies credentials to git. It is passed to the child process and
	// never logged. Credentials come from the environment, never from flags,
	// so they stay out of shell history and ps output.
	Env []string
	// Timeout bounds one git invocation. Zero uses defaultTimeout.
	Timeout time.Duration
}

// Commit is a resolved revision.
type Commit struct {
	SHA       string    `json:"sha"`
	Ref       string    `json:"ref"`
	Author    string    `json:"author"`
	Message   string    `json:"message"`
	Committed time.Time `json:"committed_at"`
	// Signed reports whether the commit carries a good signature. It is only
	// meaningful when signature verification was requested.
	Signed bool `json:"signed"`
}

var (
	// shaPattern matches a full or abbreviated object id.
	shaPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	// refPattern bounds what may be passed to git as a ref. Refusing here
	// keeps a repository document from smuggling an option into argv — a
	// value starting with '-' would otherwise be read as a flag.
	refPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$`)
)

// Open prepares a local mirror, cloning it on first use and fetching after.
// It is idempotent and safe to call on every reconcile.
func Open(ctx context.Context, repo Repo) error {
	if repo.URL == "" {
		return errors.New("HD0230: a repository URL is required")
	}
	if repo.Dir == "" {
		return errors.New("HD0230: a cache directory is required")
	}
	if strings.HasPrefix(repo.URL, "-") {
		return fmt.Errorf("HD0231: repository URL %q may not begin with '-'", repo.URL)
	}

	if _, err := os.Stat(filepath.Join(repo.Dir, "HEAD")); err == nil {
		return fetch(ctx, repo)
	}
	if err := os.MkdirAll(filepath.Dir(repo.Dir), 0o700); err != nil {
		return fmt.Errorf("HD0232: create cache directory: %w", err)
	}
	// A mirror clone: no working tree, every ref, and cheap to update.
	_, err := run(ctx, repo, "", "clone", "--mirror", "--", repo.URL, repo.Dir)
	if err != nil {
		return fmt.Errorf("HD0233: clone %s: %w", repo.URL, err)
	}
	return nil
}

func fetch(ctx context.Context, repo Repo) error {
	// --prune so a deleted branch stops resolving, rather than pinning a sync
	// to a ref that no longer exists upstream.
	if _, err := run(ctx, repo, repo.Dir, "fetch", "--prune", "--tags", "origin"); err != nil {
		return fmt.Errorf("HD0234: fetch %s: %w", repo.URL, err)
	}
	return nil
}

// Resolve turns a ref — a branch, a tag, or a commit id — into a commit.
func Resolve(ctx context.Context, repo Repo, ref string) (Commit, error) {
	if ref == "" {
		ref = "HEAD"
	}
	if ref != "HEAD" && !refPattern.MatchString(ref) && !shaPattern.MatchString(ref) {
		return Commit{}, fmt.Errorf("HD0235: %q is not a valid ref", ref)
	}

	// A trailing ^{commit} makes an annotated tag resolve to the commit it
	// points at rather than to the tag object.
	out, err := run(ctx, repo, repo.Dir, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return Commit{}, fmt.Errorf("HD0236: resolve %q: %w", ref, err)
	}
	sha := strings.TrimSpace(out)

	// %x1f is a unit separator: it cannot appear in a commit message, so
	// splitting on it is safe where splitting on a newline would not be.
	details, err := run(ctx, repo, repo.Dir, "show", "--no-patch",
		"--format=%an <%ae>%x1f%cI%x1f%s", sha)
	if err != nil {
		return Commit{}, fmt.Errorf("HD0237: describe %s: %w", sha, err)
	}
	fields := strings.SplitN(strings.TrimSpace(details), "\x1f", 3)
	commit := Commit{SHA: sha, Ref: ref}
	if len(fields) == 3 {
		commit.Author = fields[0]
		commit.Committed, _ = time.Parse(time.RFC3339, fields[1])
		commit.Message = fields[2]
	}
	return commit, nil
}

// VerifySignature reports whether a commit carries a good signature. It is an
// optional per-repository gate: a repository that requires it refuses to
// deploy an unsigned commit.
func VerifySignature(ctx context.Context, repo Repo, sha string) error {
	if !shaPattern.MatchString(sha) {
		return fmt.Errorf("HD0238: %q is not a commit id", sha)
	}
	if _, err := run(ctx, repo, repo.Dir, "verify-commit", sha); err != nil {
		return fmt.Errorf("HD0239: commit %s has no good signature: %w", sha, err)
	}
	return nil
}

// ReadFile reads one path at one commit, without checking anything out. A
// bare mirror plus `git show` is both faster than a worktree and immune to
// leftover state from a previous sync.
func ReadFile(ctx context.Context, repo Repo, sha, path string) ([]byte, error) {
	if !shaPattern.MatchString(sha) {
		return nil, fmt.Errorf("HD0238: %q is not a commit id", sha)
	}
	clean, err := safePath(path)
	if err != nil {
		return nil, err
	}
	out, err := run(ctx, repo, repo.Dir, "show", sha+":"+clean)
	if err != nil {
		return nil, fmt.Errorf("HD0240: read %s at %s: %w", clean, sha[:7], err)
	}
	return []byte(out), nil
}

// ListDir lists the entries directly under a directory at a commit. It is how
// overlay discovery finds compose.<env>.yaml without guessing.
func ListDir(ctx context.Context, repo Repo, sha, dir string) ([]string, error) {
	if !shaPattern.MatchString(sha) {
		return nil, fmt.Errorf("HD0238: %q is not a commit id", sha)
	}
	target := ""
	if dir != "" && dir != "." {
		clean, err := safePath(dir)
		if err != nil {
			return nil, err
		}
		target = clean + "/"
	}
	out, err := run(ctx, repo, repo.Dir, "ls-tree", "--name-only", sha, target)
	if err != nil {
		return nil, fmt.Errorf("HD0241: list %q at %s: %w", dir, sha[:7], err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// safePath rejects anything that could escape the repository or be read as an
// option. A repository path comes from an application document, which an
// operator with app:update can set.
func safePath(path string) (string, error) {
	clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(path)), "./")
	switch {
	case clean == "" || clean == ".":
		return "", fmt.Errorf("HD0242: an empty path is not readable")
	case strings.HasPrefix(clean, "/"):
		return "", fmt.Errorf("HD0242: %q must be relative to the repository root", path)
	case strings.HasPrefix(clean, ".."), strings.Contains(clean, "/../"):
		return "", fmt.Errorf("HD0242: %q escapes the repository", path)
	case strings.HasPrefix(clean, "-"):
		return "", fmt.Errorf("HD0242: %q may not begin with '-'", path)
	}
	return clean, nil
}

// run invokes git with argv, never a shell.
func run(ctx context.Context, repo Repo, dir string, args ...string) (string, error) {
	timeout := repo.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir

	// A minimal, explicit environment. Inheriting the control plane's would
	// let an operator's shell configuration change what a sync deploys, and
	// would expose unrelated credentials to the child.
	command.Env = append([]string{
		"GIT_TERMINAL_PROMPT=0", // never block a reconcile on a password prompt
		"GIT_CONFIG_NOSYSTEM=1", // ignore host-wide config
		"GIT_CONFIG_GLOBAL=/dev/null",
		"HOME=" + os.TempDir(),
		"PATH=" + os.Getenv("PATH"),
	}, repo.Env...)

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("git %s timed out after %s", args[0], timeout)
		}
		// git's stderr is the actionable message; the exit status is not.
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", errors.New(message)
	}
	if stdout.Len() > maxOutputBytes {
		return "", fmt.Errorf("git %s returned %d bytes, over the %d byte limit",
			args[0], stdout.Len(), maxOutputBytes)
	}
	return stdout.String(), nil
}

// Available reports whether a usable git executable is on PATH, so `heimdall
// doctor` can say so plainly instead of the first sync failing.
func Available(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("HD0243: git is not on PATH: %w", err)
	}
	out, err := run(ctx, Repo{Timeout: 10 * time.Second}, "", "version")
	if err != nil {
		return "", fmt.Errorf("HD0243: git is unusable: %w", err)
	}
	return strings.TrimSpace(out), nil
}
