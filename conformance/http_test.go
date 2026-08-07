//go:build live

package conformance

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samdotson61/gpty/internal/mcpserv"
	"github.com/samdotson61/gpty/internal/session"
)

// This file covers the cloud-agent plane (build-plan §5, Phase 5): a REAL MCP
// client reaching a real `gpty serve` over streamable HTTP with bearer auth,
// then driving a terminal end to end. Until now the HTTP transport was only
// ever checked by hand with curl (initialize + 401/200); this pins the whole
// loop so it can't rot.

// bearer adds the Authorization header to every request.
type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// freePort asks the OS for an unused loopback port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// buildGpty compiles the gpty binary under test into a temp dir.
func buildGpty(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gpty")
	// runtime.GOOS, not os.Getenv("GOOS"): GOOS is a build-time constant and is
	// normally unset in the environment, so the env lookup silently produced a
	// suffix-less path on Windows and exec could not find the binary.
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", bin, "../cmd/gpty").CombinedOutput()
	if err != nil {
		t.Fatalf("build gpty: %v\n%s", err, out)
	}
	return bin
}

func TestServeHTTPEndToEnd(t *testing.T) {
	const token = "conformance-token"
	bin := buildGpty(t)
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// --no-ctl keeps the subprocess on the exec engine so this test measures
	// the TRANSPORT, not the control channel (covered by the ctl tests).
	srv := exec.Command(bin, "serve", "--addr", addr, "--token", token, "--no-ctl")
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Process.Kill()
		_ = srv.Wait()
	})

	base := "http://" + addr
	waitHealthy(t, base+"/healthz")

	// The loopback guard's other half — no token means no access.
	t.Run("unauthorized without token", func(t *testing.T) {
		resp, err := http.Post(base+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", resp.StatusCode)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "conformance-cloud-agent", Version: "1"}, nil).
		Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   base + "/mcp",
			HTTPClient: &http.Client{Transport: bearer{token: token, base: http.DefaultTransport}},
		}, nil)
	if err != nil {
		t.Fatalf("MCP connect over HTTP: %v", err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) != len(mcpserv.AllTools) {
		t.Errorf("got %d tools over HTTP, want %d", len(tools.Tools), len(mcpserv.AllTools))
	}

	call := func(t *testing.T, name string, args map[string]any) string {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("%s returned tool error: %s", name, toolText(res))
		}
		return toolText(res)
	}

	// Drive a terminal exactly as a remote agent would.
	name := uniqueName(t)
	marker := "cloud-plane-ok"
	call(t, "pty_spawn", map[string]any{"name": name})

	// The session must be real on this machine — the whole point is that a
	// remote agent's work is visible locally (a human can gmux attach to it).
	if !session.Has(name) {
		t.Fatalf("session %q not visible locally after remote pty_spawn", name)
	}

	call(t, "pty_send", map[string]any{"name": name, "text": "echo " + marker + "<Enter>"})
	if got := call(t, "pty_wait_for", map[string]any{"name": name, "pattern": marker, "timeout": 10}); !strings.Contains(got, marker) {
		t.Fatalf("pty_wait_for over HTTP missing %q; got:\n%s", marker, got)
	}
	if got := call(t, "pty_snapshot", map[string]any{"name": name}); !strings.Contains(got, marker) {
		t.Fatalf("pty_snapshot over HTTP missing %q; got:\n%s", marker, got)
	}
	if got := call(t, "pty_list", nil); !strings.Contains(got, name) {
		t.Errorf("pty_list over HTTP missing %q; got %q", name, got)
	}
	call(t, "pty_kill", map[string]any{"name": name})
	if session.Has(name) {
		t.Error("session still present after remote pty_kill")
	}
}

func toolText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func waitHealthy(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(url); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server never became healthy at %s", url)
}
