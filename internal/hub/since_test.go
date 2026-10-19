package hub

import (
	"encoding/json"
	"testing"
)

func TestNewTerminalStartsUnknown(t *testing.T) {
	d := &dockLive{}
	line := func(epoch, terminal, status string) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"name": "default", "epoch": epoch, "snapshot": map[string]any{
			"panes": []any{map[string]any{"pane_id": "w1:p1", "terminal_id": terminal, "agent_status": status}}}})
		return b
	}
	d.noteStatuses("default", line("e1", "t1", "idle"))
	d.noteStatuses("default", line("e1", "t1", "working"))
	if d.since["default"]["w1:p1"].at == 0 {
		t.Fatal("a change wasn't timed")
	}
	d.noteStatuses("default", line("e1", "t2", "working")) // a new process in the same pane
	if at := d.since["default"]["w1:p1"].at; at != 0 {
		t.Fatalf("a new terminal inherited the old one's time %d", at)
	}
	d.noteStatuses("default", line("e1", "t2", "idle"))
	d.noteStatuses("default", line("e2", "t2", "idle")) // the same IDs on a new herdr server
	if at := d.since["default"]["w1:p1"].at; at != 0 {
		t.Fatalf("a new server inherited the old one's time %d", at)
	}
	d.noteStatuses("other", line("e2", "t9", "idle"))
	d.keepSessions(map[string]json.RawMessage{"default": nil})
	if _, ok := d.since["other"]; ok {
		t.Fatal("history of a session that's gone survived a full resync")
	}
}

func TestStateChangeTimes(t *testing.T) {
	d := &dockLive{}
	line := func(status string) json.RawMessage {
		return json.RawMessage(`{"type":"session","snapshot":{"panes":[{"pane_id":"w1:p1","agent_status":"` + status + `"}]}}`)
	}
	d.noteStatuses("default", line("working"))
	if got := sinceView(d.since["default"]); len(got) != 0 {
		t.Fatalf("first sight claimed a start time: %v", got)
	}
	d.noteStatuses("default", line("working"))
	if got := sinceView(d.since["default"]); len(got) != 0 {
		t.Fatalf("no change, yet a time: %v", got)
	}
	d.noteStatuses("default", line("idle"))
	at := sinceView(d.since["default"])["w1:p1"]
	if at == 0 {
		t.Fatal("a change to idle wasn't timed")
	}
	d.noteStatuses("default", line("idle"))
	if sinceView(d.since["default"])["w1:p1"] != at {
		t.Fatal("the time moved without a change")
	}
	var v struct {
		Since map[string]int64 `json:"since"`
	}
	json.Unmarshal(sessionView(line("idle"), true, d.since["default"]), &v)
	if v.Since["w1:p1"] != at {
		t.Fatalf("the page doesn't get the time: %+v", v)
	}
}
