package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/keys"
	pb "github.com/vantasnerdan/herdr-ember-fleet/internal/relay/relaypb"
)

// Dial connects to a relay whose certificate key must match pin, presenting id
// as the client certificate. TLS 1.3 checks the relay's key before the client
// sends its own certificate, so a wrong relay learns nothing.
func Dial(addr, pin string, id *keys.Identity) (*grpc.ClientConn, pb.RelayClient, error) {
	cfg := &tls.Config{
		Certificates:          []tls.Certificate{id.Cert},
		InsecureSkipVerify:    true, // replaced by the pin check below
		VerifyPeerCertificate: keys.VerifyPinned(func(fp string) bool { return keys.Equal(fp, pin) }),
		MinVersion:            tls.VersionTLS13,
	}
	conn, err := grpc.NewClient("dns:///"+addr,
		grpc.WithTransportCredentials(credentials.NewTLS(cfg)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 10 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}),
		grpc.WithConnectParams(grpc.ConnectParams{MinConnectTimeout: 10 * time.Second}),
	)
	if err != nil {
		return nil, nil, err
	}
	return conn, pb.NewRelayClient(conn), nil
}

// Backoff is jittered exponential backoff from 0.5 s to 15 s, reset after a
// minute of health.
type Backoff struct {
	n       int
	healthy time.Time
}

