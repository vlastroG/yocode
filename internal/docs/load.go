package docs

import (
	"os"
	"path/filepath"
)

// chunkSize keeps chunks small (~400 tokens) so each embedding stays focused on one topic.
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
			title, body := splitFrontMatter(string(b))
			chunks := Split(filepath.Base(root)+"/"+f.Rel, body, chunkSize)
			for i := range chunks {
				chunks[i].Title = title
			}
			all = append(all, chunks...)
		}
	}
	return all, nil
}
