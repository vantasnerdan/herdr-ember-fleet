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
	dirty, _ := sub.take()
	if !dirty["quiet"] || !dirty["busy"] {
		t.Fatalf("lost a change: %v", dirty)
	}
}
