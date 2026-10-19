package dock

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/e2e"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/herdr"
)

var keyRE = regexp.MustCompile(`^[a-z0-9+_-]{1,24}$|^[[:print:]]$`)

type actionParams struct {
	PaneID      *string  `json:"pane_id"`
	Target      *string  `json:"target"`
	TabID       *string  `json:"tab_id"`
	WorkspaceID *string  `json:"workspace_id"`
	Text        *string  `json:"text"`
	Keys        []string `json:"keys"`
	Label       *string  `json:"label"`
	CloseGroup  *bool    `json:"close_group"`
}

// allowed actions: method -> permission and a builder that validates and
// re-encodes the parameters from known fields only.
var actions = map[string]struct {
	perm  string
	build func(p actionParams) (map[string]any, error)
}{
	"agent.prompt": {PermControl, func(p actionParams) (map[string]any, error) {
		pane := first(p.PaneID, p.Target)
		if !paneRE.MatchString(pane) || p.Text == nil || len(*p.Text) == 0 || len(*p.Text) > 100_000 {
			return nil, errBad
		}
		return map[string]any{"target": pane, "text": *p.Text}, nil
	}},
	"pane.send_keys": {PermControl, func(p actionParams) (map[string]any, error) {
		pane := first(p.PaneID, nil)
		if !paneRE.MatchString(pane) || len(p.Keys) == 0 || len(p.Keys) > 32 {
			return nil, errBad
		}
		for _, k := range p.Keys {
			if !keyRE.MatchString(k) || len(k) > 24 {
				return nil, errBad
			}
		}
		return map[string]any{"pane_id": pane, "keys": p.Keys}, nil
	}},
	"pane.send_text": {PermControl, func(p actionParams) (map[string]any, error) {
		pane := first(p.PaneID, nil)
		if !paneRE.MatchString(pane) || p.Text == nil || len(*p.Text) > 100_000 {
			return nil, errBad
		}
		return map[string]any{"pane_id": pane, "text": *p.Text}, nil
	}},
	"pane.rename": {PermManage, func(p actionParams) (map[string]any, error) {
		pane := first(p.PaneID, nil)
		if !paneRE.MatchString(pane) || (p.Label != nil && len(*p.Label) > 80) {
			return nil, errBad
		}
		var label any
		if p.Label != nil && *p.Label != "" {
			label = *p.Label
		}
		return map[string]any{"pane_id": pane, "label": label}, nil
	}},
	"tab.rename": {PermManage, func(p actionParams) (map[string]any, error) {
		tab := first(p.TabID, nil)
		if !tabRE.MatchString(tab) || p.Label == nil || len(*p.Label) > 80 {
			return nil, errBad
		}
		return map[string]any{"tab_id": tab, "label": *p.Label}, nil
	}},
	"workspace.rename": {PermManage, func(p actionParams) (map[string]any, error) {
		ws := first(p.WorkspaceID, nil)
		if !workspaceRE.MatchString(ws) || p.Label == nil || len(*p.Label) > 80 {
			return nil, errBad
		}
		return map[string]any{"workspace_id": ws, "label": *p.Label}, nil
	}},
	"pane.close": {PermManage, func(p actionParams) (map[string]any, error) {
		pane := first(p.PaneID, nil)
		if !paneRE.MatchString(pane) {
			return nil, errBad
		}
		return map[string]any{"pane_id": pane}, nil
	}},
	"tab.close": {PermManage, func(p actionParams) (map[string]any, error) {
		tab := first(p.TabID, nil)
		if !tabRE.MatchString(tab) {
			return nil, errBad
		}
		return map[string]any{"tab_id": tab}, nil
	}},
	"workspace.close": {PermManage, func(p actionParams) (map[string]any, error) {
		ws := first(p.WorkspaceID, nil)
		if !workspaceRE.MatchString(ws) {
			return nil, errBad
		}
		return map[string]any{"workspace_id": ws}, nil
	}},
}

var errBad = errors.New("bad parameters")

