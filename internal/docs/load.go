package docs

import (
	"os"
	"path/filepath"
)

// chunkSize keeps chunks small (~400 tokens) so each embedding stays focused on one topic.
const chunkSize = 1500

// LoadChunks discovers doc files under the project and the extra roots and splits them into chunks.
// Project sources are project-relative paths the file tools accept, e.g. "docs/install.md";
// extra roots are prefixed with their base name, e.g. "docs/content/intro.md" for /docs.
func LoadChunks(project string, extra ...string) ([]Chunk, error) {
	var all []Chunk
	for i, root := range append([]string{project}, extra...) {
		prefix := filepath.Base(root) + "/"
		if i == 0 {
			prefix = ""
		}
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
			chunks := Split(prefix+f.Rel, body, chunkSize)
			for i := range chunks {
				chunks[i].Title = title
			}
			all = append(all, chunks...)
		}
	}
	return all, nil
}
