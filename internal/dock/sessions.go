package dock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/herdr"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/relay"
)

const maxSnapshot = 4 << 20

// SessionState is what the hub sees of one herdr session.
type SessionState struct {
	Name     string          `json:"name"`
	Epoch    string          `json:"epoch,omitempty"`
	Online   bool            `json:"online"`
	Error    string          `json:"error,omitempty"`
	Snapshot json.RawMessage `json:"snapshot,omitempty"`

	ver   uint64
	peer  herdr.Peer
	index map[string]paneInfo
}

type paneInfo struct {
	Terminal string
	Cols     int
	Rows     int
}

// Sessions follows every herdr session of this OS user.
type Sessions struct {
	keepRunning bool

	mu       sync.Mutex
	states   map[string]*SessionState
	monitors map[string]context.CancelFunc
	pokes    map[string]chan struct{}
	waiters  map[chan struct{}]struct{}
	ver      uint64
	started  time.Time
}

// NewSessions makes a follower; Run starts it.
func NewSessions(keepRunning bool) *Sessions {
	return &Sessions{
		keepRunning: keepRunning,
		states:      map[string]*SessionState{},
		monitors:    map[string]context.CancelFunc{},
		pokes:       map[string]chan struct{}{},
		waiters:     map[chan struct{}]struct{}{},
	}
}

// Run discovers sessions every few seconds and follows each one.
func (s *Sessions) Run(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		names := herdr.Sessions()
		if s.keepRunning && !contains(names, "default") {
			names = append([]string{"default"}, names...)
		}
		s.mu.Lock()
		for _, n := range names {
			if _, ok := s.monitors[n]; !ok {
				mctx, cancel := context.WithCancel(ctx)
				s.monitors[n] = cancel
				s.pokes[n] = make(chan struct{}, 1)
				s.states[n] = &SessionState{Name: n}
				go s.monitor(mctx, n, s.pokes[n])
			}
		}
		for n, cancel := range s.monitors {
			if !contains(names, n) {
				cancel()
				delete(s.monitors, n)
				delete(s.pokes, n)
				delete(s.states, n)
				s.bumpLocked()
			}
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// Poke asks a session to re-read soon (after an action).
func (s *Sessions) Poke(name string) {
	s.mu.Lock()
	p := s.pokes[name]
	s.mu.Unlock()
	if p != nil {
		select {
		case p <- struct{}{}:
		default:
		}
	}
}

// Subscribe returns a channel that is signalled when anything changes.
func (s *Sessions) Subscribe() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	ch <- struct{}{} // seeded before a producer can see it
	s.mu.Lock()
	s.waiters[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.waiters, ch)
		s.mu.Unlock()
	}
}

func (s *Sessions) bumpLocked() {
	s.ver++
	for w := range s.waiters {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

// States returns a copy of every session's state with its version.
func (s *Sessions) States() map[string]SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]SessionState{}
	for n, st := range s.states {
		out[n] = *st
	}
	return out
}

// Current returns a session's state, if followed.
func (s *Sessions) Current(name string) (SessionState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.states[name]
	if !ok {
		return SessionState{}, false
	}
	return *st, true
}

func (s *Sessions) set(name string, f func(st *SessionState) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[name]
	if st == nil {
		return
	}
	if f(st) {
		st.ver++
		s.bumpLocked()
	}
}

var errResubscribe = errors.New("resubscribe")

func (s *Sessions) monitor(ctx context.Context, name string, poke chan struct{}) {
	var b relay.Backoff
	for ctx.Err() == nil {
		err := s.follow(ctx, name, poke, &b)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errResubscribe) {
			continue
		}
		msg := friendly(err)
		s.set(name, func(st *SessionState) bool {
			changed := st.Online || st.Error != msg
			st.Online, st.Error = false, msg
			return changed
		})
		if name == "default" && s.keepRunning && notRunning(err) {
			s.startServer()
		}
		b.Wait(ctx)
	}
}

func notRunning(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT)
}

func friendly(err error) string {
	switch {
	case err == nil:
		return ""
	case notRunning(err):
		return "herdr is not running"
	default:
		s := err.Error()
		if len(s) > 200 {
			s = s[:200]
		}
		return s
	}
}

