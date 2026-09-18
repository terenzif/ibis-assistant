package httpserver

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/terenzif/ibis-assistant/internal/config"
)

func originGate(bindHost string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !OriginAllowed(r.Header.Get("Origin"), bindHost) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withDeprecation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Deprecation", "true")
		w.Header().Set("Link", `</mcp>; rel="successor-version"`)
		next.ServeHTTP(w, r)
	})
}

func bindHost(cfg *config.Config) string {
	host, _, err := net.SplitHostPort(cfg.ListenAddr())
	if err != nil {
		return ""
	}
	return host
}

// Handler mounts Streamable HTTP /mcp (primary), legacy SSE, discovery, and log upload.
func Handler(cfg *config.Config, mcpServer *server.MCPServer, logUpload http.Handler) http.Handler {
	sseServer := server.NewSSEServer(mcpServer)
	streamable := server.NewStreamableHTTPServer(mcpServer,
		server.WithHeartbeatInterval(30*time.Second),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			return InjectAuth(ctx, r)
		}),
	)

	host := bindHost(cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		writeDiscovery(w, r, cfg, mcpServer)
	})
	mux.Handle("/sse", withDeprecation(originGate(host, sseServer.SSEHandler())))
	mux.Handle("/message", withDeprecation(originGate(host, sseServer.MessageHandler())))
	mux.Handle("/mcp", originGate(host, streamable))
	mux.Handle("/mcp/", originGate(host, streamable))
	if logUpload != nil {
		mux.Handle("/api/v1/logs/upload", logUpload)
	}

	return Auth(mux)
}

// NewHTTPServer builds a server that listens on cfg.ListenAddr().
func NewHTTPServer(cfg *config.Config, mcpServer *server.MCPServer, logUpload http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.ListenAddr(),
		Handler:           Handler(cfg, mcpServer, logUpload),
		ReadHeaderTimeout: 10 * time.Second,
	}
}
