package hub

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// A sign-in PIN: typed on the sign-in page, it signs in any browser that
// reaches the hub, and it doesn't expire. It is a second lock behind whatever
// guards the address (an SSO proxy), so it is short enough to remember, and
// wrong guesses are rate-limited for the whole hub. Stored as a slow hash.

const (
	pinMin       = 6
	pinMax       = 64
	pinFailShort = 5  // wrong PINs allowed per 15 minutes
	pinFailDay   = 20 // and per day
)

// PINRec is the stored PIN.
type PINRec struct {
	Hash string `json:"hash"`
	Salt string `json:"salt"`
	Set  int64  `json:"set"`
}

// normalPIN drops spaces and dashes and ignores case, so "4821-7730",
// "4821 7730" and "48217730" are the same PIN.
func normalPIN(pin string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(pin) {
		if r == ' ' || r == '-' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func checkPINShape(pin string) error {
	if len(pin) < pinMin || len(pin) > pinMax {
		return fmt.Errorf("use %d to %d letters or digits", pinMin, pinMax)
	}
	for _, r := range pin {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z') {
			return errors.New("use only letters and digits")
		}
	}
	return nil
}

// newPIN makes eight random digits, shown as 4821-7730.
func newPIN() string {
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		panic(err)
	}
	s := fmt.Sprintf("%08d", n.Int64())
	return s[:4] + "-" + s[4:]
}

func pinHash(pin string, salt []byte) []byte {
	return argon2.IDKey([]byte("ember-pin-v1\x00"+pin), salt, 2, 32*1024, 2, 32)
}

// SetPIN stores a PIN; "" makes a new random one. It returns the PIN as typed.
func (h *Hub) SetPIN(pin string) (string, error) {
	if strings.TrimSpace(pin) == "" {
		pin = newPIN()
	}
	norm := normalPIN(pin)
	if err := checkPINShape(norm); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	rec := &PINRec{
		Hash: base64.RawStdEncoding.EncodeToString(pinHash(norm, salt)),
		Salt: base64.RawStdEncoding.EncodeToString(salt),
		Set:  time.Now().UnixMilli(),
	}
	if err := h.store.Update(func(s *State) error { s.SigninPIN = rec; return nil }); err != nil {
		return "", err
	}
	h.pinMu.Lock()
	h.pinFails = nil // a new PIN starts with a clean slate
	h.pinMu.Unlock()
	log.Printf("sign-in PIN changed")
	return pin, nil
}

// RemovePIN turns PIN sign-in off.
func (h *Hub) RemovePIN() error {
	err := h.store.Update(func(s *State) error { s.SigninPIN = nil; return nil })
	if err == nil {
		log.Printf("sign-in PIN removed")
	}
	return err
}

// PINSet says whether there is a PIN, and since when.
func (h *Hub) PINSet() (bool, int64) {
	var rec *PINRec
	h.store.View(func(s *State) { rec = s.SigninPIN })
	if rec == nil {
		return false, 0
	}
	return true, rec.Set
}

var errPINWrong = errors.New("that PIN isn't right")

