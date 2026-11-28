package mcphttp

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

// Remote MCP endpoints are configured by the operator, and persistence tools
// that change them need operator approval. This guard is not a privilege
// boundary: it is the same defence-in-depth default that notify/ uses for
// webhook delivery, so a configured URL cannot silently reach loopback,
// link-local or the cloud metadata endpoint. A local MCP server is a legitimate
// setup, hence the explicit opt-out rather than a hardcoded allowance.
const AllowLocalTargetsEnv = "ARTEX_MCP_ALLOW_LOCAL"

func allowLocalTargets() bool {
	v := strings.TrimSpace(os.Getenv(AllowLocalTargetsEnv))
	return v == "1" || strings.EqualFold(v, "true")
}

// isBlockedDialIP mirrors notify's isBlockedDialIP. RFC1918 ranges are
// intentionally allowed: LAN MCP servers are a normal deployment, while
// loopback, link-local (including 169.254.169.254), unspecified and multicast
// are the sensitive addresses the default should refuse.
func isBlockedDialIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// IPv4-mapped IPv6 must be converted back or it bypasses the IPv4 checks.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// blockInternalDial is a Dialer.Control hook: it sees the address actually
// dialled, so it also covers DNS rebinding and same-origin redirects.
func blockInternalDial(_, address string, _ syscall.RawConn) error {
	if allowLocalTargets() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("cannot parse mcp target address %q", host)
	}
	if isBlockedDialIP(ip) {
		return fmt.Errorf("mcp: refusing to dial loopback/link-local address %s (set %s=1 for a local MCP server)", ip, AllowLocalTargetsEnv)
	}
	return nil
}

// mcpTransport is shared by the Streamable HTTP and legacy SSE clients. The
// proxy is cleared deliberately: with a proxy the dial hook would see the
// proxy's address, not the MCP endpoint, and could not enforce the guard.
func mcpTransport(insecure bool) *http.Transport {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t = &http.Transport{}
	} else {
		t = t.Clone()
	}
	t.Proxy = nil
	t.DialContext = (&net.Dialer{Timeout: 10 * time.Second, Control: blockInternalDial}).DialContext
	if insecure {
		tc := &tls.Config{}
		if t.TLSClientConfig != nil {
			tc = t.TLSClientConfig.Clone()
		}
		tc.InsecureSkipVerify = true //nolint:gosec // explicit opt-out for self-signed MCP servers
		t.TLSClientConfig = tc
	}
	return t
}
