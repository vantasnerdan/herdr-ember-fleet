// Package relay is the meeting point between hubs and docks. It authenticates
// members by their TLS client-certificate key, keeps track of which docks are
// online, and joins a hub's channel to a dock's. What flows through a channel is
// end-to-end TLS between hub and dock; the relay only copies bytes.
package relay

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/keys"
	pb "github.com/vantasnerdan/herdr-ember-fleet/internal/relay/relaypb"
)

const (
	RoleHub  = "hub"
	RoleDock = "dock"

	acceptTimeout      = 10 * time.Second
	maxChannelsPerDock = 256
	maxChannelsPerHub  = 1024
	maxPendingInvites  = 200
)

// Member is an enrolled key.
type Member struct {
	Role     string `json:"role"`
	Hub      string `json:"hub,omitempty"` // for docks: the hub that invited it
	Name     string `json:"name,omitempty"`
	LinkID   string `json:"link_id,omitempty"`
	Enrolled int64  `json:"enrolled"`
	LastSeen int64  `json:"last_seen,omitempty"`
}

// Ticket is a one-time enrollment ticket, stored by hash.
type Ticket struct {
	Role    string `json:"role"`
	Hub     string `json:"hub,omitempty"`
	LinkID  string `json:"link_id,omitempty"`
	Name    string `json:"name,omitempty"`
	Expires int64  `json:"expires"`
	Used    bool   `json:"used,omitempty"`
}

// State is what the relay keeps on disk: public-key fingerprints and ticket hashes only.
type State struct {
	Members map[string]*Member `json:"members"`
	Tickets map[string]*Ticket `json:"tickets"`
}

type dockLine struct {
	fp      string
	version string
	since   time.Time
	opens   chan string
	cancel  context.CancelFunc
}

type pending struct {
	hub, dock string
	accept    chan pb.Relay_AcceptServer
	done      chan struct{} // closed when the splice ends; Accept returns then
	cancel    context.CancelFunc
}

type channelRef struct {
	hub, dock string
	cancel    context.CancelFunc
}

// Server implements the Relay service.
type Server struct {
	pb.UnimplementedRelayServer

	dir   string
	id    *keys.Identity
	mu    sync.Mutex
	state State

	online   map[string]*dockLine
	pending  map[string]*pending
	channels map[string]*channelRef
	watchers map[string]map[chan struct{}]context.CancelFunc // hub fp -> directory watchers
	hubOpen  map[string]int                                  // channels reserved per hub (pending + active)
	dockOpen map[string]int                                  // channels reserved per dock (pending + active)
}

// Open loads (or creates) the relay's key and state from dir.
func Open(dir string) (*Server, error) {
	id, err := keys.LoadOrCreate(dir, "ember-fleet-relay")
	if err != nil {
		return nil, err
	}
	s := &Server{
		dir:      dir,
		id:       id,
		online:   map[string]*dockLine{},
		pending:  map[string]*pending{},
		channels: map[string]*channelRef{},
		watchers: map[string]map[chan struct{}]context.CancelFunc{},
		hubOpen:  map[string]int{},
		dockOpen: map[string]int{},
	}
	s.state = State{Members: map[string]*Member{}, Tickets: map[string]*Ticket{}}
	if b, err := os.ReadFile(filepath.Join(dir, "relay.json")); err == nil {
		if err := json.Unmarshal(b, &s.state); err != nil {
			return nil, fmt.Errorf("relay.json: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if s.state.Members == nil {
		s.state.Members = map[string]*Member{}
	}
	if s.state.Tickets == nil {
		s.state.Tickets = map[string]*Ticket{}
	}
	return s, nil
}

// Identity is the relay's own key.
func (s *Server) Identity() *keys.Identity { return s.id }

// saveLocked writes the state; the caller holds s.mu.
func (s *Server) saveLocked() error {
	// Keep used and expired tickets for a week past expiry, so a reused link
	// gets a clear "already used" answer; then forget them.
	cutoff := time.Now().Add(-7 * 24 * time.Hour).UnixMilli()
	for h, t := range s.state.Tickets {
		if t.Expires < cutoff {
			delete(s.state.Tickets, h)
		}
	}
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	return keys.WriteFileAtomic(filepath.Join(s.dir, "relay.json"), b, 0o600)
}

// AddTicket records a ticket (by hash). Used by `relay hub-link`.
func (s *Server) AddTicket(hash []byte, t Ticket) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Tickets[hex.EncodeToString(hash)] = &t
	return s.saveLocked()
}

// Members lists enrolled members, for `relay status`.
func (s *Server) Members() map[string]Member {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]Member{}
	for fp, m := range s.state.Members {
		mm := *m
		if d := s.online[fp]; d != nil {
			mm.LastSeen = time.Now().UnixMilli()
		}
		out[fp] = mm
	}
	return out
}

