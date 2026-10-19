package hub

import (
	"encoding/json"
	"time"
)

// When each agent last changed state (working, idle, blocked, done), for the
// page's "last activity". Kept in memory: after a hub restart an agent's time
// is unknown until its state next changes.

type paneSince struct {
	epoch    string // the herdr server
	terminal string // the process in the pane
	status   string
	at       int64 // unix ms; 0 = state seen but not when it began
}

// noteStatuses records state changes in a session snapshot line. Called with h.mu held.
func (d *dockLive) noteStatuses(session string, line json.RawMessage) {
	var m struct {
		Epoch    string `json:"epoch"`
		Snapshot struct {
			Panes []struct {
				PaneID   string `json:"pane_id"`
				Terminal string `json:"terminal_id"`
				Status   string `json:"agent_status"`
			} `json:"panes"`
		} `json:"snapshot"`
	}
	if json.Unmarshal(line, &m) != nil {
		return
	}
	if d.since == nil {
		d.since = map[string]map[string]paneSince{}
	}
	prev := d.since[session]
	next := make(map[string]paneSince, len(m.Snapshot.Panes))
	now := time.Now().UnixMilli()
	for _, p := range m.Snapshot.Panes {
		old, seen := prev[p.PaneID]
		// A pane ID can come back on a new server or with a new process in it: that is a
		// different agent, whose start we didn't see.
		same := seen && old.epoch == m.Epoch && old.terminal == p.Terminal
		switch {
		case !same:
			next[p.PaneID] = paneSince{epoch: m.Epoch, terminal: p.Terminal, status: p.Status}
		case old.status != p.Status:
			next[p.PaneID] = paneSince{epoch: m.Epoch, terminal: p.Terminal, status: p.Status, at: now}
		default:
			next[p.PaneID] = old
		}
	}
	d.since[session] = next
}

// keepSessions forgets history for sessions not in a fresh, complete set. Called with h.mu held.
func (d *dockLive) keepSessions(fresh map[string]json.RawMessage) {
	for name := range d.since {
		if _, ok := fresh[name]; !ok {
			delete(d.since, name)
		}
	}
}

// sinceView is pane → when its current state began, for the panes where that is known.
func sinceView(s map[string]paneSince) map[string]int64 {
	out := map[string]int64{}
	for id, p := range s {
		if p.at > 0 {
			out[id] = p.at
		}
	}
	return out
}
