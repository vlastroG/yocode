package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c := FromEnv(env(nil))
	if c.OllamaURL != "http://localhost:11434" {
		t.Errorf("OllamaURL = %q", c.OllamaURL)
	}
	if c.ChatModel != "qwen2.5:7b" || c.EmbedModel != "bge-m3" {
		t.Errorf("models = %q, %q", c.ChatModel, c.EmbedModel)
	}
	if c.ProjectDir != "/project" || c.DocsDir != "/docs" || c.DataDir != "/data" {
		t.Errorf("dirs = %q %q %q", c.ProjectDir, c.DocsDir, c.DataDir)
	}
	if c.MCPCommand != "yocode-mcp" || c.NumCtx != 8192 || c.Reindex {
		t.Errorf("mcp=%q numctx=%d reindex=%v", c.MCPCommand, c.NumCtx, c.Reindex)
	}
	if !strings.HasPrefix(c.IndexKey, "docs-") {
		t.Errorf("IndexKey = %q", c.IndexKey)
	}
}

func TestOverrides(t *testing.T) {
	c := FromEnv(env(map[string]string{
		"OLLAMA_URL": "http://ollama:11434/",
		"CHAT_MODEL": "llama3.1:8b",
		"REINDEX":    "TRUE",
		"NUM_CTX":    "4096",
	}))
	if c.OllamaURL != "http://ollama:11434" {
		t.Errorf("OllamaURL = %q, want trailing slash trimmed", c.OllamaURL)
	}
	if c.ChatModel != "llama3.1:8b" || !c.Reindex || c.NumCtx != 4096 {
		t.Errorf("got %+v", c)
	}
}

func TestIndexKeyNormalizesHostPaths(t *testing.T) {
	a := FromEnv(env(map[string]string{"PROJECT_HOST_PATH": `C:\Repos\Foo\`}))
	b := FromEnv(env(map[string]string{"PROJECT_HOST_PATH": "c:/repos/foo"}))
	if a.IndexKey != b.IndexKey {
		t.Errorf("keys differ: %q vs %q", a.IndexKey, b.IndexKey)
	}
}

func TestIndexKeyDependsOnDocsAndModel(t *testing.T) {
	base := FromEnv(env(map[string]string{"PROJECT_HOST_PATH": "c:/foo"}))
	withDocs := FromEnv(env(map[string]string{"PROJECT_HOST_PATH": "c:/foo", "DOCS_HOST_PATH": "c:/foo-docs"}))
	otherModel := FromEnv(env(map[string]string{"PROJECT_HOST_PATH": "c:/foo", "EMBED_MODEL": "mxbai-embed-large"}))
	if base.IndexKey == withDocs.IndexKey || base.IndexKey == otherModel.IndexKey {
		t.Errorf("keys should differ: %q %q %q", base.IndexKey, withDocs.IndexKey, otherModel.IndexKey)
	}
}