// RemoveMember forgets a member and cuts its channels, for `relay remove`.
func (s *Server) RemoveMember(fp string) bool {
	s.mu.Lock()
	m := s.state.Members[fp]
	if m == nil {
		s.mu.Unlock()
		return false
	}
	delete(s.state.Members, fp)
	_ = s.saveLocked()
	cut := s.cutLocked(fp)
	hub := m.Hub
	s.mu.Unlock()
	for _, c := range cut {
		c()
	}
	s.notify(hub)
	return true
}

func (s *Server) caller(ctx context.Context) (string, *Member, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", nil, status.Error(codes.Unauthenticated, "no peer")
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", nil, status.Error(codes.Unauthenticated, "not TLS")
	}
	fp, err := keys.PeerFingerprint(ti.State)
	if err != nil {
		return "", nil, status.Error(codes.Unauthenticated, "no client certificate")
	}
	s.mu.Lock()
	m := s.state.Members[fp]
	s.mu.Unlock()
	return fp, m, nil
}

func (s *Server) need(ctx context.Context, role string) (string, *Member, error) {
	fp, m, err := s.caller(ctx)
	if err != nil {
		return "", nil, err
	}
	if m == nil || m.Role != role {
		return "", nil, status.Error(codes.PermissionDenied, "this key is not enrolled as a "+role)
	}
	return fp, m, nil
}

// Enroll redeems a one-time ticket for the caller's key.
func (s *Server) Enroll(ctx context.Context, req *pb.EnrollRequest) (*pb.EnrollReply, error) {
	fp, existing, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Ticket) != 32 {
		return nil, status.Error(codes.InvalidArgument, "bad ticket")
	}
	h := hex.EncodeToString(ticketHash(req.Ticket))
	s.mu.Lock()
	t := s.state.Tickets[h]
	now := time.Now().UnixMilli()
	switch {
	case t == nil:
		s.mu.Unlock()
		return nil, status.Error(codes.PermissionDenied, "unknown ticket (the link may be wrong or cancelled)")
	case t.Used:
		s.mu.Unlock()
		return nil, status.Error(codes.PermissionDenied, "this link was already used")
	case t.Expires < now:
		s.mu.Unlock()
		return nil, status.Error(codes.PermissionDenied, "this link has expired")
	case existing != nil:
		s.mu.Unlock()
		return nil, status.Error(codes.AlreadyExists, "this key is already enrolled")
	}
	if t.Role == RoleDock {
		if hub := s.state.Members[t.Hub]; hub == nil || hub.Role != RoleHub {
			s.mu.Unlock()
			return nil, status.Error(codes.FailedPrecondition, "the hub that made this link is no longer on this relay")
		}
	}
	t.Used = true
	s.state.Members[fp] = &Member{Role: t.Role, Hub: t.Hub, Name: t.Name, LinkID: t.LinkID, Enrolled: now}
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return nil, status.Error(codes.Internal, "could not save")
	}
	hub := t.Hub
	role := t.Role
	s.mu.Unlock()
	log.Printf("enrolled %s %s (%s)", role, keys.Short(fp), t.Name)
	if role == RoleDock {
		s.notify(hub)
	}
	return &pb.EnrollReply{Role: role, MemberId: fp}, nil
}

