package docs

import (
	"path/filepath"
	"strings"
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

func TestLoadChunksAttachesPageTitle(t *testing.T) {
	base := t.TempDir()
	docsDir := filepath.Join(base, "docs")
	write(t, docsDir, "update.md", "---\ndate: :git\ntitle: Обновление\n---\n## Версии\nv00.00.00\n## Скрипты\nавтоматически")

	chunks, err := LoadChunks(docsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks: %+v", len(chunks), chunks)
	}
	for _, c := range chunks {
		if c.Title != "Обновление" {
			t.Errorf("chunk %q has title %q", c.Heading, c.Title)
		}
		if strings.Contains(c.Text, "date:") {
			t.Errorf("front matter leaked into chunk: %q", c.Text)
		}
	}
}
