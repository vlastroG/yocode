package tui

import "testing"

func TestDiffLineKind(t *testing.T) {
	cases := map[string]lineKind{
		"diff --git a/x b/x": fileLine,
		"+++ b/x":            fileLine,
		"--- a/x":            fileLine,
		"new file mode 1":    fileLine,
		"index 1..2":         fileLine,
		"@@ -1 +1 @@":        hunkLine,
		"+x":                 addLine,
		"-x":                 delLine,
		" x":                 ctxLine,
		"":                   ctxLine,
	}
	for line, want := range cases {
		if got := diffLineKind(line); got != want {
			t.Errorf("diffLineKind(%q) = %v, want %v", line, got, want)
		}
	}
}
