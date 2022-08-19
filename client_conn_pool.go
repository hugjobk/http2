package http2

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"golang.org/x/net/http2"
)

const NextProtoTLS = "h2"

type clientConnPool struct {
	t      *Transport
	mtx    sync.Mutex
	groups map[string]*clientConnGroup
	keys   map[*http2.ClientConn]string

	maxConnsPerHost int
}

func (p *clientConnPool) GetClientConn(req *http.Request, addr string) (*http2.ClientConn, error) {
	if p.t.DisableKeepAlives {
		return dialClientConn(p.t.Transport, req.Context(), addr)
	}
	p.mtx.Lock()
	g, ok := p.groups[addr]
	if !ok {
		g = &clientConnGroup{
			p:    p,
			addr: addr,
			keys: make(map[*http2.ClientConn]int),
		}
		p.groups[addr] = g
	}
	p.mtx.Unlock()
	return g.getClientConn(req)
}

func (p *clientConnPool) MarkDead(conn *http2.ClientConn) {
	p.mtx.Lock()
	addr, ok := p.keys[conn]
	if !ok {
		p.mtx.Unlock()
		return
	}
	delete(p.keys, conn)
	g, ok := p.groups[addr]
	if !ok {
		p.mtx.Unlock()
		return
	}
	p.mtx.Unlock()
	g.mtx.Lock()
	g.removeClientConn(conn)
	if len(g.conns) == 0 {
		p.mtx.Lock()
		delete(p.groups, addr)
		p.mtx.Unlock()
	}
	g.mtx.Unlock()
}

type clientConnGroup struct {
	p       *clientConnPool
	addr    string
	connIdx int
	mtx     sync.Mutex
	conns   []*http2.ClientConn
	keys    map[*http2.ClientConn]int
	dialing int32
}

func (g *clientConnGroup) getClientConn(req *http.Request) (*http2.ClientConn, error) {
	g.mtx.Lock()
	defer g.mtx.Unlock()
	if len(g.conns) == 0 {
		conn, err := dialClientConn(g.p.t.Transport, req.Context(), g.addr)
		if err != nil {
			return nil, err
		}
		g.p.mtx.Lock()
		g.addClientConn(conn)
		g.p.mtx.Unlock()
		return conn, nil
	} else if g.p.maxConnsPerHost <= 0 || g.p.maxConnsPerHost > len(g.conns) {
		go g.dialClientConn(req)
	}
	for retry := 0; retry < len(g.conns); retry++ {
		g.connIdx++
		conn := g.conns[g.connIdx%len(g.conns)]
		if conn.CanTakeNewRequest() {
			return conn, nil
		}
	}
	return nil, fmt.Errorf("no available connection to %s", g.addr)
}

func (g *clientConnGroup) dialClientConn(req *http.Request) {
	if !atomic.CompareAndSwapInt32(&g.dialing, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&g.dialing, 0)
	g.mtx.Lock()
	if len(g.conns) >= g.p.maxConnsPerHost {
		g.mtx.Unlock()
		return
	}
	g.mtx.Unlock()
	conn, err := dialClientConn(g.p.t.Transport, req.Context(), g.addr)
	if err != nil {
		fmt.Printf("error dialing '%s': %s", g.addr, err)
		return
	}
	g.mtx.Lock()
	g.p.mtx.Lock()
	g.addClientConn(conn)
	g.p.mtx.Unlock()
	g.mtx.Unlock()
}

func (g *clientConnGroup) addClientConn(conn *http2.ClientConn) {
	g.keys[conn] = len(g.conns)
	g.conns = append(g.conns, conn)
	g.p.keys[conn] = g.addr
}

func (g *clientConnGroup) removeClientConn(conn *http2.ClientConn) {
	if idx, ok := g.keys[conn]; ok {
		if idx == len(g.conns)-1 {
			g.conns = g.conns[:len(g.conns)-1]
		} else {
			lastConn := g.conns[len(g.conns)-1]
			g.conns[idx] = lastConn
			g.conns = g.conns[:len(g.conns)-1]
			g.keys[lastConn] = idx
			delete(g.keys, conn)
		}
	}
}

func dialClientConn(t *http2.Transport, ctx context.Context, addr string) (*http2.ClientConn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	var conn net.Conn
	if t.DialTLS != nil {
		conn, err = t.DialTLS("tcp", addr, newTLSConfig(t, host))
		if err != nil {
			return nil, err
		}
	} else {
		tlsConn, err := dialTLSWithContext(ctx, "tcp", addr, newTLSConfig(t, host))
		if err != nil {
			return nil, err
		}
		state := tlsConn.ConnectionState()
		if state.NegotiatedProtocol != NextProtoTLS {
			return nil, fmt.Errorf("http2: unexpected ALPN protocol %q; want %q", state.NegotiatedProtocol, NextProtoTLS)
		}
		conn = tlsConn
	}
	return t.NewClientConn(conn)
}

func newTLSConfig(t *http2.Transport, host string) *tls.Config {
	var cfg tls.Config
	if t.TLSClientConfig != nil {
		cfg = *t.TLSClientConfig.Clone()
	}
	if !strSliceContains(cfg.NextProtos, NextProtoTLS) {
		cfg.NextProtos = append([]string{NextProtoTLS}, cfg.NextProtos...)
	}
	if cfg.ServerName == "" {
		cfg.ServerName = host
	}
	return &cfg
}

func dialTLSWithContext(ctx context.Context, network string, addr string, cfg *tls.Config) (*tls.Conn, error) {
	d := tls.Dialer{Config: cfg}
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	tlsConn := conn.(*tls.Conn)
	if err := tlsConn.Handshake(); err != nil {
		return nil, err
	}
	if cfg.InsecureSkipVerify {
		return tlsConn, nil
	}
	if err := tlsConn.VerifyHostname(cfg.ServerName); err != nil {
		return nil, err
	}
	return tlsConn, nil
}

func strSliceContains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
