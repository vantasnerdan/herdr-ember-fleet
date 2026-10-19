package hub

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/webui"
)

const (
	cookieName   = "ember_session"
	sessionLife  = 30 * 24 * time.Hour
	signinLife   = 15 * time.Minute
	tokenLife    = 90 * 24 * time.Hour
	wsProtocol   = "ember.v1"
	csrfProtocol = "csrf."
)

// Web is the hub's HTTP side.
type Web struct {
	h       *Hub
	hosts   map[string]bool // allowed Host headers
	origins map[string]bool // allowed Origin values
	secure  bool            // HTTPS: cookies are Secure
	base    string          // the URL people use, for links and install
}

// NewWeb configures the allowed names. base is the page's own URL, e.g.
// http://127.0.0.1:4790 or https://fleet.example.com.
func NewWeb(h *Hub, base string, extraOrigins []string) (*Web, error) {
	w := &Web{h: h, hosts: map[string]bool{}, origins: map[string]bool{}, base: strings.TrimRight(base, "/")}
	add := func(o string) error {
		u, err := url.Parse(o)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("bad origin " + o)
		}
		w.hosts[u.Host] = true
		w.origins[u.Scheme+"://"+u.Host] = true
		if host, port, err := net.SplitHostPort(u.Host); err == nil && host == "127.0.0.1" {
			w.hosts["localhost:"+port] = true
			w.origins[u.Scheme+"://localhost:"+port] = true
		}
		return nil
	}
	if err := add(w.base); err != nil {
		return nil, err
	}
	for _, o := range extraOrigins {
		if err := add(o); err != nil {
			return nil, err
		}
	}
	w.secure = strings.HasPrefix(w.base, "https://")
	return w, nil
}

// Base is the page's URL.
func (w *Web) Base() string { return w.base }

// Handler returns the whole site.
func (w *Web) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/me", w.me)
	mux.HandleFunc("POST /api/signin", w.signin)
	mux.HandleFunc("POST /api/signout", w.auth(w.signout))
	mux.HandleFunc("GET /api/fleet", w.auth(w.fleet))
	mux.HandleFunc("GET /api/live", w.auth(w.liveWS))
	mux.HandleFunc("GET /api/term", w.auth(w.termWS))
	mux.HandleFunc("GET /api/endpoint", w.auth(w.endpointWS))
	mux.HandleFunc("POST /api/action", w.auth(w.action))
	mux.HandleFunc("GET /api/links", w.auth(w.links))
	mux.HandleFunc("POST /api/links", w.auth(w.createLink))
	mux.HandleFunc("DELETE /api/links/{id}", w.auth(w.cancelLink))
	mux.HandleFunc("DELETE /api/docks/{id}", w.auth(w.removeDock))
	mux.HandleFunc("POST /api/docks/{id}/rename", w.auth(w.renameDock))
	mux.HandleFunc("GET /api/tokens", w.auth(w.listTokens))
	mux.HandleFunc("POST /api/tokens", w.auth(w.createToken))
	mux.HandleFunc("DELETE /api/tokens/{id}", w.auth(w.revokeToken))
	mux.HandleFunc("GET /install.sh", w.installScript)
	mux.HandleFunc("GET /dl/{file}", w.download)
	mux.Handle("/", webui.Handler())
	return w.guard(mux)
}

// guard rejects requests for a name that is not ours (DNS rebinding) and sets
// safe headers on everything.
func (w *Web) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !w.hosts[r.Host] {
			http.Error(rw, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		hd := rw.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if w.secure {
			hd.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(rw, r)
	})
}

type principal struct {
	kind    string // "browser" or "token"
	name    string
	csrf    string
	cred    string    // "token:<id>" or "session:<hash>"
	expires time.Time // when the credential expires
}

type authed func(rw http.ResponseWriter, r *http.Request, p principal)

func (w *Web) originOK(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return o != "" && w.origins[o]
}

// auth accepts a browser session (cookie + CSRF + Origin for anything that
// changes state, CSRF subprotocol for WebSockets) or a bearer access token
// (terminal tools; never sent by browsers on their own).
func (w *Web) auth(next authed) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			if r.Header.Get("Origin") != "" {
				jsonErr(rw, http.StatusForbidden, "tokens are for terminal tools, not pages")
				return
			}
			t := w.h.checkToken(bearer)
			if t == nil {
				jsonErr(rw, http.StatusUnauthorized, "unknown or expired token")
				return
			}
			next(rw, r, principal{kind: "token", name: "token:" + t.Name, cred: "token:" + t.ID, expires: time.UnixMilli(t.Expires)})
			return
		}
		c, err := r.Cookie(cookieName)
		if err != nil {
			jsonErr(rw, http.StatusUnauthorized, "signed out")
			return
		}
		s := w.h.session(c.Value)
		if s == nil {
			jsonErr(rw, http.StatusUnauthorized, "signed out")
			return
		}
		p := principal{kind: "browser", name: "owner", csrf: s.CSRF, cred: "session:" + s.Hash, expires: time.UnixMilli(s.Expires)}
		isWS := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		if isWS {
			if !w.originOK(r) || !hasCSRFProtocol(r, s.CSRF) {
				jsonErr(rw, http.StatusForbidden, "bad origin or CSRF")
				return
			}
		} else if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !w.originOK(r) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Ember-CSRF")), []byte(s.CSRF)) != 1 {
				jsonErr(rw, http.StatusForbidden, "bad origin or CSRF")
				return
			}
		}
		next(rw, r, p)
	}
}

