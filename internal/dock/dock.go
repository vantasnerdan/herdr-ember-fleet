// Package dock runs on every computer (once per OS user) that runs herdr. It
// dials the relay, keeps the connection up, and serves the hub's channels:
// snapshots, terminals, allow-listed actions and the herdr client bridge. It
// enforces this machine's permissions and keeps an audit log.
package dock

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/e2e"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/herdr"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/keys"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/pair"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/relay"
	pb "github.com/vantasnerdan/herdr-ember-fleet/internal/relay/relaypb"
)

// Version is set by main.
var Version = "dev"

// Dock is a running dock.
type Dock struct {
	cfg      *Config
	id       *keys.Identity
	sessions *Sessions
	audit    *audit
	tls      *tls.Config

	mu sync.Mutex
}

// New loads the dock's config and key.
func New() (*Dock, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	id, err := keys.Load(Dir())
	if err != nil {
		return nil, err
	}
	d := &Dock{cfg: cfg, id: id, sessions: NewSessions(cfg.KeepRunning), audit: openAudit(filepath.Join(Dir(), "audit.jsonl"))}
	d.tls = e2e.DockTLS(id, func(fp string) bool { _, ok := d.grant(fp); return ok })
	return d, nil
}

func (d *Dock) grant(fp string) (HubGrant, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, h := range d.cfg.Hubs {
		if keys.Equal(h.FP, fp) {
			return h, true
		}
	}
	return HubGrant{}, false
}

// Run serves until ctx ends, reconnecting to the relay as needed.
func (d *Dock) Run(ctx context.Context) error {
	go d.sessions.Run(ctx)
	go cleanUploads(ctx)
	log.Printf("dock %s for %s, relay %s", keys.Short(d.id.Fingerprint), d.cfg.Name, d.cfg.Relay)
	var b relay.Backoff
	for ctx.Err() == nil {
		err := d.connect(ctx, &b)
		if ctx.Err() != nil {
			return nil
		}
		log.Printf("relay: %v; reconnecting", err)
		b.Wait(ctx)
	}
	return nil
}

func (d *Dock) connect(ctx context.Context, b *relay.Backoff) error {
	conn, client, err := relay.Dial(d.cfg.Relay, d.cfg.Pin, d.id)
	if err != nil {
		return err
	}
	defer conn.Close()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.Dock(sctx)
	if err != nil {
		return err
	}
	if err := stream.Send(&pb.DockUp{Msg: &pb.DockUp_Hello{Hello: &pb.Hello{Version: Version}}}); err != nil {
		return err
	}
	b.Healthy() // backoff resets only if this connection then lasts a minute
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		if open := msg.GetOpen(); open != nil {
			go d.channel(ctx, client, open.ChannelId)
		}
	}
}

func (d *Dock) channel(ctx context.Context, client pb.RelayClient, id string) {
	sc, err := relay.AcceptChannel(ctx, client, id)
	if err != nil {
		log.Printf("accept: %v", err)
		return
	}
	defer sc.Close()
	t, hubFP, err := e2e.Server(ctx, sc, d.tls)
	if err != nil {
		log.Printf("channel handshake refused: %v", err)
		return
	}
	defer t.Close()
	r := bufio.NewReaderSize(t, 64<<10)
	var h e2e.Header
	if err := e2e.ReadJSON(r, &h); err != nil {
		return
	}
	g, ok := d.grant(hubFP)
	if !ok {
		return
	}
	if h.V != e2e.Version {
		e2e.WriteJSON(t, e2e.Reply{Code: "version", Error: fmt.Sprintf("this dock speaks channel version %d", e2e.Version)})
		return
	}
	ch := &chanCtx{d: d, conn: t, r: r, hub: hubFP, grant: g, h: h}
	switch h.Kind {
	case e2e.KindPair:
		d.pair(ch)
	case e2e.KindWatch:
		if ch.need(PermInventory) {
			d.watch(ctx, ch)
		}
	case e2e.KindObserve:
		if ch.need(PermObserve) {
			d.terminal(ctx, ch, false)
		}
	case e2e.KindControl:
		perm := PermControl
		if h.Takeover {
			perm = PermTakeover
		}
		if ch.need(PermControl) && ch.need(perm) {
			d.terminal(ctx, ch, true)
		}
	case e2e.KindAction:
		d.action(ctx, ch)
	case e2e.KindEndpoint:
		if ch.need(PermAttach) {
			d.endpoint(ctx, ch)
		}
	case e2e.KindUpload:
		if ch.need(PermControl) { // putting files on this machine is as strong as typing
			d.upload(ctx, ch)
		}
	case e2e.KindUpdate:
		if ch.need(PermUpdate) {
			d.selfUpdate(ctx, ch)
		}
	case e2e.KindHerdrUp:
		if ch.need(PermUpdate) {
			d.herdrUpdate(ctx, ch)
		}
	default:
		ch.fail("unknown_kind", "unknown channel kind")
	}
}

type chanCtx struct {
	d     *Dock
	conn  *tls.Conn
	r     *bufio.Reader
	hub   string
	grant HubGrant
	h     e2e.Header
}

func (c *chanCtx) need(perm string) bool {
	if Allowed(c.grant.Role, perm) {
		return true
	}
	c.d.audit.log(c, "denied", perm)
	c.fail("forbidden", "this machine does not allow "+perm+" for your role ("+c.grant.Role+")")
	return false
}

