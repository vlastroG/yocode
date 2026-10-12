// Package docs finds documentation files and splits them into chunks for indexing.
package docs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maxFileSize = 1 << 20

var docExts = map[string]bool{
	".md": true, ".markdown": true, ".mdx": true, ".txt": true, ".rst": true, ".adoc": true,
}

// skipDirs are never documentation (dependencies, build output, Hugo's generated site).
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true,
	"bin": true, "obj": true, "public": true, "resources": true, "venv": true,
}

type File struct {
	Rel string // slash-separated, relative to the root
	Abs string
}

// Discover returns documentation files under root. A missing root yields no files.
func Discover(root string) ([]File, error) {
	if root == "" {
		return nil, nil
	}
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var files []File
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isDoc(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxFileSize {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, File{Rel: filepath.ToSlash(rel), Abs: path})
		return nil
	})
	return files, err
}

func isDoc(name string) bool {
	return docExts[strings.ToLower(filepath.Ext(name))] || strings.HasPrefix(strings.ToUpper(name), "README")
}
