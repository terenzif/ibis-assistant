package httpserver

import (
	"net/url"
	"strings"
)

// OriginAllowed implements MCP Streamable HTTP DNS-rebinding protection.
// Missing Origin (CLI / many MCP clients) is allowed. Browser http(s) origins
// must be loopback or the configured bind host.
func OriginAllowed(origin, bindHost string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" || strings.EqualFold(origin, "null") {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return true
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	bindHost = strings.ToLower(strings.TrimSpace(bindHost))
	if bindHost != "" && bindHost != "0.0.0.0" && bindHost != "::" && host == bindHost {
		return true
	}
	return false
}
