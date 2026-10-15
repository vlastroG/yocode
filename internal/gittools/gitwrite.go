package gittools

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// CreateBranch creates branch name (from `from`, default HEAD) and switches to it.
func (r Repo) CreateBranch(ctx context.Context, name, from string) (string, error) {
	if name == "" {
		return "", errors.New("name is required")
	}
	if err := checkRef(name); err != nil {
		return "", err
	}
	if err := checkRef(from); err != nil {
		return "", err
	}
	if _, err := r.git(ctx, "check-ref-format", "--branch", name); err != nil {
		return "", err
	}
	args := []string{"switch", "-c", name}
	if from != "" {
		args = append(args, from)
	}
	if _, err := r.git(ctx, args...); err != nil {
		return "", err
	}
	return "switched to new branch " + name, nil
}

// Commit stages paths (everything when empty) and commits them locally.
func (r Repo) Commit(ctx context.Context, message string, paths []string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("message is required")
	}
	add := []string{"add", "-A"}
	if len(paths) > 0 {
		add = append(add, "--")
		for _, p := range paths {
			rel, err := relPath(p)
			if err != nil {
				return "", err
			}
			add = append(add, rel)
		}
	}
	if _, err := r.git(ctx, add...); err != nil {
		return "", err
	}
	// exit 0 = nothing staged, 1 = staged changes
	err := r.gitCmd(ctx, "diff", "--cached", "--quiet").Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return "", errors.New("nothing to commit")
	case !errors.As(err, &ee) || ee.ExitCode() != 1:
		return "", err
	}
	if _, err := r.git(ctx, "commit", "-m", message); err != nil {
		return "", err
	}
	return r.git(ctx, "log", "-1", "--stat", "--pretty=format:%h %s")
}
