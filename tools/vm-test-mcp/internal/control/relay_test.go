package control

import (
	"context"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestTCPAndUDPRelays(t *testing.T) {
	for _, proto := range []string{"tcp", "udp"} {
		t.Run(proto, func(t *testing.T) {
			var target string
			var stop func()
			if proto == "tcp" {
				l, e := net.Listen("tcp4", "127.0.0.1:0")
				if e != nil {
					t.Fatal(e)
				}
				target = l.Addr().String()
				stop = func() { l.Close() }
				go func() {
					for {
						c, e := l.Accept()
						if e != nil {
							return
						}
						go func() { defer c.Close(); io.Copy(c, c) }()
					}
				}()
			} else {
				l, e := net.ListenPacket("udp4", "127.0.0.1:0")
				if e != nil {
					t.Fatal(e)
				}
				target = l.LocalAddr().String()
				stop = func() { l.Close() }
				go func() {
					buf := make([]byte, 2048)
					for {
						n, a, e := l.ReadFrom(buf)
						if e != nil {
							return
						}
						l.WriteTo(buf[:n], a)
					}
				}()
			}
			defer stop()
			r, e := OpenRelay(context.Background(), proto, "127.0.0.1", 0, target, []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")})
			if e != nil {
				t.Fatal(e)
			}
			c, e := net.Dial(proto, r.address)
			if e != nil {
				t.Fatal(e)
			}
			c.SetDeadline(time.Now().Add(time.Second))
			if _, e = c.Write([]byte("hello")); e != nil {
				t.Fatal(e)
			}
			buf := make([]byte, 5)
			if _, e = io.ReadFull(c, buf); e != nil {
				t.Fatal(e)
			}
			if string(buf) != "hello" {
				t.Fatal(string(buf))
			}
			r.Close()
			c.Close()
			r, e = OpenRelay(context.Background(), proto, "127.0.0.1", 0, target, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")})
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			c, e = net.Dial(proto, r.address)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(100 * time.Millisecond))
			c.Write([]byte("denied"))
			if n, _ := c.Read(buf); n != 0 {
				t.Fatal("unauthorized source reached relay")
			}
		})
	}
}
