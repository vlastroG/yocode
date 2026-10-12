package docs

import (
	"strings"
	"testing"
)

func TestSplitFrontMatter(t *testing.T) {
	cases := []struct {
		name, content, title, body string
	}{
		{"yaml", "---\ndate: :git\ntitle: Обновление платформы\ndescription: Способы обновления\ntags: [docs]\n---\n\nBody",
			"Обновление платформы — Способы обновления", "Body"},
		{"yaml quoted", "---\ntitle: \"Quoted: yes\"\n---\nBody", "Quoted: yes", "Body"},
		{"toml", "+++\ntitle = \"Install\"\n+++\nBody", "Install", "Body"},
		{"crlf", "---\r\ntitle: T\r\n---\r\nBody", "T", "Body"},
		{"nested keys ignored", "---\nparams:\n  title: inner\ntitle: outer\n---\nBody", "outer", "Body"},
		{"no front matter uses first h1", "intro\n```sh\n# not a title\n```\n# Readme Title\n## Build\n",
			"Readme Title", "intro\n```sh\n# not a title\n```\n# Readme Title\n## Build"},
		{"unterminated is not front matter", "---\ntitle: x\nno end", "", "---\ntitle: x\nno end"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			title, body := splitFrontMatter(c.content)
			if title != c.title {
				t.Errorf("title = %q, want %q", title, c.title)
			}
			if strings.TrimSpace(body) != c.body {
				t.Errorf("body = %q, want %q", body, c.body)
			}
		})
	}
}
