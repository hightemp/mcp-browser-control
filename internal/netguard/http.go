// Package netguard contains local HTTP boundary checks.
package netguard

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// LocalOnly rejects non-loopback Host and Origin headers. Native MCP clients
// commonly omit Origin, which is accepted.
func LocalOnly(next http.Handler) http.Handler {
	return LocalOnlyWithOrigins(next, nil)
}

// LocalOnlyWithOrigins additionally restricts browser-originated requests to
// an exact allowlist. Requests without Origin remain valid for native clients.
func LocalOnlyWithOrigins(next http.Handler, allowedOrigins []string) http.Handler {
	allowlist := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowlist[origin] = struct{}{}
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !IsLoopbackHost(request.Host) {
			http.Error(writer, "forbidden host", http.StatusForbidden)
			return
		}
		if origin := request.Header.Get("Origin"); origin != "" {
			if !IsLoopbackOrigin(origin) {
				http.Error(writer, "forbidden origin", http.StatusForbidden)
				return
			}
			if len(allowlist) > 0 {
				if _, ok := allowlist[origin]; !ok {
					http.Error(writer, "forbidden origin", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(writer, request)
	})
}

// IsLoopbackOrigin reports whether origin is HTTP(S) on a loopback host.
func IsLoopbackOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	return IsLoopbackHost(parsed.Host)
}

// IsLoopbackHost reports whether hostPort names a loopback interface.
func IsLoopbackHost(hostPort string) bool {
	host := hostPort
	if parsedHost, _, err := net.SplitHostPort(hostPort); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
