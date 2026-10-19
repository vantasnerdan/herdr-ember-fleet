package hub

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/keys"
)

// Link is a join link the hub issued. The hub keeps its own record, whatever
// the relay says: a link binds to exactly one dock key, once.
type Link struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	PairKey []byte `json:"pair_key,omitempty"` // dropped once used, cancelled or expired
	Created int64  `json:"created"`
	Expires int64  `json:"expires"`
	State   string `json:"state"` // pending, used, cancelled
	Dock    string `json:"dock,omitempty"`
}

// DockRec is a paired dock. Its ID is its key fingerprint; Name is the
// machine name (hub-owned; what the page groups by).
type DockRec struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	LinkID string `json:"link_id"`
	Paired int64  `json:"paired"`
}

// Token is a terminal access token for `attach` and `term` (stored hashed).
type Token struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Hash     string `json:"hash"`
	Created  int64  `json:"created"`
	Expires  int64  `json:"expires"`
	LastUsed int64  `json:"last_used,omitempty"`
}

// BrowserSession is a signed-in browser (stored by hash of its cookie).
type BrowserSession struct {
	Hash    string `json:"hash"`
	CSRF    string `json:"csrf"`
	Created int64  `json:"created"`
	Expires int64  `json:"expires"`
}

// State is hub.json.
type State struct {
	Fleet      string                     `json:"fleet"`
	Relay      string                     `json:"relay"`
	Pin        string                     `json:"pin"`
	URL        string                     `json:"url,omitempty"` // public URL behind a proxy (set-url)
	Links      map[string]*Link           `json:"links"`
	Docks      map[string]*DockRec        `json:"docks"`
	Tombstones map[string]int64           `json:"tombstones"` // removed docks, ignored forever
	Tokens     map[string]*Token          `json:"tokens"`
	Sessions   map[string]*BrowserSession `json:"sessions"`
	Groups     []*Group                   `json:"groups"`
}

// Store serializes every change to hub.json: one lock, one atomic write.
type Store struct {
	path string
	mu   sync.Mutex
	s    State
}

// OpenStore reads hub.json from dir.
func OpenStore(dir string) (*Store, error) {
	st := &Store{path: filepath.Join(dir, "hub.json")}
	b, err := os.ReadFile(st.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("the hub is not set up yet: run `ember-fleet hub init`")
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &st.s); err != nil {
		return nil, fmt.Errorf("hub.json: %w", err)
	}
	st.fill()
	return st, nil
}

// CreateStore writes a new hub.json.
func CreateStore(dir string, s State) (*Store, error) {
	st := &Store{path: filepath.Join(dir, "hub.json"), s: s}
	st.fill()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st, st.saveLocked()
}

func (st *Store) fill() {
	if st.s.Links == nil {
		st.s.Links = map[string]*Link{}
	}
	if st.s.Docks == nil {
		st.s.Docks = map[string]*DockRec{}
	}
	if st.s.Tombstones == nil {
		st.s.Tombstones = map[string]int64{}
	}
	if st.s.Tokens == nil {
		st.s.Tokens = map[string]*Token{}
	}
	if st.s.Sessions == nil {
		st.s.Sessions = map[string]*BrowserSession{}
	}
	if st.s.Fleet == "" {
		st.s.Fleet = "Fleet"
	}
}

func (st *Store) saveLocked() error {
	now := time.Now().UnixMilli()
	for id, l := range st.s.Links {
		if l.State == "pending" && l.Expires < now {
			l.PairKey = nil // expired: nothing left that could pair
		}
		if l.State != "pending" || l.Expires < now {
			l.PairKey = nil
		}
		if l.Expires < now-30*24*3600*1000 && l.State != "used" {
			delete(st.s.Links, id)
		}
	}
	for h, s := range st.s.Sessions {
		if s.Expires < now {
			delete(st.s.Sessions, h)
		}
	}
	for id, t := range st.s.Tokens {
		if t.Expires < now {
			delete(st.s.Tokens, id)
		}
	}
	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	return keys.WriteFileAtomic(st.path, b, 0o600)
}

// Update runs f with the lock held and saves if f returns nil. If saving
// fails, the change is not reported as made.
func (st *Store) Update(f func(s *State) error) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	backup, _ := json.Marshal(st.s)
	if err := f(&st.s); err != nil {
		return err
	}
	if err := st.saveLocked(); err != nil {
		var restored State
		json.Unmarshal(backup, &restored)
		st.s = restored
		st.fill()
		return fmt.Errorf("could not save the hub's records: %w", err)
	}
	return nil
}

// View runs f with the lock held, read-only.
func (st *Store) View(f func(s *State)) {
	st.mu.Lock()
	defer st.mu.Unlock()
	f(&st.s)
}

func randToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte("ember-token-v1\x00" + t))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func shortID() string {
	b := make([]byte, 9)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
