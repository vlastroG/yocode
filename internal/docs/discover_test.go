package docs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverFindsDocsOnly(t *testing.T) {
	root := t.TempDir()
	write(t, root, "README.md", "# Project")
	write(t, root, "docs/guide.md", "# Guide")
	write(t, root, "notes.txt", "notes")
	write(t, root, "main.go", "package main")
	write(t, root, "node_modules/pkg/README.md", "# dep")
	write(t, root, ".github/PULL_REQUEST_TEMPLATE.md", "template")

	files, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.Rel)
	}
	want := []string{"README.md", "docs/guide.md", "notes.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiscoverMissingRoot(t *testing.T) {
	files, err := Discover(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(files) != 0 {
		t.Errorf("got %v, %v; want no files and no error", files, err)
	}
}