// Invite records a dock ticket for the calling hub.
func (s *Server) Invite(ctx context.Context, req *pb.InviteRequest) (*pb.InviteReply, error) {
	hub, _, err := s.need(ctx, RoleHub)
	if err != nil {
		return nil, err
	}
	if len(req.TicketHash) != 32 || req.LinkId == "" || len(req.LinkId) > 64 || len(req.Name) > 64 {
		return nil, status.Error(codes.InvalidArgument, "bad invite")
	}
	maxExp := time.Now().Add(8 * 24 * time.Hour).UnixMilli()
	if req.ExpiresUnixMs > maxExp {
		req.ExpiresUnixMs = maxExp
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, t := range s.state.Tickets {
		if t.Hub == hub && !t.Used && t.Expires > time.Now().UnixMilli() {
			n++
		}
	}
	if n >= maxPendingInvites {
		return nil, status.Error(codes.ResourceExhausted, "too many open links")
	}
	s.state.Tickets[hex.EncodeToString(req.TicketHash)] = &Ticket{
		Role: RoleDock, Hub: hub, LinkID: req.LinkId, Name: req.Name, Expires: req.ExpiresUnixMs,
	}
	if err := s.saveLocked(); err != nil {
		return nil, status.Error(codes.Internal, "could not save")
	}
	return &pb.InviteReply{}, nil
}

// Dock is a dock's control line.
func (s *Server) Dock(stream pb.Relay_DockServer) error {
	fp, m, err := s.need(stream.Context(), RoleDock)
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "expected hello")
	}
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	line := &dockLine{fp: fp, version: trunc(hello.Version, 64), since: time.Now(), opens: make(chan string, 64), cancel: cancel}

	s.mu.Lock()
	if mm := s.state.Members[fp]; mm == nil || mm.Role != RoleDock {
		s.mu.Unlock()
		return status.Error(codes.PermissionDenied, "this key is no longer enrolled")
	}
	if old := s.online[fp]; old != nil {
		old.cancel() // a newer connection from the same key replaces the old one
	}
	s.online[fp] = line
	s.mu.Unlock()
	s.notify(m.Hub)
	log.Printf("dock %s (%s) online", keys.Short(fp), m.Name)

	defer func() {
		s.mu.Lock()
		if s.online[fp] == line {
			delete(s.online, fp)
			if mm := s.state.Members[fp]; mm != nil {
				mm.LastSeen = time.Now().UnixMilli()
				_ = s.saveLocked()
			}
		}
		s.mu.Unlock()
		s.notify(m.Hub)
		log.Printf("dock %s (%s) offline", keys.Short(fp), m.Name)
	}()

	go func() { // drain anything else the dock sends; the stream ends when it closes
		for {
			if _, err := stream.Recv(); err != nil {
				cancel()
				return
			}
		}
	}()
	out, sendErr := sender(stream.Send)
	defer close(out)
	for {
		select {
		case <-ctx.Done():
			return status.Error(codes.Canceled, "cut")
		case err := <-sendErr:
			return err
		case id := <-line.opens:
			select {
			case out <- &pb.DockDown{Msg: &pb.DockDown_Open{Open: &pb.OpenChannel{ChannelId: id}}}:
			case <-ctx.Done():
				return status.Error(codes.Canceled, "cut")
			case err := <-sendErr:
				return err
			}
		}
	}
}

// sender sends on its own goroutine, so a handler can return (which ends the
// RPC) on revocation even while a send is stalled by flow control.
func sender[T any](send func(T) error) (chan<- T, <-chan error) {
	out := make(chan T, 1)
	errc := make(chan error, 1)
	go func() {
		for m := range out {
			if err := send(m); err != nil {
				errc <- err
				for range out { // drain until the handler closes it
				}
				return
			}
		}
	}()
	return out, errc
}

