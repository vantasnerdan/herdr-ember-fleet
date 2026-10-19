package dock

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFilterInput(t *testing.T) {
	cases := []struct {
		in      string
		control bool
		ok      bool
		want    string
	}{
		{`{"type":"terminal.input","text":"ls\r"}`, true, true, `{"text":"ls\r","type":"terminal.input"}`},
		{`{"type":"terminal.input","text":"ls\r"}`, false, false, ""}, // observers cannot type
		{`{"type":"terminal.input","text":"a","bytes":"YQ=="}`, true, false, ""},
		{`{"type":"terminal.resize","cols":99999,"rows":0}`, true, false, ""},
		{`{"type":"terminal.resize","cols":99999,"rows":5}`, true, true, `{"cols":500,"rows":5,"type":"terminal.resize"}`},
		{`{"type":"terminal.scroll","direction":"sideways","lines":3}`, true, false, ""},
		{`{"type":"terminal.release","extra":"x"}`, true, true, `{"type":"terminal.release"}`},
		{`{"type":"terminal.release"}`, false, false, ""},
		{`{"type":"pane.send_text","text":"x"}`, true, false, ""},
		{`not json`, true, false, ""},
	}
	for _, c := range cases {
		got, _, ok := filterInput([]byte(c.in), c.control)
		if ok != c.ok || (ok && string(got) != c.want) {
			t.Errorf("filterInput(%s, control=%v) = %s, %v; want %s, %v", c.in, c.control, got, ok, c.want, c.ok)
		}
	}
}

func TestActionsAllowList(t *testing.T) {
	for _, m := range []string{"server.stop", "server.live_handoff", "integration.install", "plugin.link", "client.window_title.set", "pane.split", "events.subscribe"} {
		if _, ok := actions[m]; ok {
			t.Errorf("%s must not be allowed", m)
		}
	}
	build := func(method, params string) (map[string]any, error) {
		var p actionParams
		if err := json.Unmarshal([]byte(params), &p); err != nil {
			t.Fatal(err)
		}
		return actions[method].build(p)
	}
	if out, err := build("agent.prompt", `{"pane_id":"w1:p2","text":"hi","wait":{"until":"idle"},"extra":1}`); err != nil || out["target"] != "w1:p2" || len(out) != 2 {
		t.Errorf("agent.prompt = %v %v", out, err)
	}
	for _, bad := range []string{`{"pane_id":"w1:p2; rm","text":"x"}`, `{"pane_id":"../x","text":"x"}`, `{"pane_id":"w1:p2","text":""}`} {
		if _, err := build("agent.prompt", bad); err == nil {
			t.Errorf("agent.prompt accepted %s", bad)
		}
	}
	if _, err := build("pane.send_keys", `{"pane_id":"w1:p1","keys":["ctrl+c","esc","enter"]}`); err != nil {
		t.Errorf("send_keys: %v", err)
	}
	if _, err := build("pane.send_keys", `{"pane_id":"w1:p1","keys":["`+strings.Repeat("a", 40)+`"]}`); err == nil {
		t.Error("send_keys accepted a long key")
	}
	if _, err := build("pane.rename", `{"pane_id":"w1:p1","label":"`+strings.Repeat("x", 81)+`"}`); err == nil {
		t.Error("rename accepted a long label")
	}
	for _, m := range []string{"agent.prompt", "pane.send_keys", "pane.send_text"} {
		if actions[m].perm != PermControl {
			t.Errorf("%s should need terminal.control", m)
		}
	}
	for _, m := range []string{"pane.close", "tab.close", "workspace.close", "pane.rename"} {
		if actions[m].perm != PermManage {
			t.Errorf("%s should need session.manage", m)
		}
	}
}

func TestRoles(t *testing.T) {
	if Allowed(RoleViewer, PermControl) || Allowed(RoleViewer, PermAttach) || Allowed(RoleOperator, PermAttach) {
		t.Error("role grants too much")
	}
	if !Allowed(RoleOwner, PermAttach) || !Allowed(RoleViewer, PermObserve) {
		t.Error("role grants too little")
	}
	if Allowed("nonsense", PermObserve) {
		t.Error("unknown role grants something")
	}
}
