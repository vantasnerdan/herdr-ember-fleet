package dock

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/e2e"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/herdr"
)

var (
	paneRE      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}:p[0-9]{1,6}$`)
	tabRE       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}:t[0-9]{1,6}$`)
	workspaceRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	terminalRE  = regexp.MustCompile(`^term_[A-Za-z0-9]{1,64}$`)
)

// target checks that a channel names a pane of the server the hub last saw
// (same epoch), and returns that pane's terminal ID. Terminal IDs are not
// reused by a restarted server, so a stale request can never reach a new
// server's pane that happens to have the same pane ID.
func (d *Dock) target(c *chanCtx) (paneInfo, bool) {
	h := c.h
	if !herdr.ValidSession(h.Session) || !paneRE.MatchString(h.Pane) {
		c.fail("bad_target", "bad session or pane")
		return paneInfo{}, false
	}
	st, ok := d.sessions.Current(h.Session)
	if !ok || !st.Online {
		c.fail("offline", "that herdr session is not running")
		return paneInfo{}, false
	}
	if h.Epoch == "" || h.Epoch != st.Epoch {
		c.fail("stale", "herdr restarted since this view was opened")
		return paneInfo{}, false
	}
	info, ok := st.index[h.Pane]
	if !ok {
		c.fail("gone", "that pane no longer exists")
		return paneInfo{}, false
	}
	if c.h.Terminal == "" || c.h.Terminal != info.Terminal {
		c.fail("stale", "that pane now holds a different terminal")
		return paneInfo{}, false
	}
	if !terminalRE.MatchString(info.Terminal) {
		c.fail("bad_target", "pane has no terminal")
		return paneInfo{}, false
	}
	return info, true
}

func clampSize(cols, rows int) (int, int) {
	return min(max(cols, 10), 500), min(max(rows, 3), 200)
}

// terminal serves an observe or control channel through the installed
// `herdr terminal session observe|control`.
func (d *Dock) terminal(ctx context.Context, c *chanCtx, control bool) {
	info, ok := d.target(c)
	if !ok {
		return
	}
	cols, rows := c.h.Cols, c.h.Rows
	if control && (cols <= 0 || rows <= 0) {
		cols, rows = info.Cols, info.Rows // take control at the pane's current size
	}
	if cols <= 0 || rows <= 0 {
		cols, rows = 100, 30
	}
	cols, rows = clampSize(cols, rows)
	if c.ok() != nil {
		return
	}
	if control {
		d.audit.log(c, "control", map[bool]string{true: "takeover", false: ""}[c.h.Takeover])
		defer d.audit.log(c, "released", "")
	} else {
		d.audit.log(c, "observe", "")
	}

	var wmu sync.Mutex
	send := func(line []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		_, err := c.conn.Write(append(line, '\n'))
		return err
	}
	status := func(state, reason string) {
		b, _ := json.Marshal(map[string]string{"type": "terminal.status", "state": state, "reason": reason})
		send(b)
	}

	tctx, cancel := context.WithCancel(ctx)
	defer cancel()
	inputs := make(chan []byte, 64)
	inputErr := make(chan struct{})
	go func() { // hub -> dock lines
		defer close(inputErr)
		for {
			line, err := e2e.ReadLine(c.r)
			if err != nil {
				return
			}
			select {
			case inputs <- line:
			case <-tctx.Done():
				return
			}
		}
	}()

	for {
		args := []string{"terminal", "session", "observe", info.Terminal, "--cols", strconv.Itoa(cols), "--rows", strconv.Itoa(rows)}
		if control {
			args = []string{"terminal", "session", "control", info.Terminal, "--cols", strconv.Itoa(cols), "--rows", strconv.Itoa(rows)}
			if c.h.Takeover {
				args = append(args, "--takeover")
			}
		}
		pctx, pcancel := context.WithCancel(tctx)
		cmd, err := herdr.Command(pctx, c.h.Session, args...)
		if err != nil {
			pcancel()
			status("error", err.Error())
			return
		}
		var stdin io.WriteCloser
		if control {
			stdin, _ = cmd.StdinPipe()
		}
		stdout, _ := cmd.StdoutPipe()
		var stderr bytes.Buffer
		cmd.Stderr = &limitWriter{w: &stderr, n: 4096}
		if err := cmd.Start(); err != nil {
			pcancel()
			status("error", err.Error())
			return
		}
		frames := make(chan struct{})
		var closedReason string
		go func() { // herdr -> hub frames, passed through as-is
			defer close(frames)
			r := bufio.NewReaderSize(stdout, 256<<10)
			for {
				line, err := e2e.ReadLine(r)
				if err != nil {
					return
				}
				if len(line) < 8192 && bytes.Contains(line, []byte(`"terminal.closed"`)) {
					// herdr says why it stopped; we send one final status instead
					var m struct{ Type, Reason string }
					if json.Unmarshal(line, &m) == nil && m.Type == "terminal.closed" {
						closedReason = m.Reason
						continue
					}
				}
				if send(line) != nil {
					pcancel()
					return
				}
			}
		}()

		restart := false
		released := false
	loop:
		for {
			select {
			case <-frames:
				break loop
			case <-inputErr:
				// The hub went away: release control (EOF on stdin) and stop.
				if stdin != nil {
					stdin.Close()
				}
				select {
				case <-frames:
				case <-time.After(3 * time.Second):
				}
				break loop
			case line := <-inputs:
				msg, kind, ok := filterInput(line, control)
				if !ok {
					continue
				}
				if !control && kind == "terminal.resize" {
					// Observers render at the viewer's size: restart at the new size.
					var r struct{ Cols, Rows int }
					json.Unmarshal(msg, &r)
					cols, rows = clampSize(r.Cols, r.Rows)
					restart = true
					break loop
				}
				if control && stdin != nil {
					if _, err := stdin.Write(append(msg, '\n')); err != nil {
						break loop
					}
					if kind == "terminal.release" {
						released = true
						stdin.Close()
						stdin = nil
					}
				}
			}
		}
		pcancel()
		cmd.Wait()
		<-frames
		if restart {
			continue
		}
		reason := strings.TrimSpace(closedReason + "\n" + stderr.String())
		switch {
		case released:
			status("closed", "released")
		case strings.Contains(reason, "already has an attached client") || strings.Contains(reason, "--takeover"):
			status("busy", "someone else is in control of this pane")
		case strings.Contains(reason, "taken over"):
			status("closed", "someone else took control of this pane")
		case reason != "":
			status("closed", firstLine(reason))
		default:
			status("closed", "")
		}
		return
	}
}

