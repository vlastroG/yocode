package gittools

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteFile creates a file, with its parent directories, or replaces its whole content.
// Replacing a CRLF file keeps CRLF.
func (r Repo) WriteFile(path, content string) (string, error) {
	full, p, err := r.writablePath(path)
	if err != nil {
		return "", err
	}
	old, statErr := os.ReadFile(full)
	if statErr == nil && bytes.Contains(old, []byte("\r\n")) {
		content = toCRLF(content)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "", err
	}
	if statErr == nil {
		return "overwrote " + p, nil
	}
	return "created " + p, nil
}

// EditFile replaces the single occurrence of old with new. LF text matches CRLF files and keeps their CRLF.
func (r Repo) EditFile(path, old, new string) (string, error) {
	full, p, err := r.writablePath(path)
	if err != nil {
		return "", err
	}
	if old == "" {
		return "", fmt.Errorf("old_text must not be empty")
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	if isBinary(b) {
		return "", fmt.Errorf("%s looks like a binary file", path)
	}
	text := string(b)
	if strings.Contains(text, "\r\n") {
		old = toCRLF(old)
		new = toCRLF(new)
	}
	switch n := strings.Count(text, old); n {
	case 0:
		return "", fmt.Errorf("old_text not found in %s: read the file again and copy the text exactly", p)
	case 1:
		text = strings.Replace(text, old, new, 1)
	default:
		return "", fmt.Errorf("old_text occurs %d times in %s: include more surrounding lines to make it unique", n, p)
	}
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		return "", err
	}
	return "edited " + p, nil
}

// DeleteFile removes a single file; directories are refused.
func (r Repo) DeleteFile(path string) (string, error) {
	full, p, err := r.writablePath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(full)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory: only files can be deleted", p)
	}
	if err := os.Remove(full); err != nil {
		return "", err
	}
	return "deleted " + p, nil
}

// writablePath returns the absolute and the project-relative path of a file that may be modified:
// inside the project (symlinks resolved) and not in any .git directory.
func (r Repo) writablePath(path string) (full, rel string, err error) {
	rel, err = relPath(path)
	if err != nil {
		return "", "", err
	}
	if rel == "." {
		return "", "", fmt.Errorf("path %q is not a file", path)
	}
	for _, seg := range strings.Split(rel, "/") {
		if strings.EqualFold(seg, ".git") {
			return "", "", fmt.Errorf("path %q is inside .git, which is off limits", path)
		}
	}
	root, err := filepath.EvalSymlinks(r.Dir)
	if err != nil {
		return "", "", err
	}
	full = filepath.Join(r.Dir, filepath.FromSlash(rel))
	// symlinks can only hide in the part that exists, so resolve the deepest existing ancestor
	probe := full
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	resolved, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return "", "", err
	}
	if inside, err := filepath.Rel(root, resolved); err != nil || escapes(inside) {
		return "", "", fmt.Errorf("path %q is outside the project", path)
	}
	return full, rel, nil
}

func isBinary(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0
}

func toCRLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}
