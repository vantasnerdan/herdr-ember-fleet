package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/admin"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/keys"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/pair"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/paths"
)

// Config is relay/config.json.
type Config struct {
	Listen string `json:"listen"` // e.g. :4782
	Public string `json:"public"` // host:port that hubs and docks dial
}

// Dir is the relay's directory.
func Dir() string { return paths.Role("relay") }

// AdminPath is the relay's admin socket.
func AdminPath() string { return filepath.Join(Dir(), "admin.sock") }

// Init creates the relay's key and config. It returns a hub link.
func Init(public, listen string) (string, error) {
	if _, _, err := net.SplitHostPort(public); err != nil {
		return "", fmt.Errorf("--public must be host:port, e.g. relay.example.com:4782")
	}
	if err := paths.Ensure(Dir()); err != nil {
		return "", err
	}
	cfgPath := filepath.Join(Dir(), "config.json")
	if _, err := os.Stat(cfgPath); err == nil {
		return "", errors.New("the relay is already set up; use `ember-fleet relay hub-link` for another hub link")
	}
	b, _ := json.MarshalIndent(Config{Listen: listen, Public: public}, "", "  ")
	if err := keys.WriteFileAtomic(cfgPath, b, 0o600); err != nil {
		return "", err
	}
	s, err := Open(Dir())
	if err != nil {
		return "", err
	}
	return s.hubLink(public)
}

// LoadConfig reads config.json.
func LoadConfig() (Config, error) {
	var c Config
	b, err := os.ReadFile(filepath.Join(Dir(), "config.json"))
	if err != nil {
		return c, errors.New("the relay is not set up: run `ember-fleet relay init --public host:port`")
	}
	return c, json.Unmarshal(b, &c)
}

func (s *Server) hubLink(public string) (string, error) {
	ticket := pair.NewSecret()
	if err := s.AddTicket(pair.TicketHash(ticket), Ticket{Role: RoleHub, Name: "hub", Expires: time.Now().Add(24 * time.Hour).UnixMilli()}); err != nil {
		return "", err
	}
	return pair.RelayLink{Relay: public, Pin: s.id.Fingerprint, Ticket: ticket}.String(), nil
}

// HubLink makes another one-time hub link, through the running relay if
// there is one.
func HubLink() (string, error) {
	var out struct {
		Link string `json:"link"`
	}
	err := admin.Call(AdminPath(), "hub-link", nil, &out)
	if err == nil {
		return out.Link, nil
	}
	if !errors.Is(err, admin.ErrNotRunning) {
		return "", err
	}
	cfg, err := LoadConfig()
	if err != nil {
		return "", err
	}
	s, err := Open(Dir())
	if err != nil {
		return "", err
	}
	return s.hubLink(cfg.Public)
}

// Run serves the relay until ctx ends.
func Run(ctx context.Context) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	s, err := Open(Dir())
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	go admin.Serve(ctx, AdminPath(), func(ctx context.Context, op string, args json.RawMessage) (any, error) {
		switch op {
		case "hub-link":
			l, err := s.hubLink(cfg.Public)
			return map[string]string{"link": l}, err
		case "status":
			return map[string]any{"public": cfg.Public, "pin": s.id.Fingerprint, "members": s.Members()}, nil
		case "remove":
			var a struct {
				ID string `json:"id"`
			}
			json.Unmarshal(args, &a)
			if !s.RemoveMember(a.ID) {
				return nil, errors.New("no such member")
			}
			return map[string]bool{"ok": true}, nil
		}
		return nil, errors.New("unknown operation")
	})
	log.Printf("relay %s listening on %s (public %s)", keys.Short(s.id.Fingerprint), cfg.Listen, cfg.Public)
	return s.Serve(ctx, ln)
}
