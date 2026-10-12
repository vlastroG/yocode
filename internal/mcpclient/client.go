// Package mcpclient connects to an MCP server and adapts its tools for Ollama.
package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vlastroG/yocode/internal/ollama"
)

type Client struct {
	session *mcp.ClientSession
}

// Start launches an MCP server binary and talks to it over stdio.
func Start(ctx context.Context, command string, args ...string) (*Client, error) {
	return Connect(ctx, &mcp.CommandTransport{Command: exec.Command(command, args...)})
}

func Connect(ctx context.Context, t mcp.Transport) (*Client, error) {
	c := mcp.NewClient(&mcp.Implementation{Name: "yocode", Version: "0.1.0"}, nil)
	s, err := c.Connect(ctx, t, nil)
	if err != nil {
		return nil, err
	}
	return &Client{session: s}, nil
}

func (c *Client) Close() error { return c.session.Close() }

// Tools returns the server's tools in Ollama's function-calling format.
func (c *Client) Tools(ctx context.Context) ([]ollama.Tool, error) {
	res, err := c.session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, err
	}
	tools := make([]ollama.Tool, 0, len(res.Tools))
	for _, t := range res.Tools {
		params, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, err
		}
		tools = append(tools, ollama.Tool{
			Type:     "function",
			Function: ollama.ToolFunction{Name: t.Name, Description: t.Description, Parameters: params},
		})
	}
	return tools, nil
}

// Call runs a tool and returns its text output; tool-reported failures become errors.
func (c *Client) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{} // the schema expects an object, not null
	}
	res, err := c.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	if res.IsError {
		return "", errors.New(b.String())
	}
	return b.String(), nil
}