func first(a, b *string) string {
	if a != nil {
		return *a
	}
	if b != nil {
		return *b
	}
	return ""
}

// action runs one allow-listed herdr method against the server the hub last
// saw. If herdr restarted in between, it fails closed and nothing is sent.
func (d *Dock) action(ctx context.Context, c *chanCtx) {
	a, ok := actions[c.h.Method]
	if !ok {
		c.d.audit.log(c, "refused", "method not allowed")
		c.fail("not_allowed", "that action is not available through the fleet")
		return
	}
	if !c.need(a.perm) {
		return
	}
	if !herdr.ValidSession(c.h.Session) {
		c.fail("bad_target", "bad session")
		return
	}
	var p actionParams
	if len(c.h.Params) > 200_000 || json.Unmarshal(c.h.Params, &p) != nil {
		c.fail("bad_params", "bad parameters")
		return
	}
	params, err := a.build(p)
	if err != nil {
		c.fail("bad_params", err.Error())
		return
	}
	st, ok := d.sessions.Current(c.h.Session)
	if !ok || !st.Online {
		c.fail("offline", "that herdr session is not running")
		return
	}
	if c.h.Epoch == "" || c.h.Epoch != st.Epoch {
		c.fail("stale", "herdr restarted since you last saw it; nothing was sent")
		return
	}

	actx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := herdr.Dial(actx, c.h.Session)
	if err != nil {
		c.fail("offline", "herdr is not reachable; nothing was sent")
		return
	}
	defer conn.Close()
	// Check the server on *this* connection, just before sending.
	if conn.Peer.Epoch() != c.h.Epoch {
		d.sessions.Poke(c.h.Session)
		c.fail("stale", "herdr restarted since you last saw it; nothing was sent")
		return
	}
	if _, err := conn.Request(c.h.Method, params); err != nil {
		d.audit.log(c, "action", "outcome unknown: "+err.Error())
		e2e.WriteJSON(c.conn, e2e.Reply{Code: "unknown", Error: "the connection to herdr broke while sending", Outcome: "unknown"})
		return
	}
	res, herr, err := conn.Read(30 * time.Second)
	d.sessions.Poke(c.h.Session)
	switch {
	case err != nil:
		d.audit.log(c, "action", "outcome unknown")
		e2e.WriteJSON(c.conn, e2e.Reply{Code: "unknown", Error: "sent, but herdr's answer did not arrive", Outcome: "unknown"})
	case herr != nil:
		d.audit.log(c, "action", "herdr refused: "+herr.Code)
		e2e.WriteJSON(c.conn, e2e.Reply{Code: herr.Code, Error: herr.Message, Outcome: "not_sent"})
	default:
		d.audit.log(c, "action", "ok")
		if len(res) > 1<<20 {
			res = json.RawMessage(`{}`)
		}
		e2e.WriteJSON(c.conn, e2e.Reply{OK: true, Result: res})
	}
}

// endpoint joins the herdr client bridge to this channel, for `attach`.
func (d *Dock) endpoint(ctx context.Context, c *chanCtx) {
	if !herdr.ValidSession(c.h.Session) {
		c.fail("bad_target", "bad session")
		return
	}
	ectx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, err := herdr.Command(ectx, c.h.Session, "remote-client-bridge")
	if err != nil {
		c.fail("no_herdr", err.Error())
		return
	}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		c.fail("no_herdr", err.Error())
		return
	}
	if c.ok() != nil {
		cancel()
		cmd.Wait()
		return
	}
	d.audit.log(c, "attach", "")
	defer d.audit.log(c, "detach", "")
	done := make(chan struct{}, 2)
	go func() { // hub -> herdr
		io.Copy(stdin, c.r)
		stdin.Close()
		done <- struct{}{}
	}()
	go func() { // herdr -> hub
		io.Copy(c.conn, stdout)
		c.conn.CloseWrite()
		done <- struct{}{}
	}()
	<-done
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	cancel()
	cmd.Wait()
}
