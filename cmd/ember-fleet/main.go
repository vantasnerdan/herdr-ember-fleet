// ember-fleet: every agent's herdr, on every computer, in one fleet.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/admin"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/cli"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/dock"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/hub"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/keys"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/relay"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/service"
)

var version = "dev"

const usage = `ember-fleet: every agent's herdr, on every computer, in one fleet.

Set up (once):
  ember-fleet relay init --public HOST:PORT   on the reachable server; prints a hub link
  ember-fleet hub init [--fleet NAME]         asks for the hub link
  ember-fleet link NAME                       a one-time link to add a machine (or use the page)
  ember-fleet link list | link cancel ID
  ember-fleet join [--no-service]             on each machine, as each agent user; asks for the link

Run (normally as services, see 'service'):
  ember-fleet relay serve
  ember-fleet hub serve [--listen 127.0.0.1:4790] [--url https://... | --public DOMAIN] [--dev-origin URL]
  ember-fleet dock serve

Use:
  ember-fleet login-link                      a one-time sign-in link for the page
  ember-fleet attach MACHINE[/USER] [--session NAME]   the herdr app on that machine, no SSH
  ember-fleet term MACHINE[/USER]/PANE [--watch|--fit|--takeover]
  ember-fleet login HUB-URL                   use a hub from another computer (asks for a token)
  ember-fleet token [NAME]                    an access token for attach/term elsewhere
  ember-fleet status

Manage:
  ember-fleet service install|remove relay|hub|dock [--system] [-- serve flags]
  ember-fleet hub set-url https://fleet.example.com   the address a TLS proxy serves the hub at
  ember-fleet relay hub-link | relay status
  ember-fleet leave                           remove this user's dock from its fleet
  ember-fleet version
`