// Open is the hub's half of a channel.
func (s *Server) Open(stream pb.Relay_OpenServer) error {
	hub, _, err := s.need(stream.Context(), RoleHub)
	if err != nil {
		return err
	}
	md, _ := metadata.FromIncomingContext(stream.Context())
	dockIDs := md.Get("ember-dock")
	if len(dockIDs) != 1 {
		return status.Error(codes.InvalidArgument, "missing ember-dock")
	}
	dock := dockIDs[0]

	s.mu.Lock()
	m := s.state.Members[dock]
	line := s.online[dock]
	if m == nil || m.Role != RoleDock || m.Hub != hub {
		s.mu.Unlock()
		return status.Error(codes.NotFound, "no such dock for this hub")
	}
	if line == nil {
		s.mu.Unlock()
		return status.Error(codes.Unavailable, "dock is offline")
	}
	if s.hubOpen[hub] >= maxChannelsPerHub || s.dockOpen[dock] >= maxChannelsPerDock {
		s.mu.Unlock()
		return status.Error(codes.ResourceExhausted, "too many open channels")
	}
	// The reservation covers the channel from now until this handler returns:
	// pending, accepted and active alike.
	s.hubOpen[hub]++
	s.dockOpen[dock]++
	defer func() {
		s.mu.Lock()
		if s.hubOpen[hub]--; s.hubOpen[hub] <= 0 {
			delete(s.hubOpen, hub)
		}
		if s.dockOpen[dock]--; s.dockOpen[dock] <= 0 {
			delete(s.dockOpen, dock)
		}
		s.mu.Unlock()
	}()
	id := newID()
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	p := &pending{hub: hub, dock: dock, accept: make(chan pb.Relay_AcceptServer, 1), done: make(chan struct{}), cancel: cancel}
	s.pending[id] = p
	s.mu.Unlock()

	defer close(p.done)
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	select {
	case line.opens <- id:
	default:
		return status.Error(codes.ResourceExhausted, "dock is busy")
	}

	var acc pb.Relay_AcceptServer
	select {
	case acc = <-p.accept:
	case <-time.After(acceptTimeout):
		return status.Error(codes.DeadlineExceeded, "dock did not accept the channel")
	case <-ctx.Done():
		return status.Error(codes.Canceled, "channel cut")
	}

	go func() {
		select {
		case <-acc.Context().Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	// Promote to an active channel only if both keys are still members and the
	// dock still belongs to this hub; Revoke holds the same lock.
	s.mu.Lock()
	dm, hm := s.state.Members[dock], s.state.Members[hub]
	if ctx.Err() != nil || dm == nil || dm.Hub != hub || hm == nil || hm.Role != RoleHub {
		s.mu.Unlock()
		return status.Error(codes.PermissionDenied, "channel cut")
	}
	s.channels[id] = &channelRef{hub: hub, dock: dock, cancel: cancel}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.channels, id)
		s.mu.Unlock()
	}()
	return splice(ctx, stream, acc)
}

// Accept is the dock's half of a channel.
func (s *Server) Accept(stream pb.Relay_AcceptServer) error {
	dock, _, err := s.need(stream.Context(), RoleDock)
	if err != nil {
		return err
	}
	md, _ := metadata.FromIncomingContext(stream.Context())
	ids := md.Get("ember-channel")
	if len(ids) != 1 {
		return status.Error(codes.InvalidArgument, "missing ember-channel")
	}
	s.mu.Lock()
	p := s.pending[ids[0]]
	if p == nil || p.dock != dock {
		s.mu.Unlock()
		return status.Error(codes.NotFound, "no such channel")
	}
	delete(s.pending, ids[0])
	s.mu.Unlock()
	select {
	case p.accept <- stream:
	default:
		return status.Error(codes.AlreadyExists, "channel already accepted")
	}
	select {
	case <-p.done:
	case <-stream.Context().Done():
	}
	return nil
}

type chunkStream interface {
	Send(*pb.Chunk) error
	Recv() (*pb.Chunk, error)
}

// splice copies chunks both ways until both directions end or either side fails.
func splice(ctx context.Context, hub chunkStream, dock chunkStream) error {
	errc := make(chan error, 2)
	pipe := func(from, to chunkStream) {
		for {
			c, err := from.Recv()
			if err == io.EOF {
				errc <- to.Send(&pb.Chunk{Eof: true})
				return
			}
			if err != nil {
				errc <- err
				return
			}
			if err := to.Send(c); err != nil {
				errc <- err
				return
			}
			if c.Eof {
				errc <- nil
				return
			}
		}
	}
	go pipe(hub, dock)
	go pipe(dock, hub)
	for i := 0; i < 2; i++ {
		select {
		case err := <-errc:
			if err != nil {
				return status.Error(codes.Aborted, "channel closed")
			}
		case <-ctx.Done():
			return status.Error(codes.Canceled, "channel cut")
		}
	}
	return nil
}