func hasCSRFProtocol(r *http.Request, csrf string) bool {
	for _, field := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(field, ",") {
			p = strings.TrimSpace(p)
			if v, ok := strings.CutPrefix(p, csrfProtocol); ok && subtle.ConstantTimeCompare([]byte(v), []byte(csrf)) == 1 {
				return true
			}
		}
	}
	return false
}

func (w *Web) acceptWS(rw http.ResponseWriter, r *http.Request, p principal) (*websocket.Conn, error) {
	opts := &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled}
	if p.kind == "browser" {
		opts.Subprotocols = []string{wsProtocol}
		// Origin was checked exactly in auth; coder/websocket's own check
		// compares against the Host, which we have also checked.
		u, _ := url.Parse(r.Header.Get("Origin"))
		opts.OriginPatterns = []string{u.Host}
	}
	c, err := websocket.Accept(rw, r, opts)
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(4 << 20)
	return c, nil
}

func jsonOut(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(rw).Encode(v)
}

func jsonErr(rw http.ResponseWriter, code int, msg string) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	rw.WriteHeader(code)
	json.NewEncoder(rw).Encode(map[string]any{"ok": false, "error": msg})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func (w *Web) me(rw http.ResponseWriter, r *http.Request) {
	var fleet string
	w.h.store.View(func(s *State) { fleet = s.Fleet })
	out := map[string]any{"signedIn": false, "fleet": fleet, "hub": w.h.Fingerprint()[:12], "version": Version}
	if c, err := r.Cookie(cookieName); err == nil {
		if s := w.h.session(c.Value); s != nil {
			out["signedIn"] = true
			out["csrf"] = s.CSRF
		}
	}
	jsonOut(rw, out)
}

func (w *Web) signin(rw http.ResponseWriter, r *http.Request) {
	if !w.originOK(r) {
		jsonErr(rw, http.StatusForbidden, "bad origin")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if readJSON(r, &req) != nil || req.Token == "" {
		jsonErr(rw, http.StatusBadRequest, "missing token")
		return
	}
	cookie, csrf, err := w.h.redeemSignin(req.Token)
	if err != nil {
		jsonErr(rw, http.StatusUnauthorized, err.Error())
		return
	}
	http.SetCookie(rw, &http.Cookie{
		Name: cookieName, Value: cookie, Path: "/", HttpOnly: true, Secure: w.secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionLife / time.Second),
	})
	jsonOut(rw, map[string]any{"ok": true, "csrf": csrf})
}

func (w *Web) signout(rw http.ResponseWriter, r *http.Request, p principal) {
	http.SetCookie(rw, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: w.secure, SameSite: http.SameSiteStrictMode})
	if c, err := r.Cookie(cookieName); err == nil {
		if err := w.h.endSession(c.Value); err != nil {
			jsonErr(rw, http.StatusInternalServerError, "signed out until the hub restarts, but it could not save that: "+err.Error())
			return
		}
	}
	jsonOut(rw, map[string]any{"ok": true})
}

func (w *Web) fleet(rw http.ResponseWriter, r *http.Request, p principal) {
	jsonOut(rw, map[string]any{"machines": w.h.Machines()})
}

// liveWS pushes machine updates, coalesced to at most ten a second.
func (w *Web) liveWS(rw http.ResponseWriter, r *http.Request, p principal) {
	c, err := w.acceptWS(rw, r, p)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel, err := w.h.credContext(r.Context(), p)
	if err != nil {
		c.Close(websocket.StatusPolicyViolation, "signed out")
		return
	}
	defer cancel()
	ctx = c.CloseRead(ctx)
	sub, stop := w.h.subscribe()
	defer stop()
	send := func(v any) error {
		b, _ := json.Marshal(v)
		wctx, wcancel := context.WithTimeout(ctx, 30*time.Second)
		defer wcancel()
		return c.Write(wctx, websocket.MessageText, b)
	}
	machines := w.h.Machines()
	if send(map[string]any{"type": "hello", "fleet": map[string]any{"machines": machines}}) != nil {
		return
	}
	known := map[string]bool{}
	for _, m := range machines {
		known[m.Host] = true
	}
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if c.Ping(ctx) != nil {
				return
			}
			continue
		case <-sub.wake:
		}
		// let more changes gather for up to 100 ms, then send them all
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return
		}
		dirty, all := sub.take()
		machines := w.h.Machines()
		now := map[string]bool{}
		for _, m := range machines {
			now[m.Host] = true
			if all || dirty[m.Host] || !known[m.Host] {
				if send(map[string]any{"type": "machine", "machine": m}) != nil {
					return
				}
			}
		}
		for host := range known {
			if !now[host] {
				if send(map[string]any{"type": "removed", "host": host}) != nil {
					return
				}
			}
		}
		known = now
	}
}