func (c *chanCtx) fail(code, msg string) {
	e2e.WriteJSON(c.conn, e2e.Reply{Code: code, Error: msg, Outcome: "not_sent"})
}

func (c *chanCtx) ok() error { return e2e.WriteJSON(c.conn, e2e.Reply{OK: true}) }

// pair proves this dock's key to the hub named in the join link.
func (d *Dock) pair(c *chanCtx) {
	if len(c.grant.PairKey) == 0 {
		// Already paired: say so, so a hub that lost its record can tell.
		c.fail("already_paired", "this dock is already paired")
		return
	}
	proof, err := pair.Proof(c.grant.PairKey, d.id.Fingerprint, c.hub, c.grant.LinkID)
	if err != nil {
		c.fail("internal", err.Error())
		return
	}
	if err := e2e.WriteJSON(c.conn, e2e.Reply{OK: true, LinkID: c.grant.LinkID, Proof: proof}); err != nil {
		return
	}
	var confirm e2e.Reply
	if err := e2e.ReadJSON(c.r, &confirm); err != nil || !confirm.OK {
		log.Printf("hub did not confirm pairing: %v %s", err, confirm.Error)
		return
	}
	d.mu.Lock()
	for i := range d.cfg.Hubs {
		if keys.Equal(d.cfg.Hubs[i].FP, c.hub) {
			d.cfg.Hubs[i].PairKey = nil
			d.cfg.Hubs[i].Paired = true
		}
	}
	d.mu.Unlock()
	if err := d.cfg.Save(); err != nil {
		log.Printf("save dock.json: %v", err)
	}
	d.audit.log(c, "paired", "")
	log.Printf("paired with hub %s", keys.Short(c.hub))
}

// Info describes this dock.
type Info struct {
	Type    string `json:"type"`
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

func (d *Dock) info(ctx context.Context, role string) Info {
	host, _ := os.Hostname()
	u, _ := user.Current()
	name := ""
	if u != nil {
		name = u.Username
	}
	home, _ := os.UserHomeDir()
	return Info{Type: "info", Name: d.cfg.Name, Host: host, User: name, Home: home, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Herdr: installedHerdr(ctx), Version: Version, Role: role}
}

// watch sends this dock's info, then each session's state whenever it changes.
func (d *Dock) watch(ctx context.Context, c *chanCtx) {
	if c.ok() != nil {
		return
	}
	info := d.info(ctx, c.grant.Role)
	if e2e.WriteJSON(c.conn, info) != nil {
		return
	}
	wake, stop := d.sessions.Subscribe()
	defer stop()
	closed := make(chan struct{})
	go func() { io.Copy(io.Discard, c.r); close(closed) }()
	sent := map[string]uint64{}
	synced := false
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-closed:
			return
		case <-ping.C:
			// herdr may have been updated (by the fleet or by hand): resend the
			// info when its version changes, so the page never shows a stale one.
			if now := d.info(ctx, c.grant.Role); now.Herdr != info.Herdr {
				info = now
				if e2e.WriteJSON(c.conn, info) != nil {
					return
				}
			}
			if e2e.WriteJSON(c.conn, map[string]string{"type": "ping"}) != nil {
				return
			}
		case <-wake:
			states := d.sessions.States()
			for name, st := range states {
				if v, ok := sent[name]; ok && v == st.ver {
					continue
				}
				sent[name] = st.ver
				msg := struct {
					Type string `json:"type"`
					SessionState
				}{"session", st}
				if e2e.WriteJSON(c.conn, msg) != nil {
					return
				}
			}
			for name := range sent {
				if _, ok := states[name]; !ok {
					delete(sent, name)
					if e2e.WriteJSON(c.conn, map[string]string{"type": "session_removed", "name": name}) != nil {
						return
					}
				}
			}
			if !synced {
				// Everything this dock has is now sent: the hub replaces its
				// cache with exactly this set, dropping sessions that vanished
				// while the connection was down.
				synced = true
				if e2e.WriteJSON(c.conn, map[string]string{"type": "synced"}) != nil {
					return
				}
			}
		}
	}
}

type audit struct {
	mu sync.Mutex
	f  *os.File
}

func openAudit(path string) *audit {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("audit log: %v", err)
	}
	return &audit{f: f}
}

// log records who did what. Never keystrokes, screens or prompt text.
func (a *audit) log(c *chanCtx, event, detail string) {
	if a.f == nil {
		return
	}
	rec := map[string]any{
		"t": time.Now().UTC().Format(time.RFC3339Nano), "event": event, "hub": keys.Short(c.hub),
		"operator": trunc(c.h.Operator, 80), "kind": c.h.Kind, "session": c.h.Session, "pane": c.h.Pane,
	}
	if c.h.Method != "" {
		rec["method"] = c.h.Method
	}
	if detail != "" {
		rec["detail"] = trunc(detail, 200)
	}
	b, _ := json.Marshal(rec)
	a.mu.Lock()
	a.f.Write(append(b, '\n'))
	a.mu.Unlock()
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

type cachedVersion struct {
	v  string
	at time.Time
}

var herdrVersionCache atomic.Pointer[cachedVersion]

// installedHerdr is the installed herdr's version, re-read at most every 15 s.
func installedHerdr(ctx context.Context) string {
	if c := herdrVersionCache.Load(); c != nil && time.Since(c.at) < 15*time.Second {
		return c.v
	}
	v := herdr.Version(ctx)
	herdrVersionCache.Store(&cachedVersion{v: v, at: time.Now()})
	return v
}
