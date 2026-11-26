package traffic

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/Autumn-27/artex/egress"
)

// remoteAddrConn reports a chosen remote address so negotiateAuth can be driven
// as if the client connected from that address.
type remoteAddrConn struct {
	net.Conn
	remote net.Addr
}

func (c remoteAddrConn) RemoteAddr() net.Addr { return c.remote }

// negotiateFrom runs negotiateAuth for a client at remote that offers methods
// and, when the server selects username/password, sends user/pass. It returns
// the server's method selection and negotiateAuth's results.
func negotiateFrom(t *testing.T, remote net.Addr, methods []byte, user, pass string) (selected byte, paced, ok bool) {
	t.Helper()
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))

	type result struct{ paced, ok bool }
	done := make(chan result, 1)
	go func() {
		p, o := (&socks5Server{}).negotiateAuth(remoteAddrConn{Conn: server, remote: remote})
		done <- result{p, o}
	}()

	if _, err := client.Write(append([]byte{socks5Version, byte(len(methods))}, methods...)); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	var sel [2]byte
	if _, err := io.ReadFull(client, sel[:]); err != nil {
		t.Fatalf("read method selection: %v", err)
	}
	if sel[1] == socks5AuthUserPass {
		msg := []byte{socks5UserPassVer, byte(len(user))}
		msg = append(msg, user...)
		msg = append(msg, byte(len(pass)))
		msg = append(msg, pass...)
		if _, err := client.Write(msg); err != nil {
			t.Fatalf("write credentials: %v", err)
		}
		var resp [2]byte
		if _, err := io.ReadFull(client, resp[:]); err != nil {
			t.Fatalf("read auth response: %v", err)
		}
	}
	r := <-done
	return sel[1], r.paced, r.ok
}

func TestSOCKS5NonLoopbackClientRefusedForEveryMethod(t *testing.T) {
	remote := &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 40000}
	cases := []struct {
		name    string
		methods []byte
	}{
		{"userpass only", []byte{socks5AuthUserPass}},
		{"none and userpass", []byte{socks5AuthNone, socks5AuthUserPass}},
		{"none only", []byte{socks5AuthNone}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel, _, ok := negotiateFrom(t, remote, tc.methods, "anyone", "anything")
			if ok || sel != socks5AuthNoAccept {
				t.Fatalf("non-loopback client: selected=%#x ok=%v, want %#x and refused", sel, ok, socks5AuthNoAccept)
			}
		})
	}
}

func TestSOCKS5LoopbackClientStillAccepted(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1"} {
		remote := &net.TCPAddr{IP: net.ParseIP(ip), Port: 40000}

		sel, paced, ok := negotiateFrom(t, remote, []byte{socks5AuthNone, socks5AuthUserPass}, egress.PaceUser, egress.PaceUser)
		if !ok || sel != socks5AuthUserPass || !paced {
			t.Fatalf("%s userpass with pace marker: selected=%#x paced=%v ok=%v", ip, sel, paced, ok)
		}

		sel, paced, ok = negotiateFrom(t, remote, []byte{socks5AuthNone}, "", "")
		if !ok || sel != socks5AuthNone || paced {
			t.Fatalf("%s no-auth: selected=%#x paced=%v ok=%v", ip, sel, paced, ok)
		}
	}
}