// follow reads a snapshot, subscribes to events, and re-reads on every event.
func (s *Sessions) follow(ctx context.Context, name string, poke chan struct{}, b *relay.Backoff) error {
	snap, peer, err := readSnapshot(ctx, name)
	if err != nil {
		return err
	}
	panes := s.publish(name, peer, snap)

	sub, err := herdr.Dial(ctx, name)
	if err != nil {
		return err
	}
	defer sub.Close()
	if sub.Peer != peer {
		return errResubscribe // the server changed between the two connections
	}
	subs := []map[string]string{}
	for _, t := range []string{
		"workspace.created", "workspace.updated", "workspace.renamed", "workspace.moved", "workspace.reordered", "workspace.closed",
		"tab.created", "tab.closed", "tab.renamed", "tab.moved",
		"pane.created", "pane.closed", "pane.updated", "pane.moved", "pane.exited", "pane.agent_detected", "layout.updated",
	} {
		subs = append(subs, map[string]string{"type": t})
	}
	for _, p := range panes {
		subs = append(subs, map[string]string{"type": "pane.agent_status_changed", "pane_id": p})
	}
	if _, err := sub.Request("events.subscribe", map[string]any{"subscriptions": subs}); err != nil {
		return err
	}
	if _, herr, err := sub.Read(10 * time.Second); err != nil {
		return err
	} else if herr != nil {
		if herr.Code == "pane_not_found" || strings.Contains(herr.Message, "not found") {
			return errResubscribe // a pane closed in between; read again
		}
		return herr
	}
	b.Healthy()

	events := make(chan struct{}, 1)
	subErr := make(chan error, 1)
	go func() {
		for {
			_, herr, err := sub.Read(0)
			if err != nil {
				subErr <- err
				return
			}
			if herr != nil { // events_lost: the cache is stale, start over
				subErr <- errResubscribe
				return
			}
			select {
			case events <- struct{}{}:
			default:
			}
		}
	}()

	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-subErr:
			return err
		case <-events:
			if debounce == nil {
				debounce = time.After(120 * time.Millisecond)
			}
			continue
		case <-poke:
			if debounce == nil {
				debounce = time.After(50 * time.Millisecond)
			}
			continue
		case <-debounce:
		case <-tick.C:
		}
		debounce = nil
		snap, p2, err := readSnapshot(ctx, name)
		if err != nil {
			return err
		}
		if p2 != peer {
			return errResubscribe // herdr restarted: new epoch
		}
		now := s.publish(name, p2, snap)
		if !sameStrings(now, panes) {
			return errResubscribe // follow the new panes' agent states
		}
	}
}

func readSnapshot(ctx context.Context, name string) (json.RawMessage, herdr.Peer, error) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, peer, err := herdr.Call(cctx, name, "session.snapshot", nil)
	if err != nil {
		return nil, peer, err
	}
	var wrap struct {
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if err := json.Unmarshal(res, &wrap); err != nil || len(wrap.Snapshot) == 0 {
		return nil, peer, errors.New("unexpected snapshot from herdr")
	}
	if len(wrap.Snapshot) > maxSnapshot {
		return nil, peer, errors.New("herdr snapshot is too large")
	}
	return wrap.Snapshot, peer, nil
}

type snapView struct {
	Panes []struct {
		PaneID     string `json:"pane_id"`
		TerminalID string `json:"terminal_id"`
		Scroll     struct {
			ViewportRows int `json:"viewport_rows"`
		} `json:"scroll"`
	} `json:"panes"`
	Layouts []struct {
		Panes []struct {
			PaneID string `json:"pane_id"`
			Rect   struct {
				Width  int `json:"width"`
				Height int `json:"height"`
			} `json:"rect"`
		} `json:"panes"`
	} `json:"layouts"`
}

// publish stores a snapshot and returns the session's pane IDs.
func (s *Sessions) publish(name string, peer herdr.Peer, snap json.RawMessage) []string {
	var v snapView
	_ = json.Unmarshal(snap, &v)
	index := map[string]paneInfo{}
	rects := map[string][2]int{}
	for _, l := range v.Layouts {
		for _, p := range l.Panes {
			rects[p.PaneID] = [2]int{p.Rect.Width, p.Rect.Height}
		}
	}
	var ids []string
	for _, p := range v.Panes {
		ids = append(ids, p.PaneID)
		info := paneInfo{Terminal: p.TerminalID}
		if r, ok := rects[p.PaneID]; ok {
			// The real terminal is viewport_rows tall; borders take the same
			// amount from the width as from the height (measured on herdr 0.9.3).
			rows := p.Scroll.ViewportRows
			if rows <= 0 || rows > r[1] {
				rows = r[1]
			}
			cols := r[0] - (r[1] - rows)
			info.Cols, info.Rows = max(cols, 10), max(rows, 3)
		}
		index[p.PaneID] = info
	}
	sort.Strings(ids)
	epoch := peer.Epoch()
	s.set(name, func(st *SessionState) bool {
		changed := !st.Online || st.Epoch != epoch || st.Error != "" || !bytes.Equal(st.Snapshot, snap)
		st.Online, st.Error, st.Epoch, st.Snapshot, st.peer, st.index = true, "", epoch, snap, peer, index
		return changed
	})
	return ids
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// startServer starts the default herdr server through the user's login shell
// (so panes get the user's normal PATH), detached so it outlives the dock.
func (s *Sessions) startServer() {
	s.mu.Lock()
	if time.Since(s.started) < time.Minute {
		s.mu.Unlock()
		return
	}
	s.started = time.Now()
	s.mu.Unlock()
	bin, err := herdr.Binary()
	if err != nil {
		log.Printf("cannot start herdr: %v", err)
		return
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-lc", `exec "$0" server`, bin)
	cmd.Env = herdr.CleanEnv()
	home, _ := os.UserHomeDir()
	cmd.Dir = home
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		log.Printf("cannot start herdr: %v", err)
		return
	}
	log.Printf("started the herdr server (pid %d)", cmd.Process.Pid)
	go cmd.Wait()
}
