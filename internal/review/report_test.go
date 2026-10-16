package review

import (
	"strings"
	"testing"
)

func TestSortFindings(t *testing.T) {
	in := []Finding{
		{File: "b.go", Line: 5, Severity: "low", Category: "recommendation", Message: "m1"},
		{File: "a.go", Line: 9, Severity: "high", Category: "bug", Message: "m2"},
		{File: "a.go", Line: 2, Severity: "high", Category: "security", Message: "m3"},
		{File: "a.go", Line: 9, Severity: "high", Category: "bug", Message: "m2"},
	}
	got := sortFindings(in)
	var msgs []string
	for _, f := range got {
		msgs = append(msgs, f.Message)
	}
	if strings.Join(msgs, ",") != "m3,m2,m1" {
		t.Errorf("order = %v", msgs)
	}
}

func TestMarkdown(t *testing.T) {
	r := Report{
		Range:    "main...feature",
		Summary:  "Adds X.",
		Reviewed: []string{"a.go", "b.go"},
		Skipped:  []Skipped{{Path: "go.sum", Reason: "lockfile"}},
		Findings: []Finding{
			{File: "a.go", Line: 2, Severity: "high", Category: "security", Message: "path traversal", Suggestion: "use relPath"},
			{Severity: "low", Category: "architecture", Message: "two packages do the same"},
		},
	}
	md := r.Markdown()
	for _, want := range []string{
		"# Review of main...feature",
		"Adds X.",
		"Files reviewed: 2 · findings: 2 (high 1, medium 0, low 1)",
		"## High",
		"- [security] a.go:2 — path traversal",
		"  Suggestion: use relPath",
		"## Low",
		"- [architecture] (whole PR) — two packages do the same",
		"## Not reviewed",
		"- go.sum (lockfile)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "## Medium") {
		t.Error("empty severity section rendered")
	}
	if md := (Report{Range: "a...b"}).Markdown(); !strings.Contains(md, "No problems found.") {
		t.Errorf("empty report:\n%s", md)
	}
}
