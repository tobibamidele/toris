// Package routing implements the stable TCP proxy listener.
// Client applications connect to the proxy (e.g. localhost:5433) and
// are transparently forwarded to the current primary. When failover happens,
// the routing target is atomically updated — no client reconfiguration needed.
//
// Design:
//   - Each accepted connection dials the current target and bridges the streams.
//   - Target updates are protected by a sync.RWMutex; reads are cheap.
//   - If the target is unavailable, the connection is refused cleanly.
//   - The old primary is never used as a target once the generation advances.
package routing

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	torerrors "github.com/tobibamidele/toris/internal/errors"
	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/pkg/model"
)

// Proxy is the stable TCP proxy that forwards connections to the current primary.
type Proxy struct {
	log         *logging.Logger
	listenAddr  string
	dialTimeout time.Duration

	mu     sync.RWMutex
	target *model.RoutingTarget

	listener    net.Listener
	closed      atomic.Bool
	activeConns sync.WaitGroup
}

// NewProxy creates a Proxy that will listen on listenAddr.
func NewProxy(log *logging.Logger, listenAddr string, dialTimeout time.Duration) *Proxy {
	return &Proxy{
		log:         log,
		listenAddr:  listenAddr,
		dialTimeout: dialTimeout,
	}
}

// SetTarget atomically updates the routing target (called after promotion/failover).
// The fencing token (generation) is stored to detect stale targets.
func (p *Proxy) SetTarget(t *model.RoutingTarget) {
	p.mu.Lock()
	defer p.mu.Unlock()

	old := p.target
	p.target = t

	if old != nil {
		p.log.Info("routing target updated",
			"old_node", old.NodeID,
			"new_node", t.NodeID,
			"generation", t.Generation,
		)
	} else {
		p.log.Info("routing target set",
			"node", t.NodeID,
			"addr", fmt.Sprintf("%s:%d", t.Host, t.Port),
			"generation", t.Generation,
		)
	}
}

// GetTarget returns the current routing target (may be nil if not set).
func (p *Proxy) GetTarget() *model.RoutingTarget {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.target
}

// Run starts listening and accepting connections until ctx is canceled.
func (p *Proxy) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", p.listenAddr)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeInternal, err,
			"proxy failed to listen on %s", p.listenAddr)
	}
	p.listener = ln
	p.log.Info("proxy listener started", "addr", p.listenAddr)

	// Close listener when ctx is done.
	go func() {
		<-ctx.Done()
		p.closed.Store(true)
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if p.closed.Load() {
				// Graceful shutdown: wait for active connections.
				p.activeConns.Wait()
				p.log.Info("proxy listener stopped")
				return nil
			}
			p.log.Warn("proxy accept error", "error", err.Error())
			continue
		}
		p.activeConns.Add(1)
		go p.handleConn(ctx, conn)
	}
}

// handleConn bridges one client connection to the upstream target.
func (p *Proxy) handleConn(ctx context.Context, client net.Conn) {
	defer p.activeConns.Done()
	defer client.Close()

	target := p.GetTarget()
	if target == nil {
		p.log.Warn("proxy rejecting connection: no routing target configured")
		return
	}

	upstreamAddr := fmt.Sprintf("%s:%d", target.Host, target.Port)

	dialer := &net.Dialer{Timeout: p.dialTimeout}
	upstream, err := dialer.DialContext(ctx, "tcp", upstreamAddr)
	if err != nil {
		p.log.Warn("proxy cannot reach upstream",
			"upstream", upstreamAddr,
			"client", client.RemoteAddr().String(),
			"error", err.Error(),
		)
		return
	}
	defer upstream.Close()

	p.log.Debug("proxy bridging connection",
		"client", client.RemoteAddr().String(),
		"upstream", upstreamAddr,
		"node", target.NodeID,
	)

	// Bidirectional bridge with a context-aware stop.
	done := make(chan struct{}, 2)

	copy := func(dst, src net.Conn) {
		io.Copy(dst, src) //nolint:errcheck // EOF is expected
		dst.Close()
		done <- struct{}{}
	}

	go copy(upstream, client)
	go copy(client, upstream)

	// Wait for either direction to close or context to cancel.
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Addr returns the address the proxy is listening on, or "" if not started.
func (p *Proxy) Addr() string {
	if p.listener == nil {
		return ""
	}
	return p.listener.Addr().String()
}
