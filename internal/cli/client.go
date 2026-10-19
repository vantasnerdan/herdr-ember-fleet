// Package cli holds the terminal tools: login, attach and term. They talk to
// the hub over HTTPS/WebSocket with an access token, so the computer you type
// on needs no relay credentials.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/admin"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/hub"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/keys"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/paths"
)

// Config is cli.json: which hub to use and the access token for it.
type Config struct {
	Hub   string `json:"hub"`
	Token string `json:"token"`
}

func configPath() string { return filepath.Join(paths.Role("cli"), "cli.json") }

// Save stores the hub and token (0600).
func (c Config) Save() error {
	if err := paths.Ensure(paths.Role("cli")); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return keys.WriteFileAtomic(configPath(), b, 0o600)
}

// Load finds the hub to talk to: cli.json, or else a hub running on this
// computer for this user, which hands out a token over its admin socket.
func Load() (Config, error) {
	var c Config
	if b, err := os.ReadFile(configPath()); err == nil {
		if json.Unmarshal(b, &c) == nil && c.Hub != "" && c.Token != "" {
			return c, nil
		}
	}
	var res struct {
		Token string `json:"token"`
		Hub   string `json:"hub"`
	}
	host, _ := os.Hostname()
	if err := admin.Call(hub.AdminPath(), "token", map[string]string{"name": "cli@" + host}, &res); err != nil {
		return c, errors.New("no hub configured: run `ember-fleet login <hub-url>` with a token from the hub's settings")
	}
	c = Config{Hub: res.Hub, Token: res.Token}
	return c, c.Save()
}

// Login stores a hub URL and token after checking they work.
func Login(hubURL, token string) error {
	u, err := url.Parse(strings.TrimRight(hubURL, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("give the hub's address, like https://fleet.example.com")
	}
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return errors.New("use https for a hub that is not on this computer")
	}
	c := Config{Hub: u.String(), Token: strings.TrimSpace(token)}
	if _, err := c.Fleet(context.Background()); err != nil {
		return err
	}
	return c.Save()
}

func isLoopback(h string) bool { return h == "127.0.0.1" || h == "localhost" || h == "::1" }

func (c Config) request(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Hub+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	return (&http.Client{Timeout: 20 * time.Second}).Do(req)
}

// Fleet fetches the fleet model.
func (c Config) Fleet(ctx context.Context) ([]hub.Machine, error) {
	resp, err := c.request(ctx, "/api/fleet")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("hub said %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		Machines []hub.Machine `json:"machines"`
	}
	return out.Machines, json.NewDecoder(resp.Body).Decode(&out)
}

func (c Config) dial(ctx context.Context, path string, q url.Values) (*websocket.Conn, error) {
	u := strings.Replace(c.Hub, "http", "ws", 1) + path + "?" + q.Encode()
	conn, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{
		HTTPHeader:      http.Header{"Authorization": {"Bearer " + c.Token}},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("hub said %s", resp.Status)
		}
		return nil, err
	}
	conn.SetReadLimit(32 << 20)
	return conn, nil
}

// Session is a decoded herdr session as the hub reports it.
type Session struct {
	Name     string `json:"name"`
	Epoch    string `json:"epoch"`
	Online   bool   `json:"online"`
	Error    string `json:"error"`
	Snapshot struct {
		Panes []Pane `json:"panes"`
	} `json:"snapshot"`
}

// Pane is the part of a herdr pane record the tools use.
type Pane struct {
	PaneID     string `json:"pane_id"`
	TerminalID string `json:"terminal_id"`
	Agent      string `json:"agent"`
	Status     string `json:"agent_status"`
	Title      string `json:"terminal_title_stripped"`
	Label      string `json:"label"`
	Cwd        string `json:"cwd"`
}

// findDock resolves "machine" or "machine/user".
func findDock(machines []hub.Machine, spec string) (hub.DockView, error) {
	host, usr, _ := strings.Cut(spec, "/")
	var matches []hub.DockView
	var names []string
	for _, m := range machines {
		names = append(names, m.Host)
		if !strings.EqualFold(m.Host, host) {
			continue
		}
		for _, d := range m.Docks {
			if usr == "" || strings.EqualFold(d.User, usr) {
				matches = append(matches, d)
			}
		}
	}
	switch len(matches) {
	case 0:
		return hub.DockView{}, fmt.Errorf("no machine %q; the fleet has: %s", spec, strings.Join(names, ", "))
	case 1:
		return matches[0], nil
	}
	var users []string
	for _, d := range matches {
		users = append(users, host+"/"+d.User)
	}
	return hub.DockView{}, fmt.Errorf("%s has several users; pick one: %s", host, strings.Join(users, ", "))
}

func sessions(d hub.DockView) []Session {
	var out []Session
	for _, raw := range d.Sessions {
		var s Session
		if json.Unmarshal(raw, &s) == nil {
			out = append(out, s)
		}
	}
	return out
}
