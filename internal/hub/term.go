package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/e2e"
)

// termWS joins a page's terminal view to a dock's observe or control channel.
func (w *Web) termWS(rw http.ResponseWriter, r *http.Request, p principal) {
	q := r.URL.Query()
	mode := q.Get("mode")
	if mode != "observe" && mode != "control" {
		jsonErr(rw, http.StatusBadRequest, "mode must be observe or control")
		return
	}
	cols, _ := strconv.Atoi(q.Get("cols"))
	rows, _ := strconv.Atoi(q.Get("rows"))
	hdr := e2e.Header{
		Kind:     map[string]string{"observe": e2e.KindObserve, "control": e2e.KindControl}[mode],
		Operator: p.name,
		Session:  q.Get("session"),
		Pane:     q.Get("pane"),
		Terminal: q.Get("terminal"),
		Epoch:    q.Get("epoch"),
		Cols:     cols,
		Rows:     rows,
		Takeover: q.Get("takeover") == "1",
	}
	dock := q.Get("dock")

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
	status := func(state, reason string) {
		b, _ := json.Marshal(map[string]string{"type": "status", "state": state, "reason": reason})
		wctx, wc := context.WithTimeout(ctx, 5*time.Second)
		c.Write(wctx, websocket.MessageText, b)
		wc()
	}

	t, br, rep, cut, err := w.h.openPaired(ctx, dock, hdr)
	if err != nil {
		status("error", err.Error())
		c.Close(websocket.StatusNormalClosure, "")
		return
	}
	defer cut()
	defer t.Close()
	if !rep.OK {
		reason := rep.Error
		if rep.Code == "stale" || rep.Code == "gone" {
			reason = rep.Code
		}
		status("error", reason)
		c.Close(websocket.StatusNormalClosure, "")
		return
	}
	status("open", "")

	// dock -> page
	done := make(chan struct{})
	go func() {
		defer close(done)
		final := false
		for {
			line, err := e2e.ReadLine(br)
			if err != nil {
				if !final {
					status("closed", "connection lost")
				}
				return
			}
			out, ok := pageFrame(line)
			if !ok {
				continue
			}
			if bytes.Contains(out, []byte(`"type":"status"`)) {
				final = true // the dock's last word on this terminal
			}
			wctx, wc := context.WithTimeout(ctx, 30*time.Second)
			err = c.Write(wctx, websocket.MessageText, out)
			wc()
			if err != nil {
				return
			}
		}
	}()

	// page -> dock
	go func() {
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				t.CloseWrite() // the dock releases control when we stop
				return
			}
			if msg, ok := dockInput(b); ok {
				if _, err := t.Write(append(msg, '\n')); err != nil {
					return
				}
			}
		}
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
	c.Close(websocket.StatusNormalClosure, "")
}

// pageFrame turns herdr's terminal lines into the page's messages without
// decoding the screen bytes.
func pageFrame(line []byte) ([]byte, bool) {
	var m struct {
		Type   string          `json:"type"`
		Seq    uint64          `json:"seq"`
		Full   bool            `json:"full"`
		Width  int             `json:"width"`
		Height int             `json:"height"`
		Bytes  json.RawMessage `json:"bytes"`
		State  string          `json:"state"`
		Reason string          `json:"reason"`
	}
	if json.Unmarshal(line, &m) != nil {
		return nil, false
	}
	switch m.Type {
	case "terminal.frame":
		b, _ := json.Marshal(struct {
			Type   string          `json:"type"`
			Seq    uint64          `json:"seq"`
			Full   bool            `json:"full"`
			Width  int             `json:"width"`
			Height int             `json:"height"`
			Data   json.RawMessage `json:"data"`
		}{"frame", m.Seq, m.Full, m.Width, m.Height, m.Bytes})
		return b, true
	case "terminal.status":
		b, _ := json.Marshal(map[string]string{"type": "status", "state": m.State, "reason": m.Reason})
		return b, true
	case "terminal.closed":
		b, _ := json.Marshal(map[string]string{"type": "status", "state": "closed", "reason": m.Reason})
		return b, true
	}
	return nil, false
}

