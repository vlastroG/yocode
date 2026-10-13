package docs

import (
	"strings"
	"testing"
)

func TestSplitByHeadings(t *testing.T) {
	got := Split("a.md", "# Title\nintro\n\n## Install\nrun it\n", 1000)
	want := []Chunk{
		{Source: "a.md", Heading: "Title", Text: "# Title\nintro"},
		{Source: "a.md", Heading: "Install", Text: "## Install\nrun it"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d chunks: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("chunk %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSplitRespectsMaxChars(t *testing.T) {
	content := strings.Repeat(strings.Repeat("x", 30)+"\n", 10)
	got := Split("a.md", content, 100)
	if len(got) < 2 {
		t.Fatalf("expected several chunks, got %d", len(got))
	}
	for _, c := range got {
		if len(c.Text) > 100 {
			t.Errorf("chunk too long: %d", len(c.Text))
		}
	}
}

func TestSplitIgnoresHashInCodeFence(t *testing.T) {
	got := Split("a.md", "## Setup\n```bash\n# install deps\nmake\n```\n", 1000)
	if len(got) != 1 || got[0].Heading != "Setup" {
		t.Errorf("got %+v", got)
	}
}

func TestSplitCRLFAndEmpty(t *testing.T) {
	if got := Split("a.md", "", 100); len(got) != 0 {
		t.Errorf("empty content gave %+v", got)
	}
	got := Split("a.md", "# T\r\nbody\r\n", 100)
	if len(got) != 1 || got[0].Text != "# T\nbody" {
		t.Errorf("got %+v", got)
	}
}

func TestSplitLongLine(t *testing.T) {
	got := Split("a.md", strings.Repeat("y", 250), 100)
	if len(got) != 3 {
		t.Fatalf("got %d chunks", len(got))
	}
	for _, c := range got {
		if len(c.Text) > 100 {
			t.Errorf("chunk too long: %d", len(c.Text))
		}
	}
}
