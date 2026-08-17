package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d31ma/heimdall/internal/git"
)

// newUpstream builds a real repository to clone from. Driving the real git
// binary is the point: a fake would not catch an argv or environment mistake,
// which is where this package's bugs live.
func newUpstream(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}

	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = dir
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}

	runGit("init", "--initial-branch=main")
	if err := os.MkdirAll(filepath.Join(dir, "deploy"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deploy", "compose.yaml"),
		[]byte("services:\n  web:\n    image: nginx:1.27\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deploy", "compose.prod.yaml"),
		[]byte("services:\n  web:\n    image: nginx:1.27-alpine\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "initial commit")
	runGit("tag", "v1.0.0")
	return dir
}

func openMirror(t *testing.T, upstream string) git.Repo {
	t.Helper()
	repo := git.Repo{Dir: filepath.Join(t.TempDir(), "mirror.git"), URL: upstream}
	if err := git.Open(context.Background(), repo); err != nil {
		t.Fatalf("open: %v", err)
	}
	return repo
}

func TestOpenIsIdempotent(t *testing.T) {
	upstream := newUpstream(t)
	repo := openMirror(t, upstream)
	// Every reconcile calls Open; the second call must fetch, not fail.
	if err := git.Open(context.Background(), repo); err != nil {
		t.Fatalf("second open: %v", err)
	}
}

func TestResolveBranchTagAndSHA(t *testing.T) {
	repo := openMirror(t, newUpstream(t))
	ctx := context.Background()

	branch, err := git.Resolve(ctx, repo, "main")
	if err != nil {
		t.Fatalf("resolve main: %v", err)
	}
	if len(branch.SHA) != 40 {
		t.Fatalf("sha = %q, want a full object id", branch.SHA)
	}
	if branch.Message != "initial commit" || branch.Author == "" || branch.Committed.IsZero() {
		t.Errorf("commit metadata incomplete: %+v", branch)
	}

	// An annotated or lightweight tag must resolve to the commit, not the tag
	// object, or the revision id would not match the branch's.
	tag, err := git.Resolve(ctx, repo, "v1.0.0")
	if err != nil {
		t.Fatalf("resolve tag: %v", err)
	}
	if tag.SHA != branch.SHA {
		t.Errorf("tag resolved to %s, branch to %s", tag.SHA, branch.SHA)
	}

	if bySHA, err := git.Resolve(ctx, repo, branch.SHA); err != nil || bySHA.SHA != branch.SHA {
		t.Errorf("resolving a sha failed: %v %+v", err, bySHA)
	}
}

func TestResolveRejectsOptionInjection(t *testing.T) {
	repo := openMirror(t, newUpstream(t))
	for _, ref := range []string{"--upload-pack=touch /tmp/pwned", "-x", "a ref with spaces", ""} {
		if ref == "" {
			continue // empty means HEAD by design
		}
		if _, err := git.Resolve(context.Background(), repo, ref); err == nil {
			t.Errorf("accepted ref %q", ref)
		}
	}
}

func TestReadFileAtCommit(t *testing.T) {
	repo := openMirror(t, newUpstream(t))
	ctx := context.Background()
	commit, err := git.Resolve(ctx, repo, "main")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	body, err := git.ReadFile(ctx, repo, commit.SHA, "deploy/compose.yaml")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "nginx:1.27") {
		t.Fatalf("unexpected content: %q", body)
	}
}

// TestPathTraversalIsRefused matters because the path comes from an
// application document, which an operator with app:update controls.
func TestPathTraversalIsRefused(t *testing.T) {
	repo := openMirror(t, newUpstream(t))
	ctx := context.Background()
	commit, _ := git.Resolve(ctx, repo, "main")

	for _, path := range []string{"../etc/passwd", "/etc/passwd", "deploy/../../escape", "--output=/tmp/x"} {
		if _, err := git.ReadFile(ctx, repo, commit.SHA, path); err == nil {
			t.Errorf("read a path that escapes the repository: %q", path)
		}
	}
}

func TestListDirFindsOverlays(t *testing.T) {
	repo := openMirror(t, newUpstream(t))
	ctx := context.Background()
	commit, _ := git.Resolve(ctx, repo, "main")

	names, err := git.ListDir(ctx, repo, commit.SHA, "deploy")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	joined := strings.Join(names, " ")
	if !strings.Contains(joined, "compose.yaml") || !strings.Contains(joined, "compose.prod.yaml") {
		t.Fatalf("overlay discovery missed a file: %v", names)
	}
}

func TestUnsignedCommitFailsVerification(t *testing.T) {
	repo := openMirror(t, newUpstream(t))
	ctx := context.Background()
	commit, _ := git.Resolve(ctx, repo, "main")

	// The fixture is unsigned, so the optional gate must refuse it.
	if err := git.VerifySignature(ctx, repo, commit.SHA); err == nil {
		t.Fatal("an unsigned commit passed signature verification")
	}
}

func TestOpenRejectsAnOptionLikeURL(t *testing.T) {
	err := git.Open(context.Background(), git.Repo{Dir: t.TempDir(), URL: "--upload-pack=evil"})
	if err == nil {
		t.Fatal("accepted a URL that git would read as a flag")
	}
}