func (w *Web) links(rw http.ResponseWriter, r *http.Request, p principal) {
	jsonOut(rw, map[string]any{"links": w.h.Links()})
}

func (w *Web) createLink(rw http.ResponseWriter, r *http.Request, p principal) {
	var req struct {
		Name string `json:"name"`
	}
	if readJSON(r, &req) != nil {
		jsonErr(rw, http.StatusBadRequest, "bad request")
		return
	}
	link, l, err := w.h.CreateLink(r.Context(), strings.TrimSpace(req.Name))
	if err != nil {
		jsonErr(rw, http.StatusBadRequest, err.Error())
		return
	}
	jsonOut(rw, map[string]any{"id": l.ID, "link": link, "command": w.installCommandFor(l.Name), "expires": l.Expires})
}

// installCommandFor is what to run for a new machine. A hub on loopback can't
// be reached from other computers, so then it's the SSH helper instead.
func (w *Web) installCommandFor(name string) string {
	if u, err := url.Parse(w.base); err == nil && isLoopbackHost(u.Hostname()) {
		return "scripts/add-host.sh " + shellWord(name) + " USER@HOST"
	}
	return "curl -fsSL " + w.base + "/install.sh | sh"
}

func (w *Web) installCommand() string { return w.installCommandFor("NAME") }

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func shellWord(s string) string {
	for _, r := range s {
		if !(r == '-' || r == '_' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}

func (w *Web) cancelLink(rw http.ResponseWriter, r *http.Request, p principal) {
	if err := w.h.CancelLink(r.PathValue("id")); err != nil {
		jsonErr(rw, http.StatusNotFound, err.Error())
		return
	}
	jsonOut(rw, map[string]any{"ok": true})
}

func (w *Web) removeDock(rw http.ResponseWriter, r *http.Request, p principal) {
	if err := w.h.RemoveDock(r.Context(), r.PathValue("id")); err != nil {
		jsonErr(rw, http.StatusNotFound, err.Error())
		return
	}
	jsonOut(rw, map[string]any{"ok": true})
}

func (w *Web) renameDock(rw http.ResponseWriter, r *http.Request, p principal) {
	var req struct {
		Name string `json:"name"`
	}
	if readJSON(r, &req) != nil {
		jsonErr(rw, http.StatusBadRequest, "bad request")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 64 {
		jsonErr(rw, http.StatusBadRequest, "name must be 1-64 characters")
		return
	}
	id := r.PathValue("id")
	err := w.h.store.Update(func(s *State) error {
		d := s.Docks[id]
		if d == nil {
			return errors.New("no such machine")
		}
		d.Name = name
		return nil
	})
	if err != nil {
		jsonErr(rw, http.StatusNotFound, err.Error())
		return
	}
	w.h.changedAll()
	jsonOut(rw, map[string]any{"ok": true})
}

func (w *Web) listTokens(rw http.ResponseWriter, r *http.Request, p principal) {
	type tv struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Created  int64  `json:"created"`
		Expires  int64  `json:"expires"`
		LastUsed int64  `json:"lastUsed"`
	}
	out := []tv{}
	w.h.store.View(func(s *State) {
		for _, t := range s.Tokens {
			out = append(out, tv{t.ID, t.Name, t.Created, t.Expires, t.LastUsed})
		}
	})
	jsonOut(rw, map[string]any{"tokens": out})
}

func (w *Web) createToken(rw http.ResponseWriter, r *http.Request, p principal) {
	var req struct {
		Name string `json:"name"`
	}
	readJSON(r, &req)
	tok, err := w.h.CreateToken(strings.TrimSpace(req.Name))
	if err != nil {
		jsonErr(rw, http.StatusBadRequest, err.Error())
		return
	}
	jsonOut(rw, map[string]any{"token": tok, "command": "ember-fleet login " + w.base})
}

func (w *Web) revokeToken(rw http.ResponseWriter, r *http.Request, p principal) {
	if err := w.h.RevokeToken(r.PathValue("id")); err != nil {
		jsonErr(rw, http.StatusNotFound, err.Error())
		return
	}
	jsonOut(rw, map[string]any{"ok": true})
}

// Serve runs plain HTTP on ln (loopback, or behind TLS set up by the caller).
func (w *Web) Serve(ctx context.Context, srv *http.Server, ln net.Listener) error {
	srv.Handler = w.Handler()
	srv.ReadHeaderTimeout = 10 * time.Second
	srv.ErrorLog = log.New(io.Discard, "", 0)
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
