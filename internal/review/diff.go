package review

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

type fileChange struct {
	Status string // first letter of git's status: A, M, D, R, C, T
	Path   string // slash-separated; the new path for renames
}

// parseChanges reads `git diff --name-status` output. Renames ("R087\told\tnew") keep the new path.
func parseChanges(out string) []fileChange {
	var changes []fileChange
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || fields[0] == "" {
			continue // blank or "(no output)"
		}
		changes = append(changes, fileChange{Status: fields[0][:1], Path: fields[len(fields)-1]})
	}
	return changes
}

var lockfiles = map[string]bool{
	"go.sum": true, "package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"Cargo.lock": true, "poetry.lock": true, "composer.lock": true, "packages.lock.json": true,
}

var binaryExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true, ".svg": true, ".pdf": true,
	".zip": true, ".exe": true, ".dll": true, ".so": true, ".dylib": true,
	".woff": true, ".woff2": true, ".ttf": true,
}

// skipReason says why a changed file is not reviewed, or "" if it should be reviewed.
func skipReason(c fileChange) string {
	base := path.Base(c.Path)
	switch {
	case c.Status == "D":
		return "deleted"
	case lockfiles[base]:
		return "lockfile"
	case binaryExts[strings.ToLower(path.Ext(base))]:
		return "binary"
	case strings.HasPrefix(c.Path, "vendor/") || strings.Contains(c.Path, "/vendor/") || strings.Contains(c.Path, "node_modules/"):
		return "vendored"
	}
	return ""
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)`)

// numberDiff drops the file headers of a single-file unified diff and prefixes every line with
// its line number in the new file, so the model can cite exact lines. Removed lines get no number.
func numberDiff(diff string) string {
	var b strings.Builder
	n, inHunk := 0, false
	for _, line := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		if m := hunkHeader.FindStringSubmatch(line); m != nil {
			n, _ = strconv.Atoi(m[1])
			inHunk = true
			b.WriteString(line + "\n")
			continue
		}
		if !inHunk {
			continue // diff --git, index, ---, +++ headers
		}
		switch {
		case strings.HasPrefix(line, "-"):
			fmt.Fprintf(&b, "%5s %s\n", "", line)
		case line == "" || strings.HasPrefix(line, "+") || strings.HasPrefix(line, " "):
			fmt.Fprintf(&b, "%5d %s\n", n, line)
			n++
		default: // "\ No newline at end of file"
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// splitDiff cuts a numbered diff into parts of at most max bytes at hunk boundaries.
// A single hunk longer than max is truncated at a line boundary.
func splitDiff(numbered string, max int) []string {
	var hunks []string
	var cur strings.Builder
	for _, line := range strings.SplitAfter(numbered, "\n") {
		if strings.HasPrefix(line, "@@") && cur.Len() > 0 {
			hunks = append(hunks, cur.String())
			cur.Reset()
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		hunks = append(hunks, cur.String())
	}

	var parts []string
	var part strings.Builder
	for _, h := range hunks {
		if len(h) > max {
			cut := strings.LastIndex(h[:max], "\n")
			if cut <= 0 {
				cut = max
			}
			h = h[:cut] + "\n... [hunk truncated]\n"
		}
		if part.Len() > 0 && part.Len()+len(h) > max {
			parts = append(parts, part.String())
			part.Reset()
		}
		part.WriteString(h)
	}
	if part.Len() > 0 {
		parts = append(parts, part.String())
	}
	return parts
}
