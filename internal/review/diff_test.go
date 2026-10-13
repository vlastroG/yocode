package review

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseChanges(t *testing.T) {
	got := parseChanges("M\tREADME.md\nA\tcmd/new.go\nD\told.txt\nR087\tsrc/a.go\tsrc/b.go\n")
	want := []fileChange{{"M", "README.md"}, {"A", "cmd/new.go"}, {"D", "old.txt"}, {"R", "src/b.go"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := parseChanges("(no output)"); len(got) != 0 {
		t.Errorf("no-output parsed as %v", got)
	}
}

func TestSkipReason(t *testing.T) {
	cases := map[fileChange]string{
		{"M", "go.sum"}:                "lockfile",
		{"M", "web/package-lock.json"}: "lockfile",
		{"D", "a.go"}:                  "deleted",
		{"A", "img/logo.PNG"}:          "binary",
		{"M", "vendor/x/y.go"}:         "vendored",
		{"M", "web/node_modules/a.js"}: "vendored",
		{"M", "internal/a.go"}:         "",
	}
	for c, want := range cases {
		if got := skipReason(c); got != want {
			t.Errorf("skipReason(%v) = %q, want %q", c, got, want)
		}
	}
}

func TestNumberDiff(t *testing.T) {
	diff := "diff --git a/f.go b/f.go\nindex 1..2 100644\n--- a/f.go\n+++ b/f.go\n" +
		"@@ -10,4 +10,4 @@ func x() {\n a := 1\n-b := 2\n--- old comment\n+b := 3\n+c := 4\n d := 5\n"
	want := "@@ -10,4 +10,4 @@ func x() {\n" +
		"   10  a := 1\n" +
		"      -b := 2\n" +
		"      --- old comment\n" +
		"   11 +b := 3\n" +
		"   12 +c := 4\n" +
		"   13  d := 5\n"
	if got := numberDiff(diff); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := numberDiff("(no output)"); got != "" {
		t.Errorf("no-output numbered as %q", got)
	}
}

func TestSplitDiff(t *testing.T) {
	hunk := func(start int) string {
		return fmt.Sprintf("@@ -%d,1 +%d,1 @@\n%5d +%s\n", start, start, start, strings.Repeat("x", 40))
	}
	numbered := hunk(1) + hunk(20) + hunk(40) // 64 + 66 + 66 bytes
	parts := splitDiff(numbered, 140)
	if len(parts) != 2 || strings.Join(parts, "") != numbered {
		t.Fatalf("parts = %q", parts)
	}
	for _, p := range parts {
		if len(p) > 140 {
			t.Errorf("part too long: %d", len(p))
		}
	}

	long := splitDiff(hunk(1)+strings.Repeat("    2 +y\n", 100), 100)
	if len(long) != 1 || !strings.HasSuffix(long[0], "... [hunk truncated]\n") ||
		len(long[0]) > 100+len("\n... [hunk truncated]\n") {
		t.Errorf("long = %q", long)
	}
}
