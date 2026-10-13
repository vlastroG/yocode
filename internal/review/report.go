package review

import (
	"fmt"
	"sort"
	"strings"
)

type Finding struct {
	File       string `json:"file,omitempty"` // empty = the whole pull request
	Line       int    `json:"line,omitempty"`
	Severity   string `json:"severity"` // high | medium | low
	Category   string `json:"category"` // bug | security | architecture | docs | recommendation
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

type Skipped struct {
	Path   string
	Reason string
}

type Report struct {
	Range    string // e.g. main...feature
	Summary  string
	Reviewed []string
	Skipped  []Skipped
	Findings []Finding
}

var severities = []string{"high", "medium", "low"}

func severityRank(s string) int {
	for i, v := range severities {
		if v == s {
			return i
		}
	}
	return len(severities)
}

// sortFindings orders findings by severity, file and line, and drops exact duplicates.
func sortFindings(fs []Finding) []Finding {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if ra, rb := severityRank(a.Severity), severityRank(b.Severity); ra != rb {
			return ra < rb
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	seen := map[Finding]bool{}
	var out []Finding
	for _, f := range fs {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Review of %s\n\n", r.Range)
	if r.Summary != "" {
		b.WriteString(r.Summary + "\n\n")
	}
	counts := map[string]int{}
	for _, f := range r.Findings {
		counts[f.Severity]++
	}
	fmt.Fprintf(&b, "Files reviewed: %d · findings: %d (high %d, medium %d, low %d)\n",
		len(r.Reviewed), len(r.Findings), counts["high"], counts["medium"], counts["low"])
	for _, sev := range severities {
		if counts[sev] == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n", strings.ToUpper(sev[:1])+sev[1:])
		for _, f := range r.Findings {
			if f.Severity != sev {
				continue
			}
			fmt.Fprintf(&b, "- [%s] %s — %s\n", f.Category, location(f), f.Message)
			if f.Suggestion != "" {
				fmt.Fprintf(&b, "  Suggestion: %s\n", f.Suggestion)
			}
		}
	}
	if len(r.Findings) == 0 {
		b.WriteString("\nNo problems found.\n")
	}
	if len(r.Skipped) > 0 {
		b.WriteString("\n## Not reviewed\n\n")
		for _, s := range r.Skipped {
			fmt.Fprintf(&b, "- %s (%s)\n", s.Path, s.Reason)
		}
	}
	return b.String()
}

func location(f Finding) string {
	switch {
	case f.File == "":
		return "(whole PR)"
	case f.Line > 0:
		return fmt.Sprintf("%s:%d", f.File, f.Line)
	default:
		return f.File
	}
}