// filterInput accepts only the terminal messages herdr defines, re-encoded
// from known fields, so nothing else reaches herdr's stdin.
func filterInput(line []byte, control bool) ([]byte, string, bool) {
	var m struct {
		Type         string  `json:"type"`
		Text         *string `json:"text"`
		Bytes        *string `json:"bytes"`
		Cols         int     `json:"cols"`
		Rows         int     `json:"rows"`
		Direction    string  `json:"direction"`
		Lines        int     `json:"lines"`
		Source       string  `json:"source"`
		Column       *int    `json:"column"`
		Row          *int    `json:"row"`
		Modifiers    int     `json:"modifiers"`
		Action       string  `json:"action"`
		Button       string  `json:"button"`
		CellWidthPx  int     `json:"cell_width_px"`
		CellHeightPx int     `json:"cell_height_px"`
	}
	if len(line) > 1<<20 || json.Unmarshal(line, &m) != nil {
		return nil, "", false
	}
	var out map[string]any
	switch m.Type {
	case "terminal.resize":
		if m.Cols <= 0 || m.Rows <= 0 {
			return nil, "", false
		}
		c, r := clampSize(m.Cols, m.Rows)
		out = map[string]any{"type": m.Type, "cols": c, "rows": r}
	case "terminal.input":
		if !control {
			return nil, "", false
		}
		switch {
		case m.Text != nil && m.Bytes == nil:
			out = map[string]any{"type": m.Type, "text": *m.Text}
		case m.Bytes != nil && m.Text == nil:
			out = map[string]any{"type": m.Type, "bytes": *m.Bytes}
		default:
			return nil, "", false
		}
	case "terminal.scroll":
		if !control || (m.Direction != "up" && m.Direction != "down") || m.Lines <= 0 {
			return nil, "", false
		}
		out = map[string]any{"type": m.Type, "direction": m.Direction, "lines": min(m.Lines, 500)}
		if m.Source == "page_key" {
			out["source"] = "page_key"
		}
	case "terminal.mouse":
		if !control || m.Column == nil || m.Row == nil {
			return nil, "", false
		}
		switch m.Action {
		case "down", "up", "drag", "move":
		default:
			return nil, "", false
		}
		btn := m.Button
		if btn != "right" && btn != "middle" {
			btn = "left"
		}
		out = map[string]any{"type": m.Type, "action": m.Action, "button": btn, "column": *m.Column, "row": *m.Row, "modifiers": m.Modifiers & 0xff}
	case "terminal.release":
		if !control {
			return nil, "", false
		}
		out = map[string]any{"type": m.Type}
	default:
		return nil, "", false
	}
	b, _ := json.Marshal(out)
	return b, m.Type, true
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	s = strings.TrimPrefix(s, "Error: ")
	return trunc(s, 200)
}

type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}
