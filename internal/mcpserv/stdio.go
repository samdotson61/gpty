package mcpserv

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samdotson61/gpty/internal/engine"
)

// ServeStdio runs the MCP server over stdio — the transport local agents
// (Claude Code/Desktop) use. Blocks until the client disconnects. allow
// filters the registered tools (nil = all).
func ServeStdio(eng engine.Engine, allow func(string) bool) error {
	return NewServer(eng, allow).Run(context.Background(), &mcp.StdioTransport{})
}
