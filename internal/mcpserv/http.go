package mcpserv

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samdotson61/gpty/internal/engine"
)

// HTTPConfig configures the streamable-HTTP transport for cloud/remote agents.
type HTTPConfig struct {
	Addr  string            // e.g. 127.0.0.1:7683
	Token string            // bearer token; required for any non-loopback bind
	Allow func(string) bool // tool filter (nil = all tools)
}

// ServeHTTP runs the MCP server over streamable HTTP. It refuses to bind a
// non-loopback address without a token (build-plan §5/§9: the only real
// security surface — loopback + token by default, never an open default bind).
func ServeHTTP(eng engine.Engine, cfg HTTPConfig) error {
	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return fmt.Errorf("invalid --addr %q: %w", cfg.Addr, err)
	}
	if !isLoopback(host) && cfg.Token == "" {
		return fmt.Errorf("refusing to bind non-loopback address %q without a token; "+
			"set GPTY_TOKEN (or --token), or bind 127.0.0.1 and tunnel in (ssh -R / tailscale / cloudflared)", cfg.Addr)
	}
	tokenState := "required"
	if cfg.Token == "" {
		tokenState = "disabled — loopback only"
	}
	fmt.Fprintf(os.Stderr, "gpty: serving MCP over http on %s (token %s)\n", cfg.Addr, tokenState)

	srv := NewServer(eng, cfg.Allow)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/mcp", withAuth(cfg.Token, handler))
	mux.Handle("/", withAuth(cfg.Token, handler))

	//nolint:gosec // ListenAndServe without timeouts is acceptable for a localhost dev bridge
	return http.ListenAndServe(cfg.Addr, mux)
}

// withAuth enforces a constant-time bearer-token check when a token is set.
func withAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			want := "Bearer " + token
			got := r.Header.Get("Authorization")
			if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(host string) bool {
	if host == "localhost" || host == "" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
