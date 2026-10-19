package hub

import (
	"context"
	"testing"
	"time"
)

func testHub(t *testing.T) *Hub {
	t.Helper()
	st, err := CreateStore(t.TempDir(), State{Fleet: "t"})
	if err != nil {
		t.Fatal(err)
	}
	return &Hub{store: st, docks: map[string]*dockLive{}, pairing: map[string]time.Time{},
		watchers: map[*liveSub]struct{}{}, signins: map[string]time.Time{},
		credCuts: map[string]map[*context.CancelFunc]struct{}{}, revoked: map[string]bool{}}
}

func tokenPrincipal(t *testing.T, h *Hub, tok string) principal {
	t.Helper()
	tk := h.checkToken(tok)
	if tk == nil {
		t.Fatal("token not found")
	}
	return principal{kind: "token", cred: "token:" + tk.ID, expires: time.UnixMilli(tk.Expires)}
}

func TestRevokeCutsLiveSocketsAndLaterOnes(t *testing.T) {
	h := testHub(t)
	tok, err := h.CreateToken("laptop")
	if err != nil {
		t.Fatal(err)
	}
	p := tokenPrincipal(t, h, tok)
	ctx, cancel, err := h.credContext(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := h.RevokeToken(p.cred[len("token:"):]); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("revoking the token did not end its socket")
	}
	// A request authenticated just before the revoke must not register after it.
	if _, _, err := h.credContext(context.Background(), p); err == nil {
		t.Fatal("a revoked token registered a new socket")
	}
}

func TestSignOutCutsBrowserSockets(t *testing.T) {
	h := testHub(t)
	signin := h.NewSignin()
	cookie, _, err := h.redeemSignin(signin)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.redeemSignin(signin); err == nil {
		t.Fatal("a sign-in link worked twice")
	}
	s := h.session(cookie)
	p := principal{kind: "browser", cred: "session:" + s.Hash, expires: time.UnixMilli(s.Expires)}
	ctx, cancel, err := h.credContext(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	h.endSession(cookie)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("signing out did not end the browser's sockets")
	}
}

func TestSocketEndsWhenCredentialExpires(t *testing.T) {
	h := testHub(t)
	tok, _ := h.CreateToken("short")
	p := tokenPrincipal(t, h, tok)
	p.expires = time.Now().Add(50 * time.Millisecond)
	ctx, cancel, err := h.credContext(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("socket outlived its credential")
	}
}

func TestLiveFeedNeverDropsAChange(t *testing.T) {
	h := testHub(t)
	sub, stop := h.subscribe()
	defer stop()
	for i := 0; i < 10000; i++ { // a busy machine
		sub.mark("busy")
	}
	sub.mark("quiet") // another machine changes once
	c := sub.take()
	if !c.dirty["quiet"] || !c.dirty["busy"] {
		t.Fatalf("lost a change: %v", c.dirty)
	}
}

func TestSetURLSwitchesAllowedNames(t *testing.T) {
	w, err := NewWeb(testHub(t), "127.0.0.1:4790", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := w.cfg.Load()
	if !c.hosts["127.0.0.1:4790"] || !c.hosts["localhost:4790"] || w.Base() != "http://127.0.0.1:4790" {
		t.Fatalf("loopback config: %+v", c)
	}
	if err := w.setURL("https://fleet.example.com/"); err != nil {
		t.Fatal(err)
	}
	c = w.cfg.Load()
	if !c.hosts["fleet.example.com"] || !c.origins["https://fleet.example.com"] || !c.hosts["127.0.0.1:4790"] || w.Base() != "https://fleet.example.com" {
		t.Fatalf("proxied config: %+v base %s", c, w.Base())
	}
	if err := w.setURL("https://other.example.com"); err != nil {
		t.Fatal(err)
	}
	if c = w.cfg.Load(); c.hosts["fleet.example.com"] || c.origins["https://fleet.example.com"] {
		t.Fatal("old URL still allowed after a change")
	}
	if err := w.setURL("https://evil.example.com/path"); err == nil {
		t.Fatal("URL with a path accepted")
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.2.6", "0.2.5", true},
		{"0.2.10", "0.2.9", true},
		{"1.0.0", "0.9.9", true},
		{"0.2.5", "0.2.5", false},
		{"0.2.4", "0.2.5", false}, // never downgrade
		{"0.2.6", "dev", true},
		{"0.2.6", "", true},
		{"dev", "0.2.5", false},         // a dev hub never pushes
		{"0.2.6-dirty", "0.2.5", false}, // nor a dirty build
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