func main() {
	log.SetFlags(log.LstdFlags)
	dock.Version, hub.Version = version, version
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, args[0], args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ember-fleet:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "version":
		fmt.Println("ember-fleet", version)
		return nil
	case "relay":
		return relayCmd(ctx, args)
	case "hub":
		return hubCmd(ctx, args)
	case "dock":
		if len(args) == 0 || args[0] != "serve" {
			return errors.New("usage: ember-fleet dock serve")
		}
		d, err := dock.New()
		if err != nil {
			return err
		}
		return d.Run(ctx)
	case "join":
		fs := flag.NewFlagSet("join", flag.ExitOnError)
		noService := fs.Bool("no-service", false, "don't install the dock as a service")
		fs.Parse(args)
		link := fs.Arg(0)
		if link != "" {
			fmt.Fprintln(os.Stderr, "Note: a link on the command line ends up in shell history; next time, paste it when asked.")
		} else {
			var err error
			link, err = ask("Paste the join link: ", true)
			if err != nil {
				return err
			}
		}
		c, err := dock.Join(ctx, link)
		if err != nil {
			return err
		}
		fmt.Printf("Joined the fleet as %q. The hub pairs with this machine when the dock first connects.\n", c.Name)
		if *noService {
			fmt.Println("Start it with: ember-fleet dock serve")
			return nil
		}
		msg, err := service.Install("dock", nil, false)
		if err != nil {
			return fmt.Errorf("joined, but the service did not start: %w (run `ember-fleet dock serve` yourself)", err)
		}
		fmt.Println(msg)
		return nil
	case "leave":
		service.Remove("dock", false)
		fmt.Println("Stopped this user's dock. Remove the machine in the fleet page too; then delete", dock.Dir(), "to join another fleet.")
		return nil
	case "link":
		if len(args) == 1 && args[0] == "list" {
			var links []hub.LinkView
			if err := admin.Call(hub.AdminPath(), "links", nil, &links); err != nil {
				return hubErr(err)
			}
			for _, l := range links {
				fmt.Printf("  %s  %-10s %-20s expires %s\n", l.ID, l.State, l.Name, time.UnixMilli(l.Expires).Format("Jan 2 15:04"))
			}
			return nil
		}
		if len(args) == 2 && args[0] == "cancel" {
			if err := admin.Call(hub.AdminPath(), "cancel-link", map[string]string{"id": args[1]}, nil); err != nil {
				return hubErr(err)
			}
			fmt.Println("Cancelled.")
			return nil
		}
		if len(args) != 1 || strings.HasPrefix(args[0], "-") {
			return errors.New("usage: ember-fleet link MACHINE-NAME | link list | link cancel ID")
		}
		var res struct {
			Link    string `json:"link"`
			Command string `json:"command"`
			Expires int64  `json:"expires"`
		}
		if err := admin.Call(hub.AdminPath(), "link", map[string]string{"name": args[0]}, &res); err != nil {
			return hubErr(err)
		}
		fmt.Printf("Join link for %q (secret; works once; expires %s):\n\n  %s\n\n", args[0], time.UnixMilli(res.Expires).Format("Jan 2 15:04"), res.Link)
		if strings.HasPrefix(res.Command, "curl ") {
			fmt.Printf("On that machine, as the user whose agents should join:\n\n  %s\n\nand paste the link when asked.\n", res.Command)
		} else {
			fmt.Printf("This hub is only reachable on this computer. Either copy ember-fleet to that machine and run\n`ember-fleet join` there (paste the link when asked), or from here:\n\n  %s\n", res.Command)
		}
		return nil
	case "login-link":
		var res struct {
			URL string `json:"url"`
		}
		if err := admin.Call(hub.AdminPath(), "login-link", nil, &res); err != nil {
			return hubErr(err)
		}
		fmt.Printf("Open this within 15 minutes; it works once:\n\n  %s\n", res.URL)
		return nil
	case "token":
		name := "terminal"
		if len(args) > 0 {
			name = args[0]
		}
		var res struct {
			Token string `json:"token"`
			Hub   string `json:"hub"`
		}
		if err := admin.Call(hub.AdminPath(), "token", map[string]string{"name": name}, &res); err != nil {
			return hubErr(err)
		}
		fmt.Printf("Access token (shown once):\n\n  %s\n\nOn the other computer: ember-fleet login %s\n", res.Token, res.Hub)
		return nil
	case "login":
		if len(args) != 1 {
			return errors.New("usage: ember-fleet login HUB-URL")
		}
		tok, err := ask("Access token: ", true)
		if err != nil {
			return err
		}
		if err := cli.Login(args[0], tok); err != nil {
			return err
		}
		fmt.Println("Saved. Try: ember-fleet status")
		return nil
	case "attach":
		fs := flag.NewFlagSet("attach", flag.ExitOnError)
		session := fs.String("session", "default", "herdr session on that machine")
		fs.Parse(reorder(args, map[string]bool{"--session": true, "-session": true}))
		if fs.NArg() != 1 {
			return errors.New("usage: ember-fleet attach MACHINE[/USER] [--session NAME]")
		}
		return cli.Attach(ctx, fs.Arg(0), *session)
	case "term":
		fs := flag.NewFlagSet("term", flag.ExitOnError)
		watch := fs.Bool("watch", false, "only watch")
		fit := fs.Bool("fit", false, "resize the pane to this terminal")
		takeover := fs.Bool("takeover", false, "take control from whoever has it")
		fs.Parse(reorder(args, nil))
		if fs.NArg() != 1 {
			return errors.New("usage: ember-fleet term MACHINE[/USER]/PANE [--watch|--fit|--takeover]")
		}
		return cli.Term(ctx, fs.Arg(0), *watch, *fit, *takeover)
	case "update":
		// ember-fleet update [DOCK-ID]: update every machine (or one) now
		args2 := map[string]string{}
		if len(args) == 1 {
			args2["id"] = args[0]
		}
		if err := admin.Call(hub.AdminPath(), "update", args2, nil); err != nil {
			return hubErr(err)
		}
		fmt.Println("Updating; follow it in the page's settings, or: ember-fleet update-status")
		return nil
	case "update-status":
		var st hub.UpdateStatus
		if err := admin.Call(hub.AdminPath(), "update-status", nil, &st); err != nil {
			return hubErr(err)
		}
		fmt.Printf("hands out ember-fleet %s; daily at %02d:00: %v; herdr too: %v; live handoff: %v; running: %v\n",
			st.Target, st.Settings.Hour, st.Settings.Enabled, st.Settings.Herdr, st.Settings.Handoff, st.Running)
		for _, d := range st.Docks {
			line := fmt.Sprintf("  %-16s %-14s ember-fleet %-8s herdr %-7s server %-7s", d.Machine, d.User, d.Ember, d.Herdr, d.HerdrServer)
			if r := d.Result; r != nil {
				if r.Error != "" {
					line += "  last: " + r.Error
				} else {
					line += "  last: ok " + time.UnixMilli(r.At).Format("Jan 2 15:04")
				}
			}
			fmt.Println(line)
		}
		return nil
	case "status":
		return status(ctx)
	case "service":
		return serviceCmd(args)
	}
	return fmt.Errorf("unknown command %q (try ember-fleet help)", cmd)
}

