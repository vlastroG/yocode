// Package config reads yocode settings from the environment.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

type Config struct {
	OllamaURL  string
	ChatModel  string
	EmbedModel string
	NumCtx     int

	ProjectDir string // project mount inside the container
	DocsDir    string // optional separate docs mount inside the container
	DataDir    string // persistent volume for the vector DB
	MCPCommand string // path of the yocode-mcp binary

	// IndexKey identifies the docs index for this (project, docs, embedding model) combination,
	// so a project seen before reuses its embeddings.
	IndexKey string
	Reindex  bool
}

func FromEnv(getenv func(string) string) Config {
	get := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	c := Config{
		OllamaURL:  strings.TrimRight(get("OLLAMA_URL", "http://localhost:11434"), "/"),
		ChatModel:  get("CHAT_MODEL", "qwen2.5:7b"),
		EmbedModel: get("EMBED_MODEL", "nomic-embed-text"),
		NumCtx:     8192,
		ProjectDir: get("PROJECT_DIR", "/project"),
		DocsDir:    get("DOCS_DIR", "/docs"),
		DataDir:    get("DATA_DIR", "/data"),
		MCPCommand: get("YOCODE_MCP", "yocode-mcp"),
		Reindex:    parseBool(getenv("REINDEX")),
	}
	if n, err := strconv.Atoi(getenv("NUM_CTX")); err == nil && n > 0 {
		c.NumCtx = n
	}
	c.IndexKey = indexKey(get("PROJECT_HOST_PATH", c.ProjectDir), getenv("DOCS_HOST_PATH"), c.EmbedModel)
	return c
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func indexKey(project, docs, model string) string {
	h := sha256.Sum256([]byte(normPath(project) + "|" + normPath(docs) + "|" + model))
	return "docs-" + hex.EncodeToString(h[:8])
}

// normPath makes Windows host paths comparable: case-insensitive, forward slashes, no trailing slash.
func normPath(p string) string {
	p = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), `\`, "/"))
	return strings.TrimRight(p, "/")
}
