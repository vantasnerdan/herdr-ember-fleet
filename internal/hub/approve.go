package hub

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Signing in a new browser by approval, like a TV: the new browser asks and
// shows a short code; any browser that is already signed in sees the request
// with the same code and approves or denies it; the new browser then gets its
// own session. Only the browser that asked can collect it (it holds a secret
// the approver never sees).

const (
	requestLife     = 10 * time.Minute
	maxSigninAsks   = 10
	codeAlphabet    = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // no 0/O, 1/I/L
	sessionRenewGap = 24 * time.Hour
)

type signinAsk struct {
	ID       string
	Code     string
	PollHash string
	Agent    string
	At       time.Time
	State    string // pending, approved, denied
}

// SigninAsk is a pending request as signed-in pages see it.
type SigninAsk struct {
	ID    string `json:"id"`
	Code  string `json:"code"`
	Agent string `json:"agent"`
	At    int64  `json:"at"`
}

func newCode() string {
	b := make([]byte, 8)
	rand.Read(b)
	out := make([]byte, 0, 9)
	for i, c := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, codeAlphabet[int(c)%len(codeAlphabet)])
	}
	return string(out)
}

// browserName turns a User-Agent into "Chrome on macOS" and the like.
func browserName(ua string) string {
	browser := "A browser"
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	os := ""
	switch {
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		os = "iOS"
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "Mac OS X"):
		os = "macOS"
	case strings.Contains(ua, "Windows"):
		os = "Windows"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	}
	if os == "" {
		return browser
	}
	return browser + " on " + os
}

func (h *Hub) pruneAsksLocked() {
	for id, a := range h.asks {
		if time.Since(a.At) > requestLife {
			delete(h.asks, id)
		}
	}
}

// NewSigninAsk records a request from a browser that is not signed in.
func (h *Hub) NewSigninAsk(userAgent string) (id, code, poll string) {
	poll = randToken()
	a := &signinAsk{ID: shortID(), Code: newCode(), PollHash: hashToken(poll), Agent: browserName(userAgent), At: time.Now(), State: "pending"}
	h.mu.Lock()
	h.pruneAsksLocked()
	if len(h.asks) >= maxSigninAsks { // drop the oldest
		var oldest *signinAsk
		for _, x := range h.asks {
			if oldest == nil || x.At.Before(oldest.At) {
				oldest = x
			}
		}
		delete(h.asks, oldest.ID)
	}
	h.asks[a.ID] = a
	h.mu.Unlock()
	h.signinsChanged()
	return a.ID, a.Code, poll
}

