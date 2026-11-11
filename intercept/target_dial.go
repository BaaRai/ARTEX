package intercept

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// The HTTP clients the platform owns (WebFetch and the custom http tool)
// connect through PinnedDialer. The hook, the rendered-URL gate and the
// redirect policy decide on a URL, and the client then resolves the name again
// on its own when it connects: a name can answer differently in between. In a
// gated scope the dialer takes a second, independent decision on the address
// it is asked to connect to (the target of the call, of a redirect hop, or a
// proxy). It resolves the name afresh, compares the answer with the pins, and
// connects only to the addresses it compared, never to the name. It never
// waits: an address no pin covers finds or opens the approval request and
// fails the connection at once with TARGET_APPROVAL_PENDING.

// baseDial opens a connection with the dialer settings of http.DefaultTransport.
var baseDial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext

// PinnedDialer returns the DialContext of a platform-owned HTTP client run by
// the tool toolName. The scope is read from the context net/http passes to the
// dial, which carries the values of the request's context: the scope the hook,
// the rendered-URL gate and the redirect policy read.
//
// scoped is whether the client was built for a session with a target scope.
// Such a client never dials without one: a scope missing at the dial means the
// context value was lost on the way, and the dial fails with
// TARGET_GATE_UNAVAILABLE. A client built without a scope (the operator's test
// endpoint) dials as before, and so does every client while the gate is off.
func PinnedDialer(toolName string, scoped bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		scope, ok := TargetScopeFromContext(ctx)
		switch {
		case !ok && scoped:
			return nil, &dialError{code: TargetGateUnavailable, target: addr, reason: "the target scope of the session did not reach the connection"}
		case !ok:
			return baseDial(ctx, network, addr)
		case scope.Enabled == nil || !scope.Enabled():
			if scope.Interceptor != nil {
				return scope.Interceptor.baseDialer()(ctx, network, addr)
			}
			return baseDial(ctx, network, addr)
		case scope.Interceptor == nil:
			return nil, &dialError{code: TargetGateUnavailable, target: addr, reason: "the session's target scope carries no gate"}
		}
		return scope.Interceptor.dialTarget(ctx, scope, toolName, network, addr)
	}
}

// GateOn reports whether ctx carries a target scope whose gate is on.
func GateOn(ctx context.Context) bool {
	_, on := renderedScope(ctx)
	return on
}

func (i *Interceptor) baseDialer() func(ctx context.Context, network, addr string) (net.Conn, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.dial
}

// dialTarget decides one connection of a gated scope and opens it. The asset
// block rules, exemptions and pins are those of the hook, on the target the
// address names, with a fresh lookup. URL rules are not matched: they ran on
// the URL at the hook and on each hop in RedirectCheck.
//
// A derived exemption, and a pin that is name-only for this connection, dial
// the address as received. A stored exemption that holds, and a pin that
// holds every address of the lookup, dial those addresses and nothing else.
func (i *Interceptor) dialTarget(ctx context.Context, scope TargetScope, toolName, network, addr string) (net.Conn, error) {
	input, _ := json.Marshal(map[string]string{"dial": addr})
	ctx = WithCall(ctx, toolName, input)
	t, err := addrTarget(network, addr)
	if err != nil {
		res := undeterminedTarget(ctx, scope, toolName, input, "the address of the connection could not be read ("+err.Error()+")")
		return nil, &dialError{code: res.Code, target: addr, reason: res.Reason}
	}
	blocked := func(res TargetResult) error {
		return &dialError{code: res.Code, target: t.String(), reason: res.Reason}
	}
	c := &targetCheck{i: i, ctx: ctx, scope: scope, tool: toolName, input: input, fresh: true}
	unpinned, res := c.run([]Target{t})
	if res.Code != "" {
		return nil, blocked(res)
	}
	if len(unpinned) > 0 {
		// Wait bound 0: the request is found or created, and the dial does not wait.
		scope.WaitBound = 0
		res = i.RequestTargets(ctx, scope, toolName, input, unpinned, nil)
		if res.Code == TargetApprovalPending {
			res.Reason = notCovered(unpinned[0], scope)
		}
		return nil, blocked(res)
	}
	switch ct := c.checked[0]; ct.cover {
	case coverDerived, coverNameOnly:
		return i.baseDialer()(ctx, network, addr)
	default:
		return i.dialAddrs(ctx, network, ct.ips, t.Port)
	}
}

// addrTarget reads the address net/http dials, host:port with the host in
// ASCII form and the port always written, as the target urlTarget reads from
// the URL.
func addrTarget(network, addr string) (Target, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		return Target{}, fmt.Errorf("network %q is not tcp", network)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return Target{}, err
	}
	kind, h, ok := normalizeHost(host, true)
	if !ok || kind == TargetCIDR {
		return Target{}, fmt.Errorf("host %q", host)
	}
	t := Target{Kind: kind, Host: h, Port: validPort(port)}
	if t.Port == 0 {
		return Target{}, fmt.Errorf("port %q", port)
	}
	return t, nil
}

// dialAddrs connects to the addresses the decision compared, in their canonical
// order: the first that accepts wins. An address of the wrong family for tcp4
// or tcp6 is skipped. When every attempt fails, the last error is returned.
func (i *Interceptor) dialAddrs(ctx context.Context, network string, ips []string, port int) (net.Conn, error) {
	dial := i.baseDialer()
	var last error = &net.AddrError{Err: "no suitable address found", Addr: strings.Join(ips, ", ")}
	for _, ip := range ips {
		a, err := netip.ParseAddr(ip)
		if err != nil || network == "tcp4" && !a.Is4() || network == "tcp6" && !a.Is6() {
			continue
		}
		conn, err := dial(ctx, network, net.JoinHostPort(ip, strconv.Itoa(port)))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

// notCovered says why a connection needs an approval: the addresses the dial
// resolved, and the pin they fall outside of.
func notCovered(r TargetRequest, scope TargetScope) string {
	if r.Pin != nil {
		return fmt.Sprintf("%s now resolves to %s, outside pin v%d (%s) of %s",
			r.Host, strings.Join(r.ResolvedIPs, ", "), r.Pin.Version, strings.Join(r.Pin.PinnedIPs, ", "), scope.scopeName())
	}
	if r.Kind != TargetDomain {
		return fmt.Sprintf("%s is not approved for %s", r.Host, scope.scopeName())
	}
	return fmt.Sprintf("%s resolves to %s and is not approved for %s", r.Host, strings.Join(r.ResolvedIPs, ", "), scope.scopeName())
}

// dialError is what PinnedDialer returns for a connection it does not open.
// Both clients hand the error to the model as the tool result.
type dialError struct {
	code, target, reason string
}

func (e *dialError) Error() string {
	msg := "[ARTEX platform control, not target defense] " + e.code + ": the connection to " + e.target + " was not opened: " + e.reason + "."
	if e.code == TargetApprovalPending {
		msg += " An approval was requested. Retry the same call after the approval."
	}
	return msg + " Do not fetch the target with another tool or by its address."
}
