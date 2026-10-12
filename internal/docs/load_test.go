package docs

import (
	"path/filepath"
	"testing"
)

func TestLoadChunksPrefixesRootName(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "project")
	docsDir := filepath.Join(base, "docs")
	write(t, project, "README.md", "# Readme\nhello")
	write(t, docsDir, "content/intro.md", "# Intro\nwelcome")

	chunks, err := LoadChunks(project, docsDir, filepath.Join(base, "missing"))
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]bool{}
	for _, c := range chunks {
		sources[c.Source] = true
	}
	if !sources["project/README.md"] || !sources["docs/content/intro.md"] || len(sources) != 2 {
		t.Errorf("sources = %v", sources)
	}
}