// reorder lets flags come after the positional argument.
func reorder(args []string, withValue map[string]bool) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if withValue[a] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func hubErr(err error) error {
	if errors.Is(err, admin.ErrNotRunning) {
		return errors.New("no hub is running for this user on this computer (start it: ember-fleet hub serve, or ember-fleet service install hub)")
	}
	return err
}

func relayCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ember-fleet relay init|serve|hub-link|status|remove")
	}
	switch args[0] {
	case "init":
		fs := flag.NewFlagSet("relay init", flag.ExitOnError)
		public := fs.String("public", "", "host:port that hubs and docks dial, e.g. relay.example.com:4782")
		listen := fs.String("listen", ":4782", "address to listen on")
		fs.Parse(args[1:])
		link, err := relay.Init(*public, *listen)
		if err != nil {
			return err
		}
		fmt.Printf("Relay set up. Start it: ember-fleet service install relay\n\nHub link (secret; works once; 24 hours):\n\n  %s\n\nOn the hub computer: ember-fleet hub init   (paste the link when asked)\n", link)
		return nil
	case "serve":
		return relay.Run(ctx)
	case "hub-link":
		link, err := relay.HubLink()
		if err != nil {
			return err
		}
		fmt.Printf("Hub link (secret; works once; 24 hours):\n\n  %s\n", link)
		return nil
	case "status":
		var res struct {
			Public  string                  `json:"public"`
			Pin     string                  `json:"pin"`
			Members map[string]relay.Member `json:"members"`
		}
		if err := admin.Call(relay.AdminPath(), "status", nil, &res); err != nil {
			return err
		}
		fmt.Printf("relay %s  pin %s\n", res.Public, res.Pin)
		for fp, m := range res.Members {
			seen := "never"
			if m.LastSeen > 0 {
				seen = time.UnixMilli(m.LastSeen).Format("Jan 2 15:04")
			}
			fmt.Printf("  %-4s %s  %-20s last seen %s\n", m.Role, keys.Short(fp), m.Name, seen)
		}
		return nil
	case "remove":
		if len(args) != 2 {
			return errors.New("usage: ember-fleet relay remove FINGERPRINT")
		}
		return admin.Call(relay.AdminPath(), "remove", map[string]string{"id": args[1]}, nil)
	}
	return fmt.Errorf("unknown relay command %q", args[0])
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func hubCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ember-fleet hub init|serve")
	}
	switch args[0] {
	case "init":
		fs := flag.NewFlagSet("hub init", flag.ExitOnError)
		fleet := fs.String("fleet", "Fleet", "the fleet's name, shown on the page")
		fs.Parse(args[1:])
		link, err := ask("Paste the hub link from `ember-fleet relay init`: ", true)
		if err != nil {
			return err
		}
		id, err := hub.Init(ctx, link, *fleet)
		if err != nil {
			return err
		}
		fmt.Printf("Hub set up (key %s). Start it: ember-fleet service install hub\nThen: ember-fleet login-link\n", keys.Short(id.Fingerprint))
		return nil
	case "set-url":
		if len(args) != 2 {
			return errors.New("usage: ember-fleet hub set-url https://fleet.example.com   (or \"\" to use the listen address)")
		}
		var res struct {
			Base string `json:"base"`
		}
		if err := admin.Call(hub.AdminPath(), "set-url", map[string]string{"url": args[1]}, &res); err != nil {
			return hubErr(err)
		}
		fmt.Printf("The hub now answers as %s (sign-in links, install command and allowed origins use it).\n", res.Base)
		return nil
	case "serve":
		fs := flag.NewFlagSet("hub serve", flag.ExitOnError)
		var o hub.Options
		var dev multi
		fs.StringVar(&o.Listen, "listen", "127.0.0.1:4790", "address for the page (loopback unless behind your own TLS)")
		fs.StringVar(&o.Public, "public", "", "serve HTTPS for this domain on :443 with Let's Encrypt")
		fs.StringVar(&o.URL, "url", "", "public https:// URL when a TLS proxy (Pangolin, Caddy...) forwards to --listen")
		fs.StringVar(&o.Email, "email", "", "contact address for Let's Encrypt")
		fs.Var(&dev, "dev-origin", "extra allowed page origin (the Vite dev server)")
		fs.Parse(args[1:])
		o.DevOrigins = dev
		return hub.Serve(ctx, o)
	}
	return fmt.Errorf("unknown hub command %q", args[0])
}

