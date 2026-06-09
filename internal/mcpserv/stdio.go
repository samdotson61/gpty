package mcpserv

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samdotson61/gpty/internal/engine"
)

// ServeStdio runs the MCP server over stdio — the transport local agents
// (Claude Code/Desktop) use. Blocks until the client disconnects.
func ServeStdio(eng engine.Engine) error {
	return NewServer(eng).Run(context.Background(), &mcp.StdioTransport{})
}
