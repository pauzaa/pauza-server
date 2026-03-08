package middleware

import (
	"net"
	"net/http"
	"strings"
)

// TrustedRealIP returns a middleware that extracts the real client IP from
// the X-Forwarded-For or X-Real-Ip headers, but only when the directly
// connecting peer (r.RemoteAddr) is within one of the trusted proxy
// networks. When the peer is not trusted the middleware leaves RemoteAddr
// unchanged, preventing untrusted clients from spoofing their IP.
//
// If trustedNets is nil or empty, no peer is considered trusted and forwarded
// headers are never honoured (safe default: trust nobody).
//
// When the peer is trusted the middleware picks the right-most non-trusted IP
// from X-Forwarded-For (i.e. the first address that was NOT appended by a
// known proxy). If X-Forwarded-For is absent or empty it falls back to
// X-Real-Ip. If neither header yields a usable IP, RemoteAddr is left as-is.
func TrustedRealIP(trustedNets []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(trustedNets) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			peerIP := extractIP(r.RemoteAddr)
			if peerIP == nil || !inNets(peerIP, trustedNets) {
				next.ServeHTTP(w, r)
				return
			}

			// Peer is a trusted proxy — resolve the real client IP.
			if rip := resolveXForwardedFor(r.Header.Get("X-Forwarded-For"), trustedNets); rip != "" {
				r.RemoteAddr = rip
				next.ServeHTTP(w, r)
				return
			}
			if rip := strings.TrimSpace(r.Header.Get("X-Real-Ip")); rip != "" {
				if parsed := net.ParseIP(rip); parsed != nil {
					r.RemoteAddr = rip
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// resolveXForwardedFor walks the X-Forwarded-For chain from right to left and
// returns the first (right-most) IP that is NOT in the trusted proxy set.
// This is the standard algorithm for determining the true client IP when
// there may be multiple proxies in the chain.
func resolveXForwardedFor(xff string, trustedNets []*net.IPNet) string {
	if xff == "" {
		return ""
	}
	parts := strings.Split(xff, ",")
	// Walk right-to-left: skip trusted proxies, return the first untrusted IP.
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate == "" {
			continue
		}
		ip := net.ParseIP(candidate)
		if ip == nil {
			continue
		}
		if !inNets(ip, trustedNets) {
			return candidate
		}
	}
	return ""
}

// extractIP parses the IP portion out of a host:port RemoteAddr string. If
// RemoteAddr is already a bare IP it is returned directly.
func extractIP(addr string) net.IP {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Might be a bare IP already.
		return net.ParseIP(strings.TrimSpace(addr))
	}
	return net.ParseIP(host)
}

// inNets reports whether ip is contained in any of the given networks.
func inNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
