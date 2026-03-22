// internal/middleware/middleware.go
package middleware

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// IPWhitelist restricts access by IP. Empty list = allow all.
func IPWhitelist(allowed []string) func(http.Handler) http.Handler {
	if len(allowed) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	var nets []*net.IPNet
	var ips []net.IP
	for _, a := range allowed {
		if _, cidr, err := net.ParseCIDR(a); err == nil {
			nets = append(nets, cidr)
		} else if ip := net.ParseIP(a); ip != nil {
			ips = append(ips, ip)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			ip := net.ParseIP(host)
			if ip == nil {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			for _, cidr := range nets {
				if cidr.Contains(ip) {
					next.ServeHTTP(w, r)
					return
				}
			}
			for _, a := range ips {
				if a.Equal(ip) {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "forbidden", http.StatusForbidden)
		})
	}
}

// TokenAuth checks Bearer token or ?token= query param. Empty token = allow all.
// Bearer prefix is case-insensitive, token value is case-sensitive.
// Query param fallback enables WebSocket auth (WS API cannot send custom headers).
func TokenAuth(token string) func(http.Handler) http.Handler {
	if token == "" {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check Authorization header first
			if auth := r.Header.Get("Authorization"); len(auth) >= 7 && strings.EqualFold(auth[:7], "bearer ") && subtle.ConstantTimeCompare([]byte(auth[7:]), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			// Fallback: ?token= query param (for WebSocket)
			if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	}
}

// CORS adds permissive CORS headers. Safe because auth is handled by IP + token.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}