// dockInput maps the page's messages to herdr's terminal messages. The dock
// filters them again.
func dockInput(b []byte) ([]byte, bool) {
	var m struct {
		Type      string `json:"type"`
		Text      string `json:"text"`
		Cols      int    `json:"cols"`
		Rows      int    `json:"rows"`
		Direction string `json:"direction"`
		Lines     int    `json:"lines"`
	}
	if json.Unmarshal(b, &m) != nil {
		return nil, false
	}
	var out any
	switch m.Type {
	case "input":
		out = map[string]any{"type": "terminal.input", "text": m.Text}
	case "resize":
		out = map[string]any{"type": "terminal.resize", "cols": m.Cols, "rows": m.Rows}
	case "scroll":
		out = map[string]any{"type": "terminal.scroll", "direction": m.Direction, "lines": m.Lines}
	case "release":
		out = map[string]any{"type": "terminal.release"}
	default:
		return nil, false
	}
	r, _ := json.Marshal(out)
	return r, true
}

// action runs one allow-listed herdr method on a dock and reports honestly
// whether it was sent.
func (w *Web) action(rw http.ResponseWriter, r *http.Request, p principal) {
	var req struct {
		Dock    string          `json:"dock"`
		Session string          `json:"session"`
		Epoch   string          `json:"epoch"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if readJSON(r, &req) != nil {
		jsonErr(rw, http.StatusBadRequest, "bad request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	hdr := e2e.Header{Kind: e2e.KindAction, Operator: p.name, Session: req.Session, Epoch: req.Epoch, Method: req.Method, Params: req.Params}
	t, _, rep, cut, err := w.h.openPaired(ctx, req.Dock, hdr)
	if err != nil {
		outcome := "not_sent"
		if errors.Is(err, errSentNoReply) {
			outcome = "unknown"
		}
		jsonOut(rw, map[string]any{"ok": false, "error": err.Error(), "outcome": outcome})
		return
	}
	defer cut()
	defer t.Close()
	if !rep.OK {
		jsonOut(rw, map[string]any{"ok": false, "error": rep.Error, "code": rep.Code, "outcome": nz(rep.Outcome, "not_sent")})
		return
	}
	jsonOut(rw, map[string]any{"ok": true, "result": rep.Result})
}

func nz(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// endpointWS carries `attach`: raw herdr client bytes as binary messages.
// Owner-only, enforced by the dock (endpoint.attach).
func (w *Web) endpointWS(rw http.ResponseWriter, r *http.Request, p principal) {
	q := r.URL.Query()
	session := q.Get("session")
	if session == "" {
		session = "default"
	}
	c, err := w.acceptWS(rw, r, p)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	ctx, cancel, err := w.h.credContext(r.Context(), p)
	if err != nil {
		c.Close(websocket.StatusPolicyViolation, "signed out")
		return
	}
	defer cancel()
	t, br, rep, cut, err := w.h.openPaired(ctx, q.Get("dock"), e2e.Header{Kind: e2e.KindEndpoint, Operator: p.name, Session: session})
	if err != nil {
		c.Close(websocket.StatusTryAgainLater, trunc(err.Error(), 100))
		return
	}
	defer cut()
	defer t.Close()
	if !rep.OK {
		c.Close(websocket.StatusPolicyViolation, trunc(rep.Error, 100))
		return
	}
	done := make(chan struct{})
	go func() { // dock -> tool
		defer close(done)
		buf := make([]byte, 64<<10)
		for {
			n, err := br.Read(buf)
			if n > 0 {
				if c.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { // tool -> dock
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				t.CloseWrite()
				return
			}
			if _, err := t.Write(b); err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
		c.Close(websocket.StatusNormalClosure, "")
	case <-ctx.Done():
	}
}

func trunc(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) > n {
		return s[:n]
	}
	return s
}
