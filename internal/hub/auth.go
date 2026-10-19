package hub

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/pair"
)

// NewSignin makes a one-time sign-in token, valid for 15 minutes. It lives
// only in memory: a hub restart cancels outstanding sign-in links.
func (h *Hub) NewSignin() string {
	t := randToken()
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for k, exp := range h.signins {
		if now.After(exp) {
			delete(h.signins, k)
		}
	}
	h.signins[hashToken(t)] = now.Add(signinLife)
	return t
}

// redeemSignin uses up a sign-in token and starts a browser session.
func (h *Hub) redeemSignin(token string) (cookie, csrf string, err error) {
	k := hashToken(token)
	h.mu.Lock()
	exp, ok := h.signins[k]
	delete(h.signins, k) // one use, whatever happens next
	h.mu.Unlock()
	if !ok || time.Now().After(exp) {
		return "", "", errors.New("this sign-in link was already used or has expired; run `ember-fleet login-link` for a new one")
	}
	cookie, csrf = randToken(), randToken()
	err = h.store.Update(func(s *State) error {
		s.Sessions[hashToken(cookie)] = &BrowserSession{Hash: hashToken(cookie), CSRF: csrf,
			Created: time.Now().UnixMilli(), Expires: time.Now().Add(sessionLife).UnixMilli()}
		return nil
	})
	return cookie, csrf, err
}

func (h *Hub) session(cookie string) *BrowserSession {
	h.credMu.Lock()
	revoked := h.revoked[hashToken(cookie)]
	h.credMu.Unlock()
	if revoked {
		return nil
	}
	var out *BrowserSession
	h.store.View(func(s *State) {
		if bs := s.Sessions[hashToken(cookie)]; bs != nil && bs.Expires > time.Now().UnixMilli() {
			cp := *bs
			out = &cp
		}
	})
	return out
}

// endSession signs a browser out. If the hub can't save that, the session is
// still revoked in memory (until a restart) and the error is reported.
func (h *Hub) endSession(cookie string) error {
	hash := hashToken(cookie)
	err := h.store.Update(func(s *State) error { delete(s.Sessions, hash); return nil })
	if err != nil {
		h.credMu.Lock()
		if h.revoked == nil {
			h.revoked = map[string]bool{}
		}
		h.revoked[hash] = true
		h.credMu.Unlock()
	}
	h.cutCredential("session:" + hash)
	return err
}

// CreateToken makes a terminal access token (shown once, stored hashed).
func (h *Hub) CreateToken(name string) (string, error) {
	if name == "" {
		name = "terminal"
	}
	if err := pair.CheckName(name); err != nil {
		return "", err
	}
	tok := "eft_" + randToken()
	err := h.store.Update(func(s *State) error {
		if len(s.Tokens) >= 100 {
			return errors.New("too many tokens; revoke some first")
		}
		id := shortID()
		s.Tokens[id] = &Token{ID: id, Name: name, Hash: hashToken(tok), Created: time.Now().UnixMilli(),
			Expires: time.Now().Add(tokenLife).UnixMilli()}
		return nil
	})
	return tok, err
}

func (h *Hub) checkToken(tok string) *Token {
	k := hashToken(tok)
	var out *Token
	h.store.View(func(s *State) {
		for _, t := range s.Tokens {
			if t.Hash == k && t.Expires > time.Now().UnixMilli() {
				cp := *t
				out = &cp
			}
		}
	})
	if out != nil && time.Since(time.UnixMilli(out.LastUsed)) > time.Hour {
		h.store.Update(func(s *State) error {
			if t := s.Tokens[out.ID]; t != nil {
				t.LastUsed = time.Now().UnixMilli()
			}
			return nil
		})
	}
	return out
}

// RevokeToken deletes a token and closes every socket opened with it.
func (h *Hub) RevokeToken(id string) error {
	err := h.store.Update(func(s *State) error {
		if s.Tokens[id] == nil {
			return errors.New("no such token")
		}
		delete(s.Tokens, id)
		return nil
	})
	if err != nil {
		return err
	}
	h.cutCredential("token:" + id)
	return nil
}

// cutCredential cancels every request registered under a credential. The
// record is already gone, so nothing can register under it afterwards.
func (h *Hub) cutCredential(key string) {
	h.credMu.Lock()
	cuts := h.credCuts[key]
	delete(h.credCuts, key)
	h.credMu.Unlock()
	for c := range cuts {
		(*c)()
	}
}

// credValid reports whether a credential's record still exists and is unexpired.
func (h *Hub) credValid(key string) bool {
	now := time.Now().UnixMilli()
	ok := false
	h.store.View(func(s *State) {
		if id, isTok := strings.CutPrefix(key, "token:"); isTok {
			t := s.Tokens[id]
			ok = t != nil && t.Expires > now
		} else if hash, isSess := strings.CutPrefix(key, "session:"); isSess {
			bs := s.Sessions[hash]
			ok = bs != nil && bs.Expires > now && !h.revoked[hash] // caller holds credMu
		}
	})
	return ok
}

// credContext ties a long-lived request (a WebSocket) to the credential that
// opened it: it ends when the credential expires, and revoking the token or
// signing out cuts it. The record is rechecked and the request registered
// under one lock, so a revocation can't fall between the two.
func (h *Hub) credContext(ctx context.Context, p principal) (context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithDeadline(ctx, p.expires)
	h.credMu.Lock()
	if !h.credValid(p.cred) {
		h.credMu.Unlock()
		cancel()
		return nil, nil, errors.New("signed out")
	}
	if h.credCuts[p.cred] == nil {
		h.credCuts[p.cred] = map[*context.CancelFunc]struct{}{}
	}
	h.credCuts[p.cred][&cancel] = struct{}{}
	h.credMu.Unlock()
	return ctx, func() {
		cancel()
		h.credMu.Lock()
		delete(h.credCuts[p.cred], &cancel)
		if len(h.credCuts[p.cred]) == 0 {
			delete(h.credCuts, p.cred)
		}
		h.credMu.Unlock()
	}, nil
}
