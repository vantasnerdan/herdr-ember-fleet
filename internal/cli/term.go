package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/coder/websocket"
	"golang.org/x/term"
)

// Term shows one remote pane in this terminal. spec is
// "machine[/user]/target", where target is a pane ID (w1:p2), an agent kind
// (codex) or part of the pane's title. By default it takes control at the
// pane's own size; fit resizes the pane to this terminal; watch only observes.
func Term(ctx context.Context, spec string, watch, fit, takeover bool) error {
	c, err := Load()
	if err != nil {
		return err
	}
	i := strings.LastIndex(spec, "/")
	if i <= 0 {
		return errors.New("use machine/pane, e.g. studio/w1:p2 or studio/codex")
	}
	machine, target := spec[:i], spec[i+1:]
	machines, err := c.Fleet(ctx)
	if err != nil {
		return err
	}
	d, err := findDock(machines, machine)
	if err != nil {
		return err
	}
	var sess Session
	var pane Pane
	found := 0
	for _, s := range sessions(d) {
		for _, p := range s.Snapshot.Panes {
			if p.PaneID == target || strings.EqualFold(p.Agent, target) ||
				(len(target) > 2 && strings.Contains(strings.ToLower(p.Title), strings.ToLower(target))) {
				sess, pane = s, p
				found++
			}
		}
	}
	switch {
	case found == 0:
		return fmt.Errorf("no pane %q on %s", target, machine)
	case found > 1:
		var ids []string
		for _, s := range sessions(d) {
			for _, p := range s.Snapshot.Panes {
				ids = append(ids, fmt.Sprintf("%s (%s %s)", p.PaneID, p.Agent, p.Title))
			}
		}
		return fmt.Errorf("%q matches several panes; use a pane ID: %s", target, strings.Join(ids, ", "))
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return errors.New("term needs an interactive terminal")
	}
	cols, rows, _ := term.GetSize(fd)
	mode := "control"
	if watch {
		mode = "observe"
	}
	q := url.Values{
		"dock": {d.ID}, "session": {sess.Name}, "epoch": {sess.Epoch}, "pane": {pane.PaneID},
		"terminal": {pane.TerminalID}, "mode": {mode},
	}
	if watch || fit {
		q.Set("cols", strconv.Itoa(cols))
		q.Set("rows", strconv.Itoa(rows))
	} else {
		q.Set("cols", "0")
		q.Set("rows", "0")
	}
	if takeover {
		q.Set("takeover", "1")
	}
	ws, err := c.dial(ctx, "/api/term", q)
	if err != nil {
		return err
	}
	defer ws.CloseNow()

	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	var once sync.Once
	restore := func() {
		once.Do(func() {
			term.Restore(fd, old)
			// leave the alternate screen, show the cursor, stop mouse reporting
			os.Stdout.WriteString("\x1b[?1049l\x1b[?25h\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[0m\r\n")
		})
	}
	defer restore()
	os.Stdout.WriteString("\x1b[?1049h\x1b[H\x1b[2J")

	tctx, cancel := context.WithCancel(ctx)
	defer cancel()
	send := func(v any) {
		b, _ := json.Marshal(v)
		ws.Write(tctx, websocket.MessageText, b)
	}

	// local resize: only follow it when asked (watch or --fit)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			if watch || fit {
				if c2, r2, err := term.GetSize(fd); err == nil {
					send(map[string]any{"type": "resize", "cols": c2, "rows": r2})
				}
			}
		}
	}()

	// keyboard -> remote; Ctrl-] detaches
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				cancel()
				return
			}
			b := buf[:n]
			if i := strings.IndexByte(string(b), 0x1d); i >= 0 {
				if i > 0 && !watch {
					send(map[string]any{"type": "input", "text": string(b[:i])})
				}
				if !watch {
					send(map[string]any{"type": "release"})
				}
				cancel()
				return
			}
			if !watch {
				send(map[string]any{"type": "input", "text": string(b)})
			}
		}
	}()

	var exitMsg string
	for {
		_, b, err := ws.Read(tctx)
		if err != nil {
			if tctx.Err() == nil {
				exitMsg = "connection lost; what you typed last may or may not have arrived"
			}
			break
		}
		var m struct {
			Type   string `json:"type"`
			Full   bool   `json:"full"`
			Data   string `json:"data"`
			State  string `json:"state"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		switch m.Type {
		case "frame":
			raw, err := base64.StdEncoding.DecodeString(m.Data)
			if err == nil {
				os.Stdout.Write(raw)
			}
		case "status":
			switch m.State {
			case "busy":
				exitMsg = "someone else is in control of that pane; use --takeover to take it"
			case "error", "closed":
				exitMsg = strings.TrimSpace(m.State + " " + m.Reason)
			}
			if m.State != "open" {
				cancel()
			}
		}
	}
	restore()
	if exitMsg != "" && exitMsg != "closed released" && exitMsg != "closed" {
		fmt.Fprintln(os.Stderr, "ember-fleet:", exitMsg)
	}
	return nil
}
