package control

import (
	"context"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

type Relay struct {
	cancel  context.CancelFunc
	done    chan struct{}
	address string
}

func allowed(addr net.Addr, sources []netip.Prefix) bool {
	a, _, e := net.SplitHostPort(addr.String())
	if e != nil {
		return false
	}
	ip, e := netip.ParseAddr(a)
	if e != nil {
		return false
	}
	ip = ip.Unmap()
	for _, s := range sources {
		if s.Contains(ip) {
			return true
		}
	}
	return false
}
func OpenRelay(parent context.Context, proto, listen string, port int, target string, sources []netip.Prefix) (*Relay, error) {
	ctx, cancel := context.WithCancel(parent)
	r := &Relay{cancel: cancel, done: make(chan struct{})}
	addr := net.JoinHostPort(listen, strconv.Itoa(port))
	if proto == "tcp" {
		ln, e := net.Listen("tcp4", addr)
		if e != nil {
			cancel()
			return nil, e
		}
		r.address = ln.Addr().String()
		go func() { <-ctx.Done(); ln.Close() }()
		go func() {
			defer close(r.done)
			var wg sync.WaitGroup
			defer wg.Wait()
			sem := make(chan struct{}, 256)
			for {
				c, e := ln.Accept()
				if e != nil {
					return
				}
				if !allowed(c.RemoteAddr(), sources) {
					c.Close()
					continue
				}
				select {
				case sem <- struct{}{}:
				default:
					c.Close()
					continue
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer func() { <-sem }()
					defer c.Close()
					up, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp4", target)
					if e != nil {
						return
					}
					defer up.Close()
					done := make(chan struct{})
					defer close(done)
					go func() {
						select {
						case <-ctx.Done():
							c.Close()
							up.Close()
						case <-done:
						}
					}()
					copyDone := make(chan struct{})
					go func() {
						io.Copy(up, c)
						if t, ok := up.(*net.TCPConn); ok {
							t.CloseWrite()
						}
						close(copyDone)
					}()
					io.Copy(c, up)
					c.Close()
					up.Close()
					<-copyDone
				}()
			}
		}()
	} else {
		ln, e := net.ListenPacket("udp4", addr)
		if e != nil {
			cancel()
			return nil, e
		}
		r.address = ln.LocalAddr().String()
		go func() { <-ctx.Done(); ln.Close() }()
		go func() {
			defer close(r.done)
			var mu sync.Mutex
			peers := map[string]net.Conn{}
			var wg sync.WaitGroup
			defer wg.Wait()
			defer func() {
				mu.Lock()
				defer mu.Unlock()
				for _, c := range peers {
					c.Close()
				}
			}()
			buf := make([]byte, 65535)
			for {
				n, source, e := ln.ReadFrom(buf)
				if e != nil {
					return
				}
				if !allowed(source, sources) {
					continue
				}
				key := source.String()
				mu.Lock()
				up := peers[key]
				if up == nil && len(peers) < 256 {
					up, e = net.DialTimeout("udp4", target, 2*time.Second)
					if e == nil {
						peers[key] = up
						wg.Add(1)
						go func(up net.Conn, source net.Addr) {
							defer wg.Done()
							defer up.Close()
							defer func() { mu.Lock(); delete(peers, source.String()); mu.Unlock() }()
							b := make([]byte, 65535)
							for {
								up.SetReadDeadline(time.Now().Add(30 * time.Second))
								n, e := up.Read(b)
								if e != nil {
									return
								}
								if _, e = ln.WriteTo(b[:n], source); e != nil {
									return
								}
							}
						}(up, source)
					}
				}
				mu.Unlock()
				if up != nil {
					up.SetWriteDeadline(time.Now().Add(2 * time.Second))
					_, _ = up.Write(buf[:n])
				}
			}
		}()
	}
	return r, nil
}
func (r *Relay) Close() { r.cancel(); <-r.done }