// PendingAsks lists requests waiting for approval, oldest first.
func (h *Hub) PendingAsks() []SigninAsk {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneAsksLocked()
	out := []SigninAsk{}
	for _, a := range h.asks {
		if a.State == "pending" {
			out = append(out, SigninAsk{ID: a.ID, Code: a.Code, Agent: a.Agent, At: a.At.UnixMilli()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// DecideAsk approves or denies a pending request.
func (h *Hub) DecideAsk(id string, approve bool) error {
	h.mu.Lock()
	h.pruneAsksLocked()
	a := h.asks[id]
	if a == nil || a.State != "pending" {
		h.mu.Unlock()
		return errors.New("that request is gone (it may have expired)")
	}
	if approve {
		a.State = "approved"
	} else {
		a.State = "denied"
	}
	h.mu.Unlock()
	h.signinsChanged()
	return nil
}

// collectAsk is the asking browser's poll: once approved, it gets a session.
func (h *Hub) collectAsk(id, poll string) (state, cookie, csrf string, err error) {
	h.mu.Lock()
	h.pruneAsksLocked()
	a := h.asks[id]
	if a == nil {
		h.mu.Unlock()
		return "expired", "", "", nil
	}
	if subtle.ConstantTimeCompare([]byte(a.PollHash), []byte(hashToken(poll))) != 1 {
		h.mu.Unlock()
		return "", "", "", errors.New("not your request")
	}
	state = a.State
	if state != "pending" {
		delete(h.asks, id) // approved or denied: one answer, once
	}
	h.mu.Unlock()
	if state != "approved" {
		return state, "", "", nil
	}
	cookie, csrf, err = h.startSession(a.Agent, "approval")
	return state, cookie, csrf, err
}

// cancelAsk withdraws a request; only the browser that asked can.
func (h *Hub) cancelAsk(id, poll string) {
	h.mu.Lock()
	a := h.asks[id]
	gone := a != nil && subtle.ConstantTimeCompare([]byte(a.PollHash), []byte(hashToken(poll))) == 1
	if gone {
		delete(h.asks, id)
	}
	h.mu.Unlock()
	if gone {
		h.signinsChanged()
	}
}

// startSession creates a browser session and returns its cookie value and CSRF token.
func (h *Hub) startSession(agent, via string) (cookie, csrf string, err error) {
	cookie, csrf = randToken(), randToken()
	now := time.Now()
	err = h.store.Update(func(s *State) error {
		s.Sessions[hashToken(cookie)] = &BrowserSession{Hash: hashToken(cookie), CSRF: csrf, Agent: agent, Via: via,
			Created: now.UnixMilli(), Seen: now.UnixMilli(), Expires: now.Add(sessionLife).UnixMilli()}
		return nil
	})
	return cookie, csrf, err
}

// renewSession slides a session's expiry forward, at most once a day.
func (h *Hub) renewSession(hash string) (time.Time, bool) {
	var exp time.Time
	renewed := false
	h.store.Update(func(s *State) error {
		bs := s.Sessions[hash]
		if bs == nil {
			return errors.New("gone")
		}
		if time.Until(time.UnixMilli(bs.Expires)) < sessionLife-sessionRenewGap {
			bs.Expires = time.Now().Add(sessionLife).UnixMilli()
			bs.Seen = time.Now().UnixMilli()
			renewed = true
		}
		exp = time.UnixMilli(bs.Expires)
		return nil
	})
	return exp, renewed
}

// nameSession records the browser of a session signed in before names were kept.
func (h *Hub) nameSession(hash, agent string) {
	h.store.Update(func(s *State) error {
		bs := s.Sessions[hash]
		if bs == nil || bs.Agent != "" {
			return errors.New("nothing to do")
		}
		bs.Agent = agent
		if bs.Via == "" {
			bs.Via = "link"
		}
		return nil
	})
}

func (h *Hub) signinsChanged() {
	h.live.Lock()
	defer h.live.Unlock()
	for w := range h.watchers {
		w.markSignins()
	}
}

// --- HTTP ---------------------------------------------------------------------

func (w *Web) askSignin(rw http.ResponseWriter, r *http.Request) {
	if !w.originOK(r) {
		jsonErr(rw, http.StatusForbidden, "bad origin")
		return
	}
	id, code, poll := w.h.NewSigninAsk(r.UserAgent())
	jsonOut(rw, map[string]any{"ok": true, "id": id, "code": code, "poll": poll})
}

func (w *Web) pollSignin(rw http.ResponseWriter, r *http.Request) {
	if !w.originOK(r) {
		jsonErr(rw, http.StatusForbidden, "bad origin")
		return
	}
	var req struct {
		ID   string `json:"id"`
		Poll string `json:"poll"`
	}
	if readJSON(r, &req) != nil || req.ID == "" || req.Poll == "" {
		jsonErr(rw, http.StatusBadRequest, "bad request")
		return
	}
	state, cookie, csrf, err := w.h.collectAsk(req.ID, req.Poll)
	if err != nil {
		jsonErr(rw, http.StatusForbidden, err.Error())
		return
	}
	if cookie != "" {
		w.setSessionCookie(rw, r, cookie, sessionLife)
		jsonOut(rw, map[string]any{"ok": true, "state": state, "csrf": csrf})
		return
	}
	jsonOut(rw, map[string]any{"ok": true, "state": state})
}

func (w *Web) cancelSigninAsk(rw http.ResponseWriter, r *http.Request) {
	if !w.originOK(r) {
		jsonErr(rw, http.StatusForbidden, "bad origin")
		return
	}
	var req struct {
		ID   string `json:"id"`
		Poll string `json:"poll"`
	}
	if readJSON(r, &req) != nil {
		jsonErr(rw, http.StatusBadRequest, "bad request")
		return
	}
	w.h.cancelAsk(req.ID, req.Poll)
	jsonOut(rw, map[string]any{"ok": true})
}

func (w *Web) listAsks(rw http.ResponseWriter, r *http.Request, p principal) {
	jsonOut(rw, map[string]any{"requests": w.h.PendingAsks()})
}

func (w *Web) decideAsk(rw http.ResponseWriter, r *http.Request, p principal) {
	var req struct {
		ID      string `json:"id"`
		Approve bool   `json:"approve"`
	}
	if readJSON(r, &req) != nil {
		jsonErr(rw, http.StatusBadRequest, "bad request")
		return
	}
	if err := w.h.DecideAsk(req.ID, req.Approve); err != nil {
		jsonErr(rw, http.StatusNotFound, err.Error())
		return
	}
	jsonOut(rw, map[string]any{"ok": true})
}

func (w *Web) setSessionCookie(rw http.ResponseWriter, r *http.Request, cookie string, life time.Duration) {
	http.SetCookie(rw, &http.Cookie{
		Name: cookieName, Value: cookie, Path: "/", HttpOnly: true, Secure: w.secureReq(r),
		SameSite: http.SameSiteStrictMode, MaxAge: int(life / time.Second),
	})
}
