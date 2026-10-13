// Package gittools runs read-only git and file operations inside a project directory.
package gittools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// maxOutput keeps tool results small enough for the model's context window.
const maxOutput = 20000

type Repo struct {
	Dir string
}

func (r Repo) Status(ctx context.Context) (string, error) {
	return r.git(ctx, "status", "--short", "--branch")
}

// Log shows commits of ref (a branch, commit or range such as main..feature; empty = HEAD),
// optionally limited to path.
func (r Repo) Log(ctx context.Context, n int, ref, path string) (string, error) {
	if err := checkRef(ref); err != nil {
		return "", err
	}
	if n <= 0 {
		n = 20
	}
	n = min(n, 200)
	args := []string{"log", "-n", strconv.Itoa(n), "--date=short", "--pretty=format:%h %ad %an: %s"}
	if ref != "" {
		args = append(args, ref)
	}
	if path != "" {
		p, err := relPath(path)
		if err != nil {
			return "", err
		}
		args = append(args, "--", p)
	}
	return r.git(ctx, args...)
}

func (r Repo) Diff(ctx context.Context, ref string, staged bool, path string) (string, error) {
	if err := checkRef(ref); err != nil {
		return "", err
	}
	args := []string{"diff"}
	if staged {
		args = append(args, "--staged")
	}
	if ref != "" {
		args = append(args, ref)
	}
	if path != "" {
		p, err := relPath(path)
		if err != nil {
			return "", err
		}
		args = append(args, "--", p)
	}
	return r.git(ctx, args...)
}

// ChangedFiles lists the files changed in ref (e.g. "main...feature") as "<status>\t<path>" lines.
func (r Repo) ChangedFiles(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", errors.New("ref is required, e.g. main...feature")
	}
	if err := checkRef(ref); err != nil {
		return "", err
	}
	return r.git(ctx, "diff", "--name-status", "--find-renames", ref)
}

// checkRef rejects refs that git would parse as options or that contain whitespace.
func checkRef(ref string) error {
	if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, " \t\n\x00") {
		return fmt.Errorf("invalid ref %q", ref)
	}
	return nil
}

func (r Repo) Branches(ctx context.Context) (string, error) {
	return r.git(ctx, "branch", "-a", "-vv")
}

func (r Repo) ListFiles(ctx context.Context, path string) (string, error) {
	args := []string{"ls-files"}
	if path != "" {
		p, err := relPath(path)
		if err != nil {
			return "", err
		}
		args = append(args, "--", p)
	}
	return r.git(ctx, args...)
}

func (r Repo) ReadFile(path string) (string, error) {
	p, err := relPath(path)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(r.Dir)
	if err != nil {
		return "", err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(root, full); err != nil || escapes(rel) {
		return "", fmt.Errorf("path %q is outside the project", path)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
		return "", fmt.Errorf("%s looks like a binary file", path)
	}
	return truncate(string(b)), nil
}

// relPath validates a project-relative path and returns it slash-separated.
func relPath(path string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(clean) || escapes(clean) {
		return "", fmt.Errorf("path %q is outside the project", path)
	}
	return filepath.ToSlash(clean), nil
}

func escapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (r Repo) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Dir, "--no-pager"}, args...)...)
	// No index refresh writes (the project is mounted read-only), never prompt.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		msg := ""
		if errors.As(err, &ee) {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("git %s failed: %v: %s", args[0], err, msg)
	}
	if strings.TrimSpace(string(out)) == "" {
		return "(no output)", nil
	}
	return truncate(string(out)), nil
}

func truncate(s string) string {
	if len(s) <= maxOutput {
		return s
	}
	return s[:maxOutput] + "\n... [truncated]"
}
