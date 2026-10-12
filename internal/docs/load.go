package docs

import (
	"os"
	"path/filepath"
)

// chunkSize keeps chunks well under nomic-embed-text's context (~400 tokens each).
const chunkSize = 1500

// LoadChunks discovers doc files under each root and splits them into chunks.
// Sources are prefixed with the root's base name, e.g. "project/README.md" or "docs/content/intro.md".
func LoadChunks(roots ...string) ([]Chunk, error) {
	var all []Chunk
	for _, root := range roots {
		files, err := Discover(root)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			b, err := os.ReadFile(f.Abs)
			if err != nil {
				return nil, err
			}
			all = append(all, Split(filepath.Base(root)+"/"+f.Rel, string(b), chunkSize)...)
		}
	}
	return all, nil
}