// Wait sleeps for the next delay, or returns early when ctx ends.
func (b *Backoff) Wait(ctx context.Context) {
	if !b.healthy.IsZero() && time.Since(b.healthy) > time.Minute {
		b.n = 0
	}
	b.healthy = time.Time{}
	d := 500 * time.Millisecond << min(b.n, 5)
	if d > 15*time.Second {
		d = 15 * time.Second
	}
	b.n++
	d = d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// Healthy marks the start of a good connection.
func (b *Backoff) Healthy() { b.healthy = time.Now() }

// chunkClient is either side's client stream (Open or Accept).
type chunkClient interface {
	Send(*pb.Chunk) error
	Recv() (*pb.Chunk, error)
	CloseSend() error
}

// StreamConn makes a relay channel look like a net.Conn so TLS can run over it.
//
// CloseWrite half-closes (the other side reads EOF). Close is graceful: it
// half-closes and lets the other side finish, but a timer started first cuts
// the stream after three seconds whatever is blocked. Cut aborts at once.
// Deadlines work: an operation that outlives its deadline aborts the channel
// (a gRPC send or receive cannot be interrupted any other way), and a
// deadline already in the past fails new operations without aborting.
type StreamConn struct {
	s      chunkClient
	cancel context.CancelFunc

	rmu  sync.Mutex
	buf  []byte
	rerr error

	wmu    sync.Mutex
	closed bool

	dmu      sync.Mutex
	rop, wop opTimer
	timedOut atomic.Bool // a deadline, not a peer or Close, ended the stream
	closing  atomic.Bool
}

// opTimer is one direction's deadline and the timer for an operation in
// progress. Setters reschedule the timer of an operation already blocked.
type opTimer struct {
	deadline time.Time
	active   bool
	timer    *time.Timer
}

const maxChunk = 32 << 10

// OpenChannel opens a channel from a hub to a dock.
func OpenChannel(ctx context.Context, c pb.RelayClient, dock string) (*StreamConn, error) {
	ctx, cancel := context.WithCancel(ctx)
	ctx = metadata.AppendToOutgoingContext(ctx, "ember-dock", dock)
	s, err := c.Open(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	return &StreamConn{s: s, cancel: cancel}, nil
}

// AcceptChannel claims a channel the relay announced to a dock.
func AcceptChannel(ctx context.Context, c pb.RelayClient, id string) (*StreamConn, error) {
	ctx, cancel := context.WithCancel(ctx)
	ctx = metadata.AppendToOutgoingContext(ctx, "ember-channel", id)
	s, err := c.Accept(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	return &StreamConn{s: s, cancel: cancel}, nil
}

// begin marks an operation in progress and arms its deadline.
func (c *StreamConn) begin(o *opTimer) error {
	c.dmu.Lock()
	defer c.dmu.Unlock()
	if c.timedOut.Load() || (!o.deadline.IsZero() && !time.Now().Before(o.deadline)) {
		return deadlineErr
	}
	o.active = true
	c.armLocked(o)
	return nil
}

func (c *StreamConn) end(o *opTimer) {
	c.dmu.Lock()
	o.active = false
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
	c.dmu.Unlock()
}

// armLocked (re)schedules the abort for an operation in progress.
func (c *StreamConn) armLocked(o *opTimer) {
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
	if !o.active || o.deadline.IsZero() {
		return
	}
	o.timer = time.AfterFunc(time.Until(o.deadline), func() {
		c.timedOut.Store(true)
		c.cancel()
	})
}

func (c *StreamConn) setDeadline(o *opTimer, t time.Time) {
	c.dmu.Lock()
	o.deadline = t
	c.armLocked(o)
	c.dmu.Unlock()
}

var deadlineErr = fmt.Errorf("relay channel: %w", os.ErrDeadlineExceeded)

func (c *StreamConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for len(c.buf) == 0 {
		if c.rerr != nil {
			return 0, c.rerr
		}
		if err := c.begin(&c.rop); err != nil {
			return 0, err
		}
		ch, err := c.s.Recv()
		c.end(&c.rop)
		if err != nil {
			switch {
			case err == io.EOF:
				c.rerr = io.EOF
			case c.timedOut.Load():
				c.rerr = deadlineErr
			default:
				c.rerr = errors.New("relay channel closed: " + err.Error())
			}
			continue
		}
		if ch.Eof {
			c.rerr = io.EOF
		}
		c.buf = ch.Data
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

func (c *StreamConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	if err := c.begin(&c.wop); err != nil {
		return 0, err
	}
	defer c.end(&c.wop)
	n := 0
	for len(p) > 0 {
		k := min(len(p), maxChunk)
		if err := c.s.Send(&pb.Chunk{Data: append([]byte(nil), p[:k]...)}); err != nil {
			if c.timedOut.Load() {
				return n, deadlineErr
			}
			return n, err
		}
		n += k
		p = p[k:]
	}
	return n, nil
}

// CloseWrite ends this direction; the other side reads EOF.
func (c *StreamConn) CloseWrite() error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.s.CloseSend()
}

// Close ends the channel gracefully (see the type's comment). It never blocks.
func (c *StreamConn) Close() error {
	if !c.closing.CompareAndSwap(false, true) {
		return nil
	}
	force := time.AfterFunc(3*time.Second, c.cancel) // started before waiting on any lock
	go func() {
		defer force.Stop()
		c.CloseWrite()
		c.rmu.Lock()
		for c.rerr == nil {
			ch, err := c.s.Recv()
			if err != nil || ch.Eof {
				break
			}
		}
		c.rmu.Unlock()
		c.cancel()
	}()
	return nil
}

// Cut cancels the channel at once in both directions.
func (c *StreamConn) Cut() { c.cancel() }

type relayAddr struct{}

func (relayAddr) Network() string { return "relay" }
func (relayAddr) String() string  { return "relay" }

func (c *StreamConn) LocalAddr() net.Addr  { return relayAddr{} }
func (c *StreamConn) RemoteAddr() net.Addr { return relayAddr{} }

func (c *StreamConn) SetDeadline(t time.Time) error {
	c.setDeadline(&c.rop, t)
	c.setDeadline(&c.wop, t)
	return nil
}

func (c *StreamConn) SetReadDeadline(t time.Time) error  { c.setDeadline(&c.rop, t); return nil }
func (c *StreamConn) SetWriteDeadline(t time.Time) error { c.setDeadline(&c.wop, t); return nil }
