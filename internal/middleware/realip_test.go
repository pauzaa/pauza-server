package middleware_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/middleware"
)

// mustParseCIDR is a test helper that parses a CIDR string or fails the test.
func mustParseCIDR(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("bad CIDR %q: %v", cidr, err)
	}
	return ipNet
}

// captureRemoteAddr is a handler that records the final r.RemoteAddr.
func captureRemoteAddr(t *testing.T, got *string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		*got = r.RemoteAddr
	})
}

func TestTrustedRealIP_NilNets_NeverTrusts(t *testing.T) {
	var captured string
	h := middleware.TrustedRealIP(nil)(captureRemoteAddr(t, &captured))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")

	h.ServeHTTP(httptest.NewRecorder(), req)

	if captured != "10.0.0.1:1234" {
		t.Errorf("RemoteAddr = %q, want %q (should not be overwritten)", captured, "10.0.0.1:1234")
	}
}

func TestTrustedRealIP_EmptyNets_NeverTrusts(t *testing.T) {
	var captured string
	h := middleware.TrustedRealIP([]*net.IPNet{})(captureRemoteAddr(t, &captured))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")

	h.ServeHTTP(httptest.NewRecorder(), req)

	if captured != "10.0.0.1:1234" {
		t.Errorf("RemoteAddr = %q, want %q", captured, "10.0.0.1:1234")
	}
}

func TestTrustedRealIP_UntrustedPeer_IgnoresHeaders(t *testing.T) {
	trusted := []*net.IPNet{mustParseCIDR(t, "172.16.0.0/12")}
	var captured string
	h := middleware.TrustedRealIP(trusted)(captureRemoteAddr(t, &captured))

	// Peer 10.0.0.1 is NOT in 172.16.0.0/12
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")

	h.ServeHTTP(httptest.NewRecorder(), req)

	if captured != "10.0.0.1:1234" {
		t.Errorf("RemoteAddr = %q, want %q (untrusted peer)", captured, "10.0.0.1:1234")
	}
}

func TestTrustedRealIP_TrustedPeer_XForwardedFor(t *testing.T) {
	trusted := []*net.IPNet{mustParseCIDR(t, "172.16.0.0/12")}
	var captured string
	h := middleware.TrustedRealIP(trusted)(captureRemoteAddr(t, &captured))

	// Peer is 172.17.0.1 which IS in 172.16.0.0/12
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.17.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")

	h.ServeHTTP(httptest.NewRecorder(), req)

	if captured != "203.0.113.50" {
		t.Errorf("RemoteAddr = %q, want %q", captured, "203.0.113.50")
	}
}

func TestTrustedRealIP_TrustedPeer_XForwardedFor_MultiHop(t *testing.T) {
	// Trust the two internal proxies.
	trusted := []*net.IPNet{
		mustParseCIDR(t, "172.16.0.0/12"),
		mustParseCIDR(t, "10.0.0.0/8"),
	}
	var captured string
	h := middleware.TrustedRealIP(trusted)(captureRemoteAddr(t, &captured))

	// Chain: client 203.0.113.50 -> proxy 10.0.0.1 -> proxy 172.17.0.1
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.17.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 10.0.0.1")

	h.ServeHTTP(httptest.NewRecorder(), req)

	// Should skip 10.0.0.1 (trusted) and return 203.0.113.50 (untrusted).
	if captured != "203.0.113.50" {
		t.Errorf("RemoteAddr = %q, want %q", captured, "203.0.113.50")
	}
}

func TestTrustedRealIP_TrustedPeer_AllXFF_Trusted(t *testing.T) {
	// All IPs in the chain are trusted — no real client can be determined.
	trusted := []*net.IPNet{mustParseCIDR(t, "10.0.0.0/8")}
	var captured string
	h := middleware.TrustedRealIP(trusted)(captureRemoteAddr(t, &captured))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "10.0.0.2, 10.0.0.3")

	h.ServeHTTP(httptest.NewRecorder(), req)

	// All candidates are trusted so RemoteAddr stays as-is.
	if captured != "10.0.0.1:5555" {
		t.Errorf("RemoteAddr = %q, want %q (all XFF trusted)", captured, "10.0.0.1:5555")
	}
}

