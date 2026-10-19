# Herdr Ember Fleet

See and steer every coding agent on every computer you own, whatever harness it runs, from one web page or your own terminal. No SSH, no VPN, no port forwarding on the computers running agents.

```
 browser ──► hub ──┐                       ┌── dock ──► herdr   (laptop behind a home router)
                   ├── dial out ──► relay ◄─┼── dock ──► herdr   (office box on a private LAN)
 terminal ─────────┘                       └── dock ──► herdr   (cloud VM with a changing IP)
```

Agents run in [herdr](https://herdr.dev) panes: Claude Code, Codex, omp, Grok, gemini, opencode, your own bots. Herdr Ember Fleet reads herdr, so it works with all of them and runs none of them. It doesn't manage projects or drive agents. Each agent works from its own home folder, in its own way.

> **Status: v1, early.** The relay, hub, docks, pairing, live terminals, `attach` and `term` work end to end on the author's own fleet. Expect rough edges. [BUILD.md](BUILD.md) is what v1 covers; [PLAN.md](PLAN.md) is the longer design.

## What you get

- **One page for the whole fleet.** Machine → OS user → herdr session → workspace → tab → pane, with each agent's harness and state (working, needs you, idle, done), live. Arrange the tree by your own groups (Physics, Coding, Burst…) or by machine; each agent shows once.
- **Find any agent fast.** Ctrl/Cmd-K searches agents, machines and pages. **Agents** lists every agent with its task, place, state and last activity, sortable and filterable. **Machines** shows each machine's users, groups, agents and versions. Every view has a link you can bookmark, like `#/a/studio/agent-one/w1:p2`.
- **Needs you.** Every agent waiting for input across the fleet, in one list, with browser notifications.
- **Live terminals.** Watch any pane read-only (fitted to the window, or the whole pane scaled down); *Take control* to type; a prompt bar and Esc / Ctrl-C / Enter buttons that work without taking control. `[` and `]` step to the previous and next agent.
- **The herdr app you already use, no SSH.** `ember-fleet attach studio` opens the unchanged herdr app on that machine through the relay.
- **One pane in your terminal.** `ember-fleet term studio/w1:p2` (or `studio/codex`).
- **Reach machines SSH can't.** Every computer dials *out* to the relay. Laptops that change networks, machines behind NAT and VMs with new addresses just reconnect.
- **The relay can't read or change your traffic.** Hub and machines talk TLS 1.3 end to end, each pinning the other's key. Pairing uses a secret the relay never sees.
- **Each machine sets its own rules.** Viewer, operator or owner, enforced on that machine, with a local audit log.

## How it fits together

One binary, `ember-fleet`, in three roles:

| Role | Runs on | Does |
|---|---|---|
| `relay` | one server everyone can reach (one open port) | the meeting point: authenticates keys, routes channels, never parses them |
| `hub` | your laptop, or a server with HTTPS | the web page and the fleet's records; opens end-to-end channels to docks |
| `dock` | every computer that runs herdr, **once per OS user** | serves that user's herdr sessions to the hub; enforces permissions |

## Set up a fleet

You need one server reachable from everywhere (a small VM is plenty), with one open port (4782 below). Install herdr on every computer that runs agents.

**1. Build** (Go 1.26+ and Node 20+):

```sh
git clone https://github.com/vantasnerdan/herdr-ember-fleet && cd herdr-ember-fleet
scripts/build.sh            # binaries for Linux and macOS in dist/
```

**2. The relay**, on the server:

```sh
install -m 755 dist/ember-fleet-linux-amd64 ~/.local/bin/ember-fleet
ember-fleet relay init --public relay.example.com:4782
ember-fleet service install relay
```

`relay init` prints a one-time **hub link**.

**3. The hub**, on your laptop (the page stays on `127.0.0.1`):

```sh
ember-fleet hub init --fleet "My fleet"      # paste the hub link
ember-fleet service install hub
ember-fleet login-link                       # open the link it prints
```

To sign in from other computers, make a **PIN**: `ember-fleet pin`, or **Settings › Signing in** on the page. Type it on the sign-in page of any browser that can reach the hub. It doesn't expire, and the browser stays signed in while it's used. With no PIN with you, a new browser shows a code and asks to be approved: a browser that is already signed in shows the same code with **Approve**. **Settings › Browsers** lists the signed-in browsers, and you can sign any of them out.

**4. Add machines.** Use **Add a machine** on the page, or:

```sh
scripts/add-host.sh studio                   # prints a link and the command to run there
scripts/add-host.sh studio me@studio.local   # or installs and joins over SSH, right now
```

On the new computer, as the user whose agents should join:

```sh
curl -fsSL https://<your hub>/install.sh | sh      # asks for the link
```

That installs `ember-fleet`, joins, and starts the dock as a user service: systemd with lingering on Linux, a launchd agent on macOS. A link works once and expires in 24 hours. Several OS users on one computer each run their own dock. Give their links the same machine name and they show up together.

**5. Optional: move the page to the server**, so a phone can reach it. There are two ways.

Behind your own TLS proxy (Pangolin, Caddy, nginx), with the hub on a local port:

```sh
scripts/move-hub.sh me@relay.example.com --url https://fleet.example.com
# later, if the address changes: ssh me@relay.example.com ember-fleet hub set-url https://new.example.com
```

If the proxy adds its own sign-in (Pangolin SSO, for example), let `/install.sh` and `/dl/*` through without it, so new machines can install. Do the same for `/api/*` if you use `ember-fleet attach` or `term` from other computers. The hub still checks its own sign-in, tokens and join links on all of them.

Or the hub serves HTTPS itself, with a Let's Encrypt certificate on ports 80 and 443:

```sh
scripts/move-hub.sh me@relay.example.com --public fleet.example.com
```

Either way, the hub's key and records move with it, so no machine pairs again.

## Everyday use

```sh
ember-fleet status                      # machines, sessions and agents
ember-fleet attach studio               # the herdr app on studio (--session NAME for others)
ember-fleet attach studio/agent-one     # a specific OS user on studio
ember-fleet term studio/codex           # one pane here; Ctrl-] to leave
ember-fleet term studio/w1:p2 --watch   # read-only
```

Run `attach` from an ordinary terminal, not inside a herdr pane. Herdr refuses nested clients unless you turn on its `allow_nested` setting. `term` works anywhere.

From another computer, make a token on the hub (`ember-fleet token laptop`, or in the page's settings), then run `ember-fleet login https://fleet.example.com`.

## Keeping the fleet current

The hub hands its own `ember-fleet` build to every machine, over the same end-to-end channel it already uses. The dock checks the file's size, SHA-256 and reported version, swaps it in, and restarts itself. Then the dock runs `herdr update` on its machine. This happens once a day (04:00 hub time by default), or when you press **Update now** in Settings → Updates, where you also see every machine's versions and the last result. Only the owner role can update. A machine can opt out with `"accept_updates": false` in its `dock.json`.

herdr is updated **without live handoff** by default. Running servers, and the agents' monitors, waits and pane-to-pane messages, are left alone, and pick up the new version on their next restart. Turn on live handoff to move running servers over at once. It keeps pane processes alive, but herdr calls it experimental, and connections in flight may be cut.

From the hub's terminal: `ember-fleet update [DOCK-ID]` and `ember-fleet update-status`. To ship a new version, put the new binaries and a `VERSION` file in the hub's `dist/` (`scripts/build.sh` writes both) and restart the hub.

## Security model, briefly

- **Joining.** A join link carries the relay's address and certificate pin, the hub's key fingerprint and a 256-bit secret. The dock derives a *ticket* for the relay and a *pairing key* the relay never sees. It proves its own key to the hub with the pairing key, inside TLS that pins the hub's key from the link. The hub keeps its own record of every link and binds each one to exactly one key. A relay that swaps keys, or replays a used or cancelled link, gets nothing. `internal/fleettest` tests each of these.
- **Traffic.** Every channel is TLS 1.3 between hub and dock, inside the relay's gRPC stream. The relay sees who talks to whom, when, and how much, but not what.
- **Permissions** are enforced on each machine (`~/.config/ember-fleet/dock/dock.json`). Viewers can only watch. Typing into a terminal or using `attach` is full trust: it is that user's shell. Actions are an allow-list (prompt, keys, rename, close). Server control, plugins and integrations are never offered.
- **Stale targets.** Terminals and actions are tied to the exact herdr server process the page last saw. If herdr restarted, they fail closed instead of reaching a new pane that happens to have the same ID.
- **The page** needs sign-in even on `127.0.0.1`: a one-time sign-in link, the PIN, or approval from a signed-in browser. Sessions are HttpOnly SameSite=Strict cookies, with exact Host and Origin checks and CSRF on every change, including WebSockets.
- **The PIN** is meant as a second lock behind something that already guards the address (an SSO proxy, a VPN). It is stored as an Argon2id hash. Wrong PINs pause PIN sign-in for the whole hub after 5 tries in 15 minutes or 20 in a day.
- **Audit.** Each dock logs who did what, when and to which pane in `~/.config/ember-fleet/dock/audit.jsonl`. It never logs keystrokes, screens or prompt text.

## Development

```sh
go test -race ./...
scripts/dev.sh                          # throwaway relay + hub + dock against your herdr
cd web && npm run dev                   # the page with hot reload (proxies /api to the hub)
cd web && npm run dev:mock              # the page against made-up data
```

Layout: `cmd/ember-fleet` (CLI), `internal/{relay,hub,dock,e2e,pair,keys,herdr,cli,service}`, `proto/ember/relay/v1`, `web/` (Vue 3 + xterm.js).

Built on lessons from Ember Fleet, a private fleet manager for [durable-pi](https://github.com/disler/durable-pi) agents whose gRPC relay this project generalizes, and dressed in [Marbell Ember](https://github.com/vantasnerdan/marbell-ember).

## License

MIT, see [LICENSE](LICENSE).
