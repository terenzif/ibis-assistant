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
		w.Header().Set("Sunset", "Sat, 18 Sep 2027 00:00:00 GMT")
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
	return HandlerWithSettings(cfg, mcpServer, logUpload, nil, nil)
}

// HandlerWithSettings is Handler plus optional /settings and /api/v1/settings.
func HandlerWithSettings(cfg *config.Config, mcpServer *server.MCPServer, logUpload, settingsAPI, settingsUI http.Handler) http.Handler {
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
	if settingsAPI != nil {
		mux.Handle("/api/v1/settings", settingsAPI)
	}
	if settingsUI != nil {
		mux.Handle("/settings/", http.StripPrefix("/settings/", settingsUI))
		mux.Handle("/settings", http.RedirectHandler("/settings/", http.StatusFound))
	}

	return Auth(mux)
}

// NewHTTPServer builds a server that listens on cfg.ListenAddr().
func NewHTTPServer(cfg *config.Config, mcpServer *server.MCPServer, logUpload http.Handler) *http.Server {
	return NewHTTPServerWithSettings(cfg, mcpServer, logUpload, nil, nil)
}

// NewHTTPServerWithSettings mounts optional settings UI and API.
func NewHTTPServerWithSettings(cfg *config.Config, mcpServer *server.MCPServer, logUpload, settingsAPI, settingsUI http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.ListenAddr(),
		Handler:           HandlerWithSettings(cfg, mcpServer, logUpload, settingsAPI, settingsUI),
		ReadHeaderTimeout: 10 * time.Second,
	}
}
