package dock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/keys"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/pair"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/paths"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/relay"
	pb "github.com/vantasnerdan/herdr-ember-fleet/internal/relay/relaypb"
)

// Roles a hub can hold on this dock.
const (
	RoleViewer   = "viewer"
	RoleOperator = "operator"
	RoleOwner    = "owner"
)

// Permissions.
const (
	PermInventory = "inventory.read"
	PermObserve   = "terminal.observe"
	PermControl   = "terminal.control"
	PermTakeover  = "terminal.takeover"
	PermManage    = "session.manage"
	PermAttach    = "endpoint.attach"
	PermUpdate    = "fleet.update"
)

var rolePerms = map[string]map[string]bool{
	RoleViewer:   {PermInventory: true, PermObserve: true},
	RoleOperator: {PermInventory: true, PermObserve: true, PermControl: true, PermTakeover: true},
	RoleOwner:    {PermInventory: true, PermObserve: true, PermControl: true, PermTakeover: true, PermManage: true, PermAttach: true, PermUpdate: true},
}

// Allowed reports whether role grants perm.
func Allowed(role, perm string) bool { return rolePerms[role][perm] }

// HubGrant is a hub this dock trusts, and what it may do.
type HubGrant struct {
	FP      string `json:"fp"`
	Role    string `json:"role"`
	LinkID  string `json:"link_id"`
	PairKey []byte `json:"pair_key,omitempty"` // kept only until the hub confirms pairing
	Paired  bool   `json:"paired"`
}

// Config is dock.json. The machine owner can edit roles here.
type Config struct {
	Relay       string `json:"relay"`
	Pin         string `json:"pin"`
	Name        string `json:"name"`         // machine name, from the join link
	KeepRunning bool   `json:"keep_running"` // start the default herdr server if it is not running
	// AcceptUpdates lets the hub update ember-fleet and herdr on this machine
	// (default true). Set it to false to update this machine by hand.
	AcceptUpdates *bool      `json:"accept_updates,omitempty"`
	Hubs          []HubGrant `json:"hubs"`

	dir string
}

// Dir is the dock's directory.
func Dir() string { return paths.Role("dock") }

var cfgMu sync.Mutex

// LoadConfig reads dock.json.
func LoadConfig() (*Config, error) {
	b, err := os.ReadFile(filepath.Join(Dir(), "dock.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("this computer has not joined a fleet yet: run `ember-fleet join`")
		}
		return nil, err
	}
	c := Config{dir: Dir()}
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("dock.json: %w", err)
	}
	for i := range c.Hubs {
		if rolePerms[c.Hubs[i].Role] == nil {
			c.Hubs[i].Role = RoleViewer // unknown roles fail safe
		}
	}
	return &c, nil
}

// Save writes dock.json.
func (c *Config) Save() error {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if c.dir == "" {
		c.dir = Dir()
	}
	return keys.WriteFileAtomic(filepath.Join(c.dir, "dock.json"), b, 0o600)
}

// Join enrolls this OS user's dock with the relay named in a join link.
func Join(ctx context.Context, link string) (*Config, error) {
	j, err := pair.ParseJoin(link)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(Dir(), "dock.json")); err == nil {
		return nil, errors.New("this user already belongs to a fleet; run `ember-fleet leave` first")
	}
	if err := paths.Ensure(Dir()); err != nil {
		return nil, err
	}
	id, err := keys.LoadOrCreate(Dir(), "ember-fleet-dock")
	if err != nil {
		return nil, err
	}
	conn, client, err := relay.Dial(j.Relay, j.Pin, id)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	reply, err := client.Enroll(cctx, &pb.EnrollRequest{Ticket: pair.Ticket(j.Secret)})
	if err != nil {
		return nil, fmt.Errorf("the relay refused this link: %w", err)
	}
	if reply.Role != relay.RoleDock {
		return nil, errors.New("this is not a machine link")
	}
	c := &Config{
		dir:         Dir(),
		Relay:       j.Relay,
		Pin:         j.Pin,
		Name:        j.Name,
		KeepRunning: true,
		Hubs: []HubGrant{{
			FP:      j.HubFP,
			Role:    RoleOwner,
			LinkID:  pair.LinkID(j.Secret),
			PairKey: pair.PairKey(j.Secret),
		}},
	}
	if err := c.Save(); err != nil {
		return nil, err
	}
	return c, nil
}

// acceptsUpdates reports whether the hub may update this machine.
func (c *Config) acceptsUpdates() bool { return c.AcceptUpdates == nil || *c.AcceptUpdates }
