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

// Commit stages and commits only paths, leaving other staged changes out of the commit; without
// paths it stages and commits everything. Hooks don't run and commits are not signed.
func (r Repo) Commit(ctx context.Context, message string, paths []string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("message is required")
	}
	var spec []string
	if len(paths) > 0 {
		spec = []string{"--"}
		for _, p := range paths {
			rel, err := relPath(p)
			if err != nil {
				return "", err
			}
			spec = append(spec, rel)
		}
	}
	if _, err := r.git(ctx, append([]string{"add", "-A"}, spec...)...); err != nil {
		return "", err
	}
	// exit 0 = nothing staged, 1 = staged changes
	err := r.gitCmd(ctx, append([]string{"diff", "--cached", "--quiet"}, spec...)...).Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return "", errors.New("nothing to commit")
	case !errors.As(err, &ee) || ee.ExitCode() != 1:
		return "", err
	}
	if _, err := r.git(ctx, append([]string{"commit", "--no-verify", "-m", message}, spec...)...); err != nil {
		return "", err
	}
	return r.git(ctx, "log", "-1", "--stat", "--pretty=format:%h %s")
}