func serviceCmd(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: ember-fleet service install|remove relay|hub|dock [--system] [-- serve flags]")
	}
	op, role := args[0], args[1]
	if role != "relay" && role != "hub" && role != "dock" {
		return errors.New("role must be relay, hub or dock")
	}
	rest := args[2:]
	system := false
	var extra []string
	for i, a := range rest {
		if a == "--system" {
			system = true
			continue
		}
		if a == "--" {
			extra = rest[i+1:]
			break
		}
	}
	switch op {
	case "install":
		msg, err := service.Install(role, extra, system)
		if err != nil {
			return err
		}
		fmt.Println(msg)
		return nil
	case "remove":
		return service.Remove(role, system)
	}
	return errors.New("usage: ember-fleet service install|remove ROLE")
}

func status(ctx context.Context) error {
	any := false
	if c, err := dock.LoadConfig(); err == nil {
		any = true
		fmt.Printf("dock: machine %q, relay %s\n", c.Name, c.Relay)
		for _, h := range c.Hubs {
			state := "waiting for the hub to pair"
			if h.Paired {
				state = "paired"
			}
			fmt.Printf("  hub %s as %s, %s\n", keys.Short(h.FP), h.Role, state)
		}
	}
	var hs struct {
		Base     string        `json:"base"`
		Relay    bool          `json:"relay"`
		Machines []hub.Machine `json:"machines"`
	}
	if err := admin.Call(hub.AdminPath(), "status", nil, &hs); err == nil {
		any = true
		fmt.Printf("hub: %s, relay connected: %v\n", hs.Base, hs.Relay)
		printMachines(hs.Machines)
	} else if c, err := cli.Load(); err == nil {
		any = true
		ms, err := c.Fleet(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("hub: %s\n", c.Hub)
		printMachines(ms)
	}
	if !any {
		fmt.Println("Nothing set up for this user yet. See: ember-fleet help")
	}
	return nil
}

func printMachines(ms []hub.Machine) {
	for _, m := range ms {
		for _, d := range m.Docks {
			state := "offline"
			if d.Online {
				state = "online"
			}
			fmt.Printf("  %s/%s  %s  herdr %s\n", m.Host, d.User, state, d.Herdr)
			for _, raw := range d.Sessions {
				var s cli.Session
				if json.Unmarshal(raw, &s) != nil {
					continue
				}
				for _, p := range s.Snapshot.Panes {
					agent := p.Agent
					if agent == "" {
						agent = "shell"
					}
					fmt.Printf("    %-8s %-6s %-8s %s\n", s.Name+"/"+p.PaneID, agent, p.Status, p.Title)
				}
			}
		}
	}
}

// ask reads one line, without echo for secrets.
func ask(prompt string, secret bool) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if secret && term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return strings.TrimSpace(string(b)), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no input")
	}
	return strings.TrimSpace(line), nil
}
