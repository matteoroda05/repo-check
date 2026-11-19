package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCheckFork(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is required to create and serve the local fixtures")
	}
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	fork := filepath.Join(root, "fork")
	local := filepath.Join(root, "local")

	runGit(t, root, "init", "-b", "trunk", parent)
	runGit(t, parent, "commit", "--allow-empty", "-m", "base")
	runGit(t, root, "clone", "-b", "trunk", parent, fork)
	runGit(t, fork, "checkout", "-b", "feature/login")
	runGit(t, fork, "commit", "--allow-empty", "-m", "feature base")
	runGit(t, root, "clone", "-b", "feature/login", fork, local)
	runGit(t, local, "remote", "add", "upstream", "../parent")
	runGit(t, local, "commit", "--allow-empty", "-m", "local work")
	runGit(t, fork, "commit", "--allow-empty", "-m", "remote work")
	runGit(t, parent, "commit", "--allow-empty", "-m", "new trunk commit")
	runGit(t, fork, "branch", "-m", "trunk", "main")
	runGit(t, fork, "checkout", "main")
	runGit(t, local, "update-ref", "refs/remotes/upstream/feature/login", "HEAD")

	draft := filepath.Join(local, "draft.txt")
	if err := os.WriteFile(draft, []byte("unfinished work"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, local, "add", "draft.txt")
	if err := os.WriteFile(draft, []byte("unfinished work with new edits"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "notes.txt"), []byte("untracked work"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeHead := runGit(t, local, "rev-parse", "HEAD")
	beforeFiles := runGit(t, local, "status", "--porcelain")
	repo, err := Open(local)
	if err != nil {
		t.Fatal(err)
	}
	status := repo.Check(context.Background())
	if status.Err != nil || status.Head != "feature/login" || len(status.Remotes) != 2 {
		t.Fatalf("unexpected status: %+v", status)
	}
	assertRemote(t, status.Remotes[0], "origin", []Comparison{
		{Kind: "Matching branch", Target: "origin/feature/login", Tracking: true, Relationship: Diverged},
		{Kind: "Default branch", Target: "origin/main", Relationship: Ahead},
	})
	assertRemote(t, status.Remotes[1], "upstream", []Comparison{
		{Kind: "Matching branch", Target: "upstream/feature/login", Relationship: Absent},
		{Kind: "Default branch", Target: "upstream/trunk", Relationship: Diverged},
	})
	if runGit(t, local, "rev-parse", "refs/remotes/origin/feature/login") != runGit(t, fork, "rev-parse", "feature/login") {
		t.Fatal("fetch did not refresh origin/feature/login")
	}
	if runGit(t, local, "rev-parse", "HEAD") != beforeHead || runGit(t, local, "status", "--porcelain") != beforeFiles {
		t.Fatal("check changed the checked-out commit or working tree")
	}

	runGit(t, local, "branch", "--set-upstream-to=origin/main", "feature/login")
	status = repo.Check(context.Background())
	assertRemote(t, status.Remotes[0], "origin", []Comparison{
		{Kind: "Matching branch", Target: "origin/feature/login", Relationship: Diverged},
		{Kind: "Upstream", Target: "origin/main", Tracking: true, Relationship: Ahead},
		{Kind: "Default branch", Target: "origin/main", Relationship: Ahead},
	})
}

func assertRemote(t *testing.T, got RemoteStatus, name string, want []Comparison) {
	t.Helper()
	if got.Name != name || got.Err != nil || len(got.Comparisons) != len(want) {
		t.Fatalf("unexpected remote status: %+v", got)
	}
	for i, item := range got.Comparisons {
		if item != want[i] {
			t.Errorf("%s comparison %d = %+v, want %+v", name, i, item, want[i])
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	options := []string{"-C", dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
		"-c", "commit.gpgSign=false", "-c", "core.hooksPath=/dev/null"}
	cmd := exec.Command("git", append(options, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