func TestTrustedRealIP_TrustedPeer_XRealIp_Fallback(t *testing.T) {
	trusted := []*net.IPNet{mustParseCIDR(t, "172.16.0.0/12")}
	var captured string
	h := middleware.TrustedRealIP(trusted)(captureRemoteAddr(t, &captured))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.17.0.1:5555"
	// No X-Forwarded-For, only X-Real-Ip.
	req.Header.Set("X-Real-Ip", "203.0.113.99")

	h.ServeHTTP(httptest.NewRecorder(), req)

	if captured != "203.0.113.99" {
		t.Errorf("RemoteAddr = %q, want %q", captured, "203.0.113.99")
	}
}

func TestTrustedRealIP_TrustedPeer_NoHeaders_KeepsOriginal(t *testing.T) {
	trusted := []*net.IPNet{mustParseCIDR(t, "172.16.0.0/12")}
	var captured string
	h := middleware.TrustedRealIP(trusted)(captureRemoteAddr(t, &captured))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.17.0.1:5555"
	// No forwarded headers at all.

	h.ServeHTTP(httptest.NewRecorder(), req)

	if captured != "172.17.0.1:5555" {
		t.Errorf("RemoteAddr = %q, want %q (no headers)", captured, "172.17.0.1:5555")
	}
}

func TestTrustedRealIP_TrustedPeer_XForwardedFor_TakesPrecedenceOverXRealIp(t *testing.T) {
	trusted := []*net.IPNet{mustParseCIDR(t, "172.16.0.0/12")}
	var captured string
	h := middleware.TrustedRealIP(trusted)(captureRemoteAddr(t, &captured))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.17.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	req.Header.Set("X-Real-Ip", "203.0.113.99")

	h.ServeHTTP(httptest.NewRecorder(), req)

	// X-Forwarded-For should take precedence.
	if captured != "203.0.113.50" {
		t.Errorf("RemoteAddr = %q, want %q (XFF takes precedence)", captured, "203.0.113.50")
	}
}

func TestTrustedRealIP_SingleHostCIDR(t *testing.T) {
	// Trust exactly one IP via /32.
	trusted := []*net.IPNet{mustParseCIDR(t, "172.17.0.1/32")}
	var captured string
	h := middleware.TrustedRealIP(trusted)(captureRemoteAddr(t, &captured))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.17.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")

	h.ServeHTTP(httptest.NewRecorder(), req)

	if captured != "203.0.113.50" {
		t.Errorf("RemoteAddr = %q, want %q", captured, "203.0.113.50")
	}
}

func TestTrustedRealIP_IPv6_TrustedPeer(t *testing.T) {
	trusted := []*net.IPNet{mustParseCIDR(t, "fd00::/8")}
	var captured string
	h := middleware.TrustedRealIP(trusted)(captureRemoteAddr(t, &captured))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[fd00::1]:5555"
	req.Header.Set("X-Forwarded-For", "2001:db8::42")

	h.ServeHTTP(httptest.NewRecorder(), req)

	if captured != "2001:db8::42" {
		t.Errorf("RemoteAddr = %q, want %q", captured, "2001:db8::42")
	}
}

func TestTrustedRealIP_InvalidXFF_Skipped(t *testing.T) {
	trusted := []*net.IPNet{mustParseCIDR(t, "172.16.0.0/12")}
	var captured string
	h := middleware.TrustedRealIP(trusted)(captureRemoteAddr(t, &captured))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.17.0.1:5555"
	req.Header.Set("X-Forwarded-For", "not-an-ip, also-not-ip")

	h.ServeHTTP(httptest.NewRecorder(), req)

	// No valid IP in XFF and no X-Real-Ip, so RemoteAddr stays as-is.
	if captured != "172.17.0.1:5555" {
		t.Errorf("RemoteAddr = %q, want %q (invalid XFF entries)", captured, "172.17.0.1:5555")
	}
}
