// Package hub is the fleet's backend: it holds the keys and records, keeps a
// live model of every paired dock through the relay, and serves the web page
// and the terminal tools.
package hub

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/e2e"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/keys"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/pair"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/paths"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/relay"
	pb "github.com/vantasnerdan/herdr-ember-fleet/internal/relay/relaypb"
)

// Version is set by main.
var Version = "dev"

// Dir is the hub's directory.
func Dir() string { return paths.Role("hub") }

const (
	maxSessionsPerDock = 64
	maxSnapshotBytes   = 4 << 20
)

// Hub is a running hub.
type Hub struct {
	id    *keys.Identity
	store *Store

	mu      sync.Mutex
	client  pb.RelayClient
	conn    *grpc.ClientConn
	docks   map[string]*dockLive
	pairing map[string]time.Time

	live     sync.Mutex
	watchers map[*liveSub]struct{} // pages following the fleet

	signins map[string]time.Time // one-time sign-in token hashes

	upd updater

	credMu   sync.Mutex
	credCuts map[string]map[*context.CancelFunc]struct{} // credential -> its live requests
	revoked  map[string]bool                             // sessions signed out but not yet saved
}

type dockLive struct {
	id       string
	online   bool
	lastSeen int64
	version  string
	info     *dockInfo
	sessions map[string]json.RawMessage
	ctx      context.Context
	cancel   context.CancelFunc
	watching bool
}