// checkPIN compares a typed PIN, one at a time, with the rate limit applied
// before the comparison.
func (h *Hub) checkPIN(pin string) error {
	h.pinMu.Lock()
	defer h.pinMu.Unlock()
	now := time.Now()
	kept := h.pinFails[:0]
	short := 0
	for _, t := range h.pinFails {
		if now.Sub(t) < 24*time.Hour {
			kept = append(kept, t)
			if now.Sub(t) < 15*time.Minute {
				short++
			}
		}
	}
	h.pinFails = kept
	if short >= pinFailShort {
		wait := 15*time.Minute - now.Sub(h.pinFails[len(h.pinFails)-pinFailShort])
		return &pinLocked{wait}
	}
	if len(h.pinFails) >= pinFailDay {
		return &pinLocked{24*time.Hour - now.Sub(h.pinFails[0])}
	}
	var rec *PINRec
	h.store.View(func(s *State) { rec = s.SigninPIN })
	if rec == nil {
		return errors.New("PIN sign-in is off on this hub")
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(rec.Salt)
	want, err2 := base64.RawStdEncoding.DecodeString(rec.Hash)
	if err1 != nil || err2 != nil {
		return errors.New("the stored PIN is damaged; set a new one")
	}
	if subtle.ConstantTimeCompare(pinHash(normalPIN(pin), salt), want) != 1 {
		h.pinFails = append(h.pinFails, now)
		log.Printf("wrong sign-in PIN (%d in the last 15 minutes)", short+1)
		return errPINWrong
	}
	return nil
}

type pinLocked struct{ wait time.Duration }

func (e *pinLocked) Error() string {
	m := int(e.wait.Minutes()) + 1
	if m > 90 {
		return fmt.Sprintf("too many wrong PINs; PIN sign-in is paused for about %d hours", m/60+1)
	}
	return fmt.Sprintf("too many wrong PINs; try again in %d minutes", m)
}

// --- signed-in browsers ---------------------------------------------------------

// SessionInfo is a signed-in browser as the settings page lists it.
type SessionInfo struct {
	ID      string `json:"id"`
	Agent   string `json:"agent"`
	Via     string `json:"via,omitempty"` // pin, approval, link
	Created int64  `json:"created"`
	Seen    int64  `json:"seen,omitempty"`
	Current bool   `json:"current"`
}

func sessionID(hash string) string { return hash[:16] }

// BrowserSessions lists signed-in browsers, newest first.
func (h *Hub) BrowserSessions(current string) []SessionInfo {
	out := []SessionInfo{}
	now := time.Now().UnixMilli()
	h.store.View(func(s *State) {
		for hash, bs := range s.Sessions {
			if bs.Expires < now {
				continue
			}
			out = append(out, SessionInfo{ID: sessionID(hash), Agent: bs.Agent, Via: bs.Via, Created: bs.Created,
				Seen: bs.Seen, Current: "session:"+hash == current})
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out
}

// EndSessionID signs out one browser by its listed ID and cuts its sockets.
func (h *Hub) EndSessionID(id string) error {
	var hash string
	err := h.store.Update(func(s *State) error {
		for k := range s.Sessions {
			if len(id) == 16 && sessionID(k) == id {
				hash = k
			}
		}
		if hash == "" {
			return errors.New("that browser is already signed out")
		}
		delete(s.Sessions, hash)
		return nil
	})
	if err != nil {
		return err
	}
	h.cutCredential("session:" + hash)
	return nil
}

// --- HTTP ---------------------------------------------------------------------

func (w *Web) pinSignin(rw http.ResponseWriter, r *http.Request) {
	if !w.originOK(r) {
		jsonErr(rw, http.StatusForbidden, "bad origin")
		return
	}
	var req struct {
		PIN string `json:"pin"`
	}
	if readJSON(r, &req) != nil || strings.TrimSpace(req.PIN) == "" || len(req.PIN) > 200 {
		jsonErr(rw, http.StatusBadRequest, "type your PIN")
		return
	}
	if err := w.h.checkPIN(req.PIN); err != nil {
		var locked *pinLocked
		switch {
		case errors.As(err, &locked):
			jsonErr(rw, http.StatusTooManyRequests, err.Error())
		case errors.Is(err, errPINWrong):
			jsonErr(rw, http.StatusUnauthorized, err.Error())
		default:
			jsonErr(rw, http.StatusForbidden, err.Error())
		}
		return
	}
	agent := browserName(r.UserAgent())
	cookie, csrf, err := w.h.startSession(agent, "pin")
	if err != nil {
		jsonErr(rw, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("signed in with the PIN: %s", agent)
	w.setSessionCookie(rw, r, cookie, sessionLife)
	jsonOut(rw, map[string]any{"ok": true, "csrf": csrf})
}

func (w *Web) getPIN(rw http.ResponseWriter, r *http.Request, p principal) {
	set, at := w.h.PINSet()
	jsonOut(rw, map[string]any{"set": set, "at": at})
}

func (w *Web) setPIN(rw http.ResponseWriter, r *http.Request, p principal) {
	var req struct {
		PIN string `json:"pin"`
	}
	if readJSON(r, &req) != nil {
		jsonErr(rw, http.StatusBadRequest, "bad request")
		return
	}
	pin, err := w.h.SetPIN(req.PIN)
	if err != nil {
		jsonErr(rw, http.StatusBadRequest, err.Error())
		return
	}
	_, at := w.h.PINSet()
	out := map[string]any{"ok": true, "set": true, "at": at}
	if strings.TrimSpace(req.PIN) == "" {
		out["pin"] = pin // made here: show it once
	}
	jsonOut(rw, out)
}

func (w *Web) deletePIN(rw http.ResponseWriter, r *http.Request, p principal) {
	if err := w.h.RemovePIN(); err != nil {
		jsonErr(rw, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOut(rw, map[string]any{"ok": true, "set": false})
}

func (w *Web) listSessions(rw http.ResponseWriter, r *http.Request, p principal) {
	jsonOut(rw, map[string]any{"sessions": w.h.BrowserSessions(p.cred)})
}

func (w *Web) endSessionID(rw http.ResponseWriter, r *http.Request, p principal) {
	if err := w.h.EndSessionID(r.PathValue("id")); err != nil {
		jsonErr(rw, http.StatusNotFound, err.Error())
		return
	}
	jsonOut(rw, map[string]any{"ok": true})
}