// Directory streams the calling hub's docks.
func (s *Server) Directory(_ *pb.DirectoryRequest, stream pb.Relay_DirectoryServer) error {
	hub, _, err := s.need(stream.Context(), RoleHub)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	wake := make(chan struct{}, 1)
	wake <- struct{}{} // seeded before anyone else can signal it
	s.mu.Lock()
	if m := s.state.Members[hub]; m == nil || m.Role != RoleHub {
		s.mu.Unlock()
		return status.Error(codes.PermissionDenied, "this key is no longer enrolled")
	}
	if s.watchers[hub] == nil {
		s.watchers[hub] = map[chan struct{}]context.CancelFunc{}
	}
	s.watchers[hub][wake] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.watchers[hub], wake)
		s.mu.Unlock()
	}()
	out, sendErr := sender(stream.Send)
	defer close(out)
	for {
		select {
		case <-ctx.Done():
			return status.Error(codes.Canceled, "cut")
		case err := <-sendErr:
			return err
		case <-wake:
			select {
			case out <- s.snapshot(hub):
			case <-ctx.Done():
				return status.Error(codes.Canceled, "cut")
			case err := <-sendErr:
				return err
			}
		}
	}
}

func (s *Server) snapshot(hub string) *pb.DirectorySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := &pb.DirectorySnapshot{}
	for fp, m := range s.state.Members {
		if m.Role != RoleDock || m.Hub != hub {
			continue
		}
		e := &pb.DockEntry{Id: fp, LinkId: m.LinkID, Name: m.Name, SinceUnixMs: m.Enrolled, LastSeenUnixMs: m.LastSeen}
		if line := s.online[fp]; line != nil {
			e.Online = true
			e.SinceUnixMs = line.since.UnixMilli()
			e.LastSeenUnixMs = time.Now().UnixMilli()
			e.Version = line.version
		}
		out.Docks = append(out.Docks, e)
	}
	sort.Slice(out.Docks, func(i, j int) bool { return out.Docks[i].Id < out.Docks[j].Id })
	return out
}

func (s *Server) notify(hub string) {
	if hub == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for w := range s.watchers[hub] {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

// Revoke forgets one of the calling hub's docks and cuts its channels.
func (s *Server) Revoke(ctx context.Context, req *pb.RevokeRequest) (*pb.RevokeReply, error) {
	hub, _, err := s.need(ctx, RoleHub)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	m := s.state.Members[req.DockId]
	if m == nil || m.Role != RoleDock || m.Hub != hub {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "no such dock")
	}
	delete(s.state.Members, req.DockId)
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return nil, status.Error(codes.Internal, "could not save")
	}
	cut := s.cutLocked(req.DockId)
	s.mu.Unlock()
	for _, c := range cut {
		c()
	}
	s.notify(hub)
	log.Printf("revoked dock %s", keys.Short(req.DockId))
	return &pb.RevokeReply{}, nil
}

// cutLocked collects cancel funcs for a member's control line and channels.
func (s *Server) cutLocked(fp string) []context.CancelFunc {
	var out []context.CancelFunc
	if line := s.online[fp]; line != nil {
		out = append(out, line.cancel)
	}
	for _, c := range s.channels {
		if c.dock == fp || c.hub == fp {
			out = append(out, c.cancel)
		}
	}
	for id, p := range s.pending {
		if p.dock == fp || p.hub == fp {
			out = append(out, p.cancel)
			delete(s.pending, id)
		}
	}
	for _, c := range s.watchers[fp] {
		out = append(out, c)
	}
	return out
}

// TLSConfig is the relay's server TLS: its own certificate, any client certificate
// (authorization happens per call, by fingerprint).
func (s *Server) TLSConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{s.id.Cert},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS13,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509Cert) error {
			return checkSelfSigned(raw)
		},
	}
}

// Serve runs the gRPC server on ln until ctx ends.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	gs := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(s.TLSConfig())),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 5 * time.Second, PermitWithoutStream: true}),
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: 15 * time.Second, Timeout: 10 * time.Second}),
		grpc.MaxConcurrentStreams(4096),
	)
	pb.RegisterRelayServer(gs, s)
	go func() {
		<-ctx.Done()
		gs.Stop()
	}()
	return gs.Serve(ln)
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
