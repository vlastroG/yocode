// Package integration runs yocode's commands against a real Ollama on a fixture repository.
// The scenarios need the integration build tag; see integration_test.go.
package integration

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vlastroG/yocode/internal/testutil"
)

// fixtureCommits are the subjects of the commits newRepo makes, oldest first.
var fixtureCommits = []string{"add greet package", "add app", "add usage docs"}

// newRepo copies the fixture project into an empty temp folder and commits it in three steps
// on branch main.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	err := filepath.WalkDir("testdata/fixture", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel("testdata/fixture", path)
		if err != nil {
			return err
		}
		if rel == "go.mod.txt" {
			rel = "go.mod"
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// a Windows checkout has CRLF; the repo gets the LF text git stores
		data = []byte(strings.ReplaceAll(string(data), "\r\n", "\n"))
		dst := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "init", "-b", "main")
	testutil.Git(t, dir, "config", "user.name", "test")
	testutil.Git(t, dir, "config", "user.email", "test@example.com")
	testutil.Git(t, dir, "config", "core.autocrlf", "false")
	testutil.Git(t, dir, "config", "commit.gpgsign", "false")
	for i, paths := range [][]string{{"go.mod", "README.md", "greet"}, {"cmd"}, {"docs"}} {
		testutil.Git(t, dir, append([]string{"add"}, paths...)...)
		testutil.Git(t, dir, "commit", "-m", fixtureCommits[i])
	}
	return dir
}

// changedFiles lists modified, added, deleted and untracked files, sorted.
func changedFiles(t *testing.T, repo string) []string {
	t.Helper()
	var files []string
	// porcelain lines start with a two-letter status that may begin with a space, so no trimming
	out, err := exec.Command("git", "-C", repo, "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) > 3 {
			files = append(files, line[3:])
		}
	}
	slices.Sort(files)
	return files
}

// gitOut runs git in repo and returns its trimmed output.
func gitOut(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestNewRepo(t *testing.T) {
	repo := newRepo(t)
	if got := gitOut(t, repo, "log", "--reverse", "--format=%s"); got != strings.Join(fixtureCommits, "\n") {
		t.Errorf("commits = %q", got)
	}
	if files := changedFiles(t, repo); len(files) != 0 {
		t.Errorf("changed files = %v, want none", files)
	}
	want := "README.md\ncmd/app/main.go\ndocs/usage.md\ngo.mod\ngreet/greet.go"
	if got := gitOut(t, repo, "ls-files"); got != want {
		t.Errorf("files = %q, want %q", got, want)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if files := changedFiles(t, repo); !slices.Equal(files, []string{"README.md"}) {
		t.Errorf("changed files = %v, want [README.md]", files)
	}
}
