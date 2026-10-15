package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/vlastroG/yocode/internal/agent"
)

const noChanges = "No changes."

// WorkingDiff returns the uncommitted changes (staged, unstaged and untracked files),
// optionally limited to path.
func WorkingDiff(ctx context.Context, tools agent.Tools, path string) (string, error) {
	args := map[string]any{"ref": "HEAD"}
	if path != "" {
		args["path"] = path
	}
	tracked, err := tools.Call(ctx, "git_diff", args)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if t := strings.TrimSpace(tracked); t != "" && t != "(no output)" {
		b.WriteString(strings.TrimRight(tracked, "\n"))
		b.WriteString("\n")
	}

	status, err := tools.Call(ctx, "git_status", nil)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(status, "\n") {
		p, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "?? ")
		if !ok {
			continue
		}
		p = strings.Trim(p, `"`)
		if !under(p, path) {
			continue
		}
		content, err := tools.Call(ctx, "read_file", map[string]any{"path": p})
		if err != nil {
			if strings.Contains(err.Error(), "binary file") {
				fmt.Fprintf(&b, "Binary file %s added\n", p)
				continue
			}
			return "", err
		}
		b.WriteString(newFileDiff(p, content))
	}
	if b.Len() == 0 {
		return noChanges, nil
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// under reports whether p is dir or inside it; an empty dir matches everything.
func under(p, dir string) bool {
	dir = strings.Trim(strings.TrimPrefix(dir, "./"), "/")
	return dir == "" || p == dir || strings.HasPrefix(p, dir+"/")
}

// newFileDiff renders content as an all-added diff, like git does for a new file.
func newFileDiff(p, content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	var lines []string
	if content != "" {
		lines = strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n", p, p, p)
	if len(lines) > 0 {
		b.WriteString("@@ -0,0 +1," + strconv.Itoa(len(lines)) + " @@\n")
		for _, l := range lines {
			b.WriteString("+" + l + "\n")
		}
	}
	return b.String()
}
