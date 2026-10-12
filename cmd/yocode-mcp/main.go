// Command yocode-mcp is a stdio MCP server with read-only git/file tools for one project.
package main

import (
	"context"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vlastroG/yocode/internal/gittools"
	"github.com/vlastroG/yocode/internal/mcpserver"
)

func main() {
	dir := os.Getenv("PROJECT_DIR")
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if dir == "" {
		dir = "/project"
	}
	s := mcpserver.New(gittools.Repo{Dir: dir})
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