type dockInfo struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	User    string `json:"user"`
	Home    string `json:"home"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Herdr   string `json:"herdr"`
	Version string `json:"version"`
	Role    string `json:"role"`
}

// Init creates the hub's key and enrolls it with a relay.
func Init(ctx context.Context, relayLink, fleet string) (*keys.Identity, error) {
	rl, err := pair.ParseRelay(relayLink)
	if err != nil {
		return nil, err
	}
	if err := paths.Ensure(Dir()); err != nil {
		return nil, err
	}
	if _, err := OpenStore(Dir()); err == nil {
		return nil, errors.New("this hub is already set up")
	}
	id, err := keys.LoadOrCreate(Dir(), "ember-fleet-hub")
	if err != nil {
		return nil, err
	}
	conn, client, err := relay.Dial(rl.Relay, rl.Pin, id)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	reply, err := client.Enroll(cctx, &pb.EnrollRequest{Ticket: rl.Ticket})
	if err != nil {
		return nil, fmt.Errorf("the relay refused this link: %w", err)
	}
	if reply.Role != relay.RoleHub {
		return nil, errors.New("this is not a hub link")
	}
	if fleet == "" {
		fleet = "Fleet"
	}
	if _, err := CreateStore(Dir(), State{Fleet: fleet, Relay: rl.Relay, Pin: rl.Pin}); err != nil {
		return nil, err
	}
	return id, nil
}

// New loads a set-up hub.
func New() (*Hub, error) {
	store, err := OpenStore(Dir())
	if err != nil {
		return nil, err
	}
	id, err := keys.Load(Dir())
	if err != nil {
		return nil, err
	}
	return &Hub{
		id:       id,
		store:    store,
		docks:    map[string]*dockLive{},
		pairing:  map[string]time.Time{},
		watchers: map[*liveSub]struct{}{},
		signins:  map[string]time.Time{},
		credCuts: map[string]map[*context.CancelFunc]struct{}{},
		revoked:  map[string]bool{},
	}, nil
}

// Fingerprint is the hub's key fingerprint.
func (h *Hub) Fingerprint() string { return h.id.Fingerprint }

// Run keeps the relay connection and directory up until ctx ends.
func (h *Hub) Run(ctx context.Context) {
	var b relay.Backoff
	for ctx.Err() == nil {
		err := h.follow(ctx, &b)
		if ctx.Err() != nil {
			return
		}
		log.Printf("relay: %v; reconnecting", err)
		h.mu.Lock()
		for _, d := range h.docks {
			if d.online {
				d.online = false
				d.lastSeen = time.Now().UnixMilli()
				d.cancel()
				d.ctx, d.cancel = context.WithCancel(context.Background())
			}
		}
		h.client = nil
		h.mu.Unlock()
		h.changedAll()
		b.Wait(ctx)
	}
}

func (h *Hub) relayAddr() (string, string) {
	var addr, pin string
	h.store.View(func(s *State) { addr, pin = s.Relay, s.Pin })
	return addr, pin
}

func (h *Hub) follow(ctx context.Context, b *relay.Backoff) error {
	addr, pin := h.relayAddr()
	conn, client, err := relay.Dial(addr, pin, h.id)
	if err != nil {
		return err
	}
	defer conn.Close()
	dir, err := client.Directory(ctx, &pb.DirectoryRequest{})
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.client, h.conn = client, conn
	h.mu.Unlock()
	b.Healthy()
	for {
		snap, err := dir.Recv()
		if err != nil {
			return err
		}
		h.directory(ctx, snap)
	}
}

// directory applies the relay's view of who is online. The relay is trusted
// only for presence; which keys are ours comes from hub.json.
func (h *Hub) directory(ctx context.Context, snap *pb.DirectorySnapshot) {
	var bound map[string]bool
	tomb := map[string]bool{}
	pendingLinks := map[string]bool{}
	// Lock order everywhere: h.mu, then the store. Reading the records under
	// h.mu means RemoveDock (which updates the store under h.mu) can't slip
	// in between this check and the dockLive changes below.
	h.mu.Lock()
	h.store.View(func(s *State) {
		bound = map[string]bool{}
		for id := range s.Docks {
			bound[id] = true
		}
		for id := range s.Tombstones {
			tomb[id] = true
		}
		now := time.Now().UnixMilli()
		for id, l := range s.Links {
			if l.State == "pending" && l.Expires > now {
				pendingLinks[id] = true
			}
		}
	})
	seen := map[string]bool{}
	var toWatch, toPair []string
	for _, e := range snap.Docks {
		seen[e.Id] = true
		if tomb[e.Id] {
			continue
		}
		if !bound[e.Id] {
			if e.Online && pendingLinks[e.LinkId] && time.Since(h.pairing[e.Id]) > 20*time.Second {
				h.pairing[e.Id] = time.Now()
				toPair = append(toPair, e.Id)
			}
			continue
		}
		d := h.liveLocked(e.Id)
		wasOnline := d.online
		d.online, d.lastSeen, d.version = e.Online, e.LastSeenUnixMs, e.Version
		if !e.Online && wasOnline {
			d.cancel()
			d.ctx, d.cancel = context.WithCancel(context.Background())
		}
		if e.Online && !d.watching {
			d.watching = true
			toWatch = append(toWatch, e.Id)
		}
	}
	for id, d := range h.docks {
		if !seen[id] && d.online {
			d.online = false
			d.cancel()
			d.ctx, d.cancel = context.WithCancel(context.Background())
		}
	}
	h.mu.Unlock()
	for _, id := range toPair {
		go h.pair(ctx, id)
	}
	for _, id := range toWatch {
		go h.watch(id)
	}
	h.changedAll()
}

func (h *Hub) liveLocked(id string) *dockLive {
	d := h.docks[id]
	if d == nil {
		ctx, cancel := context.WithCancel(context.Background())
		d = &dockLive{id: id, sessions: map[string]json.RawMessage{}, ctx: ctx, cancel: cancel}
		h.docks[id] = d
	}
	return d
}

func (h *Hub) isBound(id string) bool {
	ok := false
	h.store.View(func(s *State) { _, ok = s.Docks[id]; _, dead := s.Tombstones[id]; ok = ok && !dead })
	return ok
}

// open makes an end-to-end channel to a paired dock and sends the header.
// allow decides which dock key the TLS layer accepts.
func (h *Hub) open(ctx context.Context, dock string, hdr e2e.Header, allow func(string) bool) (*tls.Conn, *bufio.Reader, e2e.Reply, error) {
	h.mu.Lock()
	client := h.client
	h.mu.Unlock()
	if client == nil {
		return nil, nil, e2e.Reply{}, errors.New("not connected to the relay")
	}
	sc, err := relay.OpenChannel(ctx, client, dock)
	if err != nil {
		return nil, nil, e2e.Reply{}, err
	}
	t, fp, err := e2e.Client(ctx, sc, e2e.HubTLS(h.id, allow))
	if err != nil {
		sc.Close()
		return nil, nil, e2e.Reply{}, fmt.Errorf("could not reach the machine securely: %w", err)
	}
	if !keys.Equal(fp, dock) {
		t.Close()
		return nil, nil, e2e.Reply{}, errors.New("the machine answered with the wrong key")
	}
	hdr.V = e2e.Version
	if err := e2e.WriteJSON(t, hdr); err != nil {
		// Once the header (which carries an action) starts going out, a
		// failure can't prove the dock didn't get it: outcome unknown.
		t.Close()
		return nil, nil, e2e.Reply{}, errSentNoReply
	}
	r := bufio.NewReaderSize(t, 256<<10)
	var rep e2e.Reply
	if err := e2e.ReadJSON(r, &rep); err != nil {
		t.Close()
		return nil, nil, e2e.Reply{}, errSentNoReply
	}
	return t, r, rep, nil
}

var errSentNoReply = errors.New("sent, but no answer arrived")

// openPaired opens a channel to a dock the hub has paired with, tied to that
// dock's lifetime (removing it cuts the channel).
func (h *Hub) openPaired(ctx context.Context, dock string, hdr e2e.Header) (*tls.Conn, *bufio.Reader, e2e.Reply, context.CancelFunc, error) {
	h.mu.Lock()
	bound := h.isBound(dock)
	d := h.docks[dock]
	var dctx context.Context
	if d != nil && bound {
		dctx = d.ctx
	}
	h.mu.Unlock()
	if !bound {
		return nil, nil, e2e.Reply{}, nil, errors.New("no such machine")
	}
	if d == nil || dctx == nil {
		return nil, nil, e2e.Reply{}, nil, errors.New("that machine is offline")
	}
	cctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-dctx.Done():
			cancel()
		case <-cctx.Done():
		}
	}()
	t, r, rep, err := h.open(cctx, dock, hdr, func(fp string) bool { return keys.Equal(fp, dock) })
	if err != nil {
		cancel()
		return nil, nil, rep, nil, err
	}
	go func() { <-cctx.Done(); t.Close() }()
	return t, r, rep, cancel, nil
}

// pair checks a new dock's proof against the hub's own link record and binds
// the link to that exact key, in one locked, durable step.
func (h *Hub) pair(ctx context.Context, dock string) {
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var peer string
	t, _, rep, err := h.open(pctx, dock, e2e.Header{Kind: e2e.KindPair}, func(fp string) bool { peer = fp; return true })
	if err != nil {
		log.Printf("pairing %s: %v", keys.Short(dock), err)
		return
	}
	defer t.Close()
	if !rep.OK {
		log.Printf("pairing %s: dock says %s", keys.Short(dock), rep.Error)
		return
	}
	var name string
	err = h.store.Update(func(s *State) error {
		if _, dead := s.Tombstones[peer]; dead {
			return errors.New("that machine was removed")
		}
		if _, dup := s.Docks[peer]; dup {
			return errors.New("already paired")
		}
		l := s.Links[rep.LinkID]
		switch {
		case l == nil:
			return errors.New("unknown link")
		case l.State != "pending":
			return fmt.Errorf("link is %s", l.State)
		case l.Expires < time.Now().UnixMilli():
			return errors.New("link expired")
		case len(l.PairKey) == 0:
			return errors.New("link has no key")
		}
		if !pair.CheckProof(l.PairKey, peer, h.id.Fingerprint, l.ID, rep.Proof) {
			return errors.New("pairing proof is wrong (a relay may have swapped keys)")
		}
		l.State, l.Dock, l.PairKey = "used", peer, nil
		s.Docks[peer] = &DockRec{ID: peer, Name: l.Name, LinkID: l.ID, Paired: time.Now().UnixMilli()}
		name = l.Name
		return nil
	})
	if err != nil {
		log.Printf("pairing %s refused: %v", keys.Short(dock), err)
		e2e.WriteJSON(t, e2e.Reply{Error: err.Error()})
		return
	}
	e2e.WriteJSON(t, e2e.Reply{OK: true})
	log.Printf("paired %s as %q", keys.Short(peer), name)
	h.mu.Lock()
	start := false
	if h.isBound(peer) { // not removed in the meantime
		d := h.liveLocked(peer)
		d.online = true
		start = !d.watching
		d.watching = true
	}
	h.mu.Unlock()
	if start {
		go h.watch(peer)
	}
	h.changedAll()
}

// watch keeps a watch channel open while the dock is online.
func (h *Hub) watch(dock string) {
	var b relay.Backoff
	for {
		// Decide to stop and clear the flag under one lock, so a dock that
		// comes back online at that moment still gets a watcher.
		h.mu.Lock()
		d := h.docks[dock]
		if d == nil || !d.online || !h.isBound(dock) {
			if d != nil {
				d.watching = false
			}
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()
		err := h.watchOnce(dock, &b)
		if err != nil {
			log.Printf("watch %s: %v", keys.Short(dock), err)
		}
		b.Wait(context.Background())
	}
}

func (h *Hub) watchOnce(dock string, b *relay.Backoff) error {
	t, r, rep, cancel, err := h.openPaired(context.Background(), dock, e2e.Header{Kind: e2e.KindWatch, Operator: "hub"})
	if err != nil {
		return err
	}
	defer cancel()
	defer t.Close()
	if !rep.OK {
		return errors.New(rep.Error)
	}
	b.Healthy()
	// Until "synced", sessions go into a fresh set that then replaces the
	// cache whole; after that, updates apply directly.
	fresh := map[string]json.RawMessage{}
	syncing := true
	for {
		line, err := e2e.ReadLine(r)
		if err != nil {
			return err
		}
		var m struct {
			Type     string          `json:"type"`
			Name     string          `json:"name"`
			Snapshot json.RawMessage `json:"snapshot"`
		}
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		switch m.Type {
		case "info":
			var info dockInfo
			if json.Unmarshal(line, &info) == nil {
				info.clip()
				h.mu.Lock()
				if d := h.docks[dock]; d != nil {
					d.info = &info
				}
				h.mu.Unlock()
				h.changed(dock)
			}
		case "session":
			if len(m.Snapshot) > maxSnapshotBytes || len(m.Name) > 64 {
				continue
			}
			if syncing {
				if _, ok := fresh[m.Name]; ok || len(fresh) < maxSessionsPerDock {
					fresh[m.Name] = append(json.RawMessage(nil), line...)
				}
				continue
			}
			h.mu.Lock()
			if d := h.docks[dock]; d != nil {
				if _, ok := d.sessions[m.Name]; ok || len(d.sessions) < maxSessionsPerDock {
					d.sessions[m.Name] = append(json.RawMessage(nil), line...)
				}
			}
			h.mu.Unlock()
			h.changed(dock)
		case "session_removed":
			if syncing {
				delete(fresh, m.Name)
				continue
			}
			h.mu.Lock()
			if d := h.docks[dock]; d != nil {
				delete(d.sessions, m.Name)
			}
			h.mu.Unlock()
			h.changed(dock)
		case "synced":
			if syncing {
				syncing = false
				h.mu.Lock()
				if d := h.docks[dock]; d != nil {
					d.sessions = fresh
				}
				h.mu.Unlock()
				h.changed(dock)
			}
		}
	}
}

func (i *dockInfo) clip() {
	c := func(s *string, n int) {
		if len(*s) > n {
			*s = (*s)[:n]
		}
	}
	c(&i.Name, 64)
	c(&i.Host, 128)
	c(&i.User, 64)
	c(&i.Home, 256)
	c(&i.OS, 16)
	c(&i.Arch, 16)
	c(&i.Herdr, 32)
	c(&i.Version, 32)
	c(&i.Role, 16)
}

// --- the model the page sees -------------------------------------------------

// Machine groups docks under the hub-owned machine name.
type Machine struct {
	Host  string     `json:"host"`
	Docks []DockView `json:"docks"`
}

// DockView is one OS user's herdr on a machine.
type DockView struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Hostname string            `json:"hostname"` // as the dock reports it (a label only)
	User     string            `json:"user"`
	Home     string            `json:"home"`
	OS       string            `json:"os"`
	Arch     string            `json:"arch"`
	Herdr    string            `json:"herdr"`       // installed herdr
	HerdrRun string            `json:"herdrServer"` // the default session's running herdr server
	Version  string            `json:"version"`
	Online   bool              `json:"online"`
	LastSeen int64             `json:"lastSeen"`
	Role     string            `json:"role"`
	Sessions []json.RawMessage `json:"sessions"`
}

// Machines builds the page's view of the fleet.
func (h *Hub) Machines() []Machine {
	var recs []DockRec
	h.store.View(func(s *State) {
		for _, d := range s.Docks {
			if _, dead := s.Tombstones[d.ID]; !dead {
				recs = append(recs, *d)
			}
		}
	})
	by := map[string]*Machine{}
	h.mu.Lock()
	for _, rec := range recs {
		v := DockView{ID: rec.ID, Name: rec.Name, Sessions: []json.RawMessage{}}
		if d := h.docks[rec.ID]; d != nil {
			v.Online, v.LastSeen = d.online, d.lastSeen
			if d.online {
				v.LastSeen = time.Now().UnixMilli()
			}
			if d.info != nil {
				v.Hostname, v.User, v.Home, v.OS, v.Arch, v.Herdr, v.Version, v.Role =
					d.info.Host, d.info.User, d.info.Home, d.info.OS, d.info.Arch, d.info.Herdr, d.info.Version, d.info.Role
			}
			names := make([]string, 0, len(d.sessions))
			for n := range d.sessions {
				names = append(names, n)
			}
			sort.Slice(names, func(i, j int) bool {
				if names[i] == "default" || names[j] == "default" {
					return names[i] == "default"
				}
				return names[i] < names[j]
			})
			for _, n := range names {
				v.Sessions = append(v.Sessions, sessionView(d.sessions[n], d.online))
			}
			if raw, ok := d.sessions["default"]; ok {
				var s struct {
					Snapshot struct {
						Version string `json:"version"`
					} `json:"snapshot"`
				}
				if json.Unmarshal(raw, &s) == nil {
					v.HerdrRun = s.Snapshot.Version
				}
			}
		}
		m := by[rec.Name]
		if m == nil {
			m = &Machine{Host: rec.Name}
			by[rec.Name] = m
		}
		m.Docks = append(m.Docks, v)
	}
	h.mu.Unlock()
	out := make([]Machine, 0, len(by))
	for _, m := range by {
		sort.Slice(m.Docks, func(i, j int) bool { return m.Docks[i].User < m.Docks[j].User })
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// sessionView passes the dock's session line through, minus the "type" key,
// and marks it offline when the dock is.
func sessionView(line json.RawMessage, online bool) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(line, &m) != nil {
		return json.RawMessage(`{}`)
	}
	delete(m, "type")
	if !online {
		m["online"] = json.RawMessage("false")
	}
	b, _ := json.Marshal(m)
	return b
}

// MachineOf returns the machine name a dock belongs to.
func (h *Hub) machineOf(dock string) string {
	name := ""
	h.store.View(func(s *State) {
		if d := s.Docks[dock]; d != nil {
			name = d.Name
		}
	})
	return name
}

// liveSub is one page's live feed: a set of changed machines (or "all")
// and a one-slot wakeup. Nothing is ever dropped: repeated changes coalesce.
type liveSub struct {
	mu      sync.Mutex
	dirty   map[string]bool
	all     bool
	groups  bool
	fleet   bool
	updates bool
	wake    chan struct{}
}

func (s *liveSub) markUpdates() {
	s.mu.Lock()
	s.updates = true
	s.mu.Unlock()
	s.poke()
}

func (s *liveSub) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *liveSub) markGroups() {
	s.mu.Lock()
	s.groups = true
	s.mu.Unlock()
	s.poke()
}

func (s *liveSub) markFleet() {
	s.mu.Lock()
	s.fleet = true
	s.mu.Unlock()
	s.poke()
}

func (s *liveSub) mark(name string) {
	s.mu.Lock()
	if name == "" {
		s.all = true
	} else {
		s.dirty[name] = true
	}
	s.mu.Unlock()
	s.poke()
}

type liveChanges struct {
	dirty                       map[string]bool
	all, groups, fleet, updates bool
}

// take returns and clears what changed.
func (s *liveSub) take() liveChanges {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := liveChanges{dirty: s.dirty, all: s.all, groups: s.groups, fleet: s.fleet, updates: s.updates}
	s.dirty, s.all, s.groups, s.fleet, s.updates = map[string]bool{}, false, false, false, false
	return c
}

func (h *Hub) changed(dock string) {
	name := h.machineOf(dock)
	if name == "" {
		return
	}
	h.live.Lock()
	defer h.live.Unlock()
	for w := range h.watchers {
		w.mark(name)
	}
}

func (h *Hub) changedAll() {
	h.live.Lock()
	defer h.live.Unlock()
	for w := range h.watchers {
		w.mark("")
	}
}

func (h *Hub) subscribe() (*liveSub, func()) {
	sub := &liveSub{dirty: map[string]bool{}, wake: make(chan struct{}, 1)}
	h.live.Lock()
	h.watchers[sub] = struct{}{}
	h.live.Unlock()
	return sub, func() {
		h.live.Lock()
		delete(h.watchers, sub)
		h.live.Unlock()
	}
}

// RemoveDock forgets a dock here first (tombstone, cut its channels), then asks
// the relay to revoke it. A relay that ignores the request gains nothing.
func (h *Hub) RemoveDock(ctx context.Context, id string) error {
	var name string
	h.mu.Lock()
	err := h.store.Update(func(s *State) error {
		d := s.Docks[id]
		if d == nil {
			return errors.New("no such machine")
		}
		name = d.Name
		delete(s.Docks, id)
		s.Tombstones[id] = time.Now().UnixMilli()
		dropDockMembersLocked(s, id)
		return nil
	})
	if err != nil {
		h.mu.Unlock()
		return err
	}
	if d := h.docks[id]; d != nil {
		d.cancel() // cuts its watch and every terminal, action and attach channel
		delete(h.docks, id)
	}
	client := h.client
	h.mu.Unlock()
	if client != nil {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, err := client.Revoke(cctx, &pb.RevokeRequest{DockId: id}); err != nil {
			log.Printf("relay revoke %s: %v (removed here anyway)", keys.Short(id), err)
		}
	}
	log.Printf("removed %s (%s)", keys.Short(id), name)
	h.changedAll()
	h.groupsChanged()
	return nil
}

// CreateLink makes a join link and registers its ticket with the relay.
func (h *Hub) CreateLink(ctx context.Context, name string) (string, *Link, error) {
	if err := pair.CheckName(name); err != nil {
		return "", nil, err
	}
	h.mu.Lock()
	client := h.client
	h.mu.Unlock()
	if client == nil {
		return "", nil, errors.New("not connected to the relay yet; try again in a moment")
	}
	secret := pair.NewSecret()
	ticket := pair.Ticket(secret)
	l := &Link{
		ID:      pair.LinkID(secret),
		Name:    name,
		PairKey: pair.PairKey(secret),
		Created: time.Now().UnixMilli(),
		Expires: time.Now().Add(24 * time.Hour).UnixMilli(),
		State:   "pending",
	}
	if err := h.store.Update(func(s *State) error { s.Links[l.ID] = l; return nil }); err != nil {
		return "", nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := client.Invite(cctx, &pb.InviteRequest{TicketHash: pair.TicketHash(ticket), LinkId: l.ID, Name: name, ExpiresUnixMs: l.Expires}); err != nil {
		h.store.Update(func(s *State) error { s.Links[l.ID].State = "cancelled"; return nil })
		return "", nil, fmt.Errorf("the relay did not accept the link: %w", err)
	}
	addr, pin := h.relayAddr()
	link := pair.Join{Relay: addr, Pin: pin, HubFP: h.id.Fingerprint, Secret: secret, Name: name}.String()
	cp := *l
	cp.PairKey = nil
	return link, &cp, nil
}

// CancelLink marks a pending link cancelled; the hub refuses it from then on.
func (h *Hub) CancelLink(id string) error {
	return h.store.Update(func(s *State) error {
		l := s.Links[id]
		if l == nil {
			return errors.New("no such link")
		}
		if l.State == "pending" {
			l.State, l.PairKey = "cancelled", nil
		}
		return nil
	})
}

// LinkView is a link as the page sees it.
type LinkView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Created int64  `json:"created"`
	Expires int64  `json:"expires"`
	State   string `json:"state"`
	Dock    string `json:"dock,omitempty"`
}

// Links lists links, newest first.
func (h *Hub) Links() []LinkView {
	var out []LinkView
	now := time.Now().UnixMilli()
	h.store.View(func(s *State) {
		for _, l := range s.Links {
			st := l.State
			if st == "pending" && l.Expires < now {
				st = "expired"
			}
			out = append(out, LinkView{ID: l.ID, Name: l.Name, Created: l.Created, Expires: l.Expires, State: st, Dock: l.Dock})
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out
}
