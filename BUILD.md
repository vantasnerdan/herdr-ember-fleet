# Build plan: v1

[PLAN.md](PLAN.md) is the long-range design. This file is what v1 builds, in order, and why it is smaller.

## The goal for v1

**Every agent's herdr, on every computer and every OS user, in one fleet web page.** You can see each agent's state, watch its screen live, and take control of it when needed. Getting there should need no SSH.

Agents stay exactly where and how they are. Each one runs from its own home folder (`/home/<user>` or `/home/<agent-name>`), in whatever harness it uses, and handles its own projects and working folders. The fleet reads herdr. It does not copy pi-durables sessions, projects or jobs, and it does not create projects. It also does not drive agents: there is no mail injection and no auto-answering. The operator types; the fleet only carries it.

## What v1 includes

| In v1 | Later |
|---|---|
| Relay, dock, hub, pairing with one-time links, end-to-end TLS between hub and dock | Direct LAN mode, a second relay |
| Fleet tree: machine → OS user → herdr session → workspace → tab → pane, with harness and state | Agent registry (`agents.toml`), mail addresses |
| **Needs you**: every `blocked` agent across the fleet, plus browser notifications | Mail (JMAP) correspondence view |
| Live terminals in the browser: watch (read-only), *Take control*, release | Kitty graphics, scrollback search |
| Prompt bar (`agent.prompt`), Esc / Ctrl-C / Enter, rename, close pane | New workspace in a folder, start agents, worktrees |
| `ember-fleet attach <machine>` (the stock herdr app through the relay), `ember-fleet term` | herdr plugin, macOS installer package, Homebrew |
| Add a machine from the page or `scripts/add-host.sh`; remove a machine (revokes and cuts channels) | Roles UI, audit viewer, key rotation command |
| Marbell Ember look with full WebGL effects; phone layout | Multi-user hub |
| systemd user services (linger) and launchd user agents | Windows docks |

## A typical fleet

```
                        server.example.com
  laptop                ┌──────────────────────────────────┐
  ┌──────────────┐      │ relay   :4782 (gRPC, pinned TLS) │
  │ hub  :4790   │─────►│ hub     :443 (optional; Let's    │◄── browser, phone
  │ dock (you)   │─────►│          Encrypt + sign-in)      │
  └──────────────┘      │ dock (admin user)                │
                        │ dock (each agent OS user)        │
                        └──────────────────────────────────┘
```

- **Relay** on a reachable server, at a DNS name such as `server.example.com:4782`. It uses its own self-signed certificate, pinned by every link. That way it doesn't depend on Let's Encrypt renewals, and a new IP address only needs a DNS change.
- **Hub** on the laptop at `http://127.0.0.1:4790` to start with. It can move to the server later at `https://server.example.com`, with a Let's Encrypt certificate and sign-in. `scripts/move-hub.sh` copies its keys and records, so no machine has to pair again.
- **Docks**: one for each OS user that runs herdr. That means you on the laptop, plus the admin user and each agent user on the server. Every dock serves all of its user's herdr sessions (`default` and any named ones), and keeps the `default` server running after a reboot.

## The pieces

One Go binary, `ember-fleet`, plus the web page embedded in it.

```
cmd/ember-fleet/      relay | hub | dock | join | link | login | attach | term | status | service
internal/relay/       gRPC relay server and client (ember.relay.v1)
internal/e2e/         net.Conn over a relay stream, TLS 1.3 with pinned keys, channel header
internal/pair/        join links, ticket and pairing-key derivation, proofs, key files
internal/herdr/       socket client (one request per connection), peer PID check, session discovery
internal/dock/        snapshots and events, terminals, actions allow-list, endpoint bridge, audit
internal/hub/         fleet model, HTTP + WebSocket API, sign-in, CSRF, links, machines
internal/service/     systemd user units, launchd agents
proto/ember/relay/v1  relay.proto
web/                  Vue 3 + Vite + xterm.js (WebGL renderer), Marbell Ember theme and effects
scripts/              build.sh, add-host.sh, install.sh, move-hub.sh, dev.sh
```

### Relay (`ember.relay.v1`)

- `Enroll(ticket)`: the caller's TLS client certificate becomes a member (hub or dock) if the hash of the ticket is on record and unused.
- `Invite(ticket hash, name, expiry)`: hub only; records a pending dock ticket.
- `Dock(stream)`: a dock's control line (hello, presence, "open channel X").
- `Accept(stream)` and `Open(stream)`: the dock's and hub's halves of one channel. The relay joins them and copies opaque chunks with flow control.
- `Directory(stream)`: hub only; lists the hub's docks with online/last-seen.
- `Revoke(dock)`: hub only; removes the key and cuts its live channels at once.

The relay authenticates members by TLS client-certificate fingerprint and stores only public-key fingerprints and ticket hashes. HTTP/2 keepalive pings every 10 s with a 10 s timeout; reconnects back off with jitter from 0.5 s, capped at 15 s.

### End to end

Every channel is TLS 1.3 between hub (client) and dock (server) inside the relay stream. Each side pins the other's Ed25519 key. The first line inside the channel names its kind:

| Kind | What | Dock permission |
|---|---|---|
| `pair` | dock proves its key with the pairing key from the link | (pairing only) |
| `watch` | the dock's info, then each herdr session's latest snapshot whenever it changes | `inventory.read` |
| `observe` | one pane's frames at the viewer's size (re-opened on resize) | `terminal.observe` |
| `control` | frames plus input, resize, scroll, mouse, release; `takeover` optional | `terminal.control` / `terminal.takeover` |
| `action` | one allow-listed herdr method, validated, tied to the server epoch | per method |
| `endpoint` | raw `herdr remote-client-bridge` bytes, for `attach` | `endpoint.attach` |

**Pairing.** A link holds the relay address and pin, the hub's key fingerprint and a 32-byte secret. Two values are derived from the secret: `ticket = HMAC(secret, "ember-ticket-v1")` for the relay, and `pairkey = HMAC(secret, "ember-pair-v1")`, which never leaves the dock and the hub. The dock trusts only the hub fingerprint from the link. The hub binds a link to a dock key only when the dock sends `HMAC(pairkey, dockFP ‖ hubFP)` inside a TLS channel whose peer key is `dockFP`. The link record (pending, used, expired, cancelled) lives in the hub's own state file, written atomically. A relay that swaps either key, or replays a used link, gets nothing.

**Server epochs.** The dock identifies each herdr server by the PID and start time of the process on the other end of the socket (`SO_PEERCRED` on Linux, `LOCAL_PEERPID` on macOS). Every snapshot carries an epoch ID. Every action carries the epoch ID the operator saw, and the dock checks it against the socket it has just connected to. If the epoch doesn't match, the action fails closed.

### Dock

- Finds herdr sessions (`~/.config/herdr/herdr.sock` and `sessions/*/herdr.sock`). For each one it keeps an `events.subscribe` connection open (lifecycle events, plus `pane.agent_status_changed` for every pane) and treats each event as a cue to re-read. A coalesced `session.snapshot` is re-read on events and every 15 s, and sent on only when it has changed.
- Terminals run the machine's own `herdr --session S terminal session observe|control`. Actions open one fresh socket connection per request. Every herdr process the dock starts gets a clean environment with no `HERDR_*` variables.
- Allowed actions: `agent.prompt`, `pane.send_keys`, `pane.send_text`, `pane.rename`, `tab.rename`, `workspace.rename`, `pane.close`, `tab.close`, `workspace.close`. Anything else is refused.
- Permissions come from `dock.json`, keyed by hub fingerprint (Owner by default for the hub that paired). The dock keeps an audit log in JSON lines: who, what, target and outcome. Keystrokes and screen contents are not logged.
- A dock never runs as root. It serves only its own user's sockets.

### Hub

- Holds the relay link, its own key, link records and machines. It keeps one `watch` channel open per online dock, and gives the browser a fleet model that updates live.
- Sign-in: `ember-fleet login-link` prints a one-time URL. Following it sets an HttpOnly, SameSite=Strict session cookie, which is also Secure on HTTPS. The hub checks Host and Origin on every request and WebSocket, and every change needs a CSRF header.
- Terminal tools (`attach`, `term`) authenticate with an access token, made with `ember-fleet token` on the hub or from the page. They connect over WebSocket, so the laptop needs no relay credentials.
- Serves `/install.sh` and the release binaries, so a new machine needs one line.

## Web API (hub ↔ page)

All JSON. Cookie auth with an exact `Origin` check; `X-Ember-CSRF` on every non-GET request, and the CSRF token as a WebSocket subprotocol (`new WebSocket(url, ["ember.v1", "csrf." + csrf])`). Terminal tools use `Authorization: Bearer <access token>` instead. Every socket ends when its sign-in session or token is revoked or expires.

| Call | Does |
|---|---|
| `POST /api/signin {token}` | exchanges a one-time sign-in token for a session cookie |
| `POST /api/signin/pin {pin}` | signs in with the fleet's PIN (401 wrong, 429 paused after too many wrong ones) |
| `POST /api/signin/ask` · `poll {id, poll}` · `cancel {id, poll}` | a browser asks to be approved, waits, or withdraws; `GET /api/signin/requests` and `POST /api/signin/decide {id, approve}` for signed-in pages |
| `GET/POST/DELETE /api/pin` | `{set, at}`; POST `{pin}` sets one, `{}` makes one and returns it once; DELETE turns PIN sign-in off |
| `GET /api/sessions` · `DELETE /api/sessions/{id}` | signed-in browsers `{id, agent, via, created, seen, current}`; sign one out (its sockets close) |
| `GET /api/me` | `{signedIn, csrf, fleet, hub, version, pin}` |
| `GET /api/fleet` | `{machines: [Machine]}` |
| `WS /api/live` | `{type:"hello", fleet, version}` first (a page whose build differs offers a reload), then `{type:"machine", machine}` and `{type:"removed", host}` as things change (coalesced, never dropped) |
| `WS /api/term?dock=&session=&epoch=&pane=&terminal=&mode=observe\|control&takeover=0\|1&cols=&rows=` | targets the exact server (epoch) and terminal the page last saw; `cols=0&rows=0` with control keeps the pane's real size. server→page `{type:"frame",seq,full,width,height,data}` (base64 ANSI) and `{type:"status",state,reason}` (`open`, `busy`, `closed`, `error` with `stale`/`gone`); page→server `{type:"input",text}`, `{type:"resize",cols,rows}`, `{type:"scroll",direction,lines}`, `{type:"release"}` |
| `WS /api/endpoint?dock=&session=` | `attach`: raw herdr client bytes as binary messages (owner only) |
| `POST /api/action {dock, session, epoch, method, params}` | `{ok, result}` or `{ok:false, error, outcome:"not_sent"\|"unknown"}` |
| `GET/POST/DELETE /api/links` | list, create `{name}` → `{link, command, expires}`, cancel |
| `DELETE /api/docks/{id}` | revoke a machine user |
| `POST /api/docks/{id}/rename {name}` | display name |
| `POST /api/tokens` | a terminal access token, shown once |

A `Machine` groups docks by host: `{host, docks:[{id, name, user, os, herdr, online, lastSeen, role, sessions:[{name, epoch, online, error, snapshot}]}]}`. Here `snapshot` is herdr's own `session.snapshot` result: workspaces, tabs, panes, agents and layouts.

## Scripts

- `scripts/dev.sh`: builds the web page and the binary, then runs a relay, hub and dock locally against a throwaway herdr session.
- `scripts/build.sh`: release binaries for Linux and macOS (amd64, arm64) into `dist/`.
- `scripts/add-host.sh <name> [user@host]`: makes a join link. With an SSH target it also installs and joins over SSH, which is handy for machines you can still reach. Without one, it prints the line to paste on the new machine: `curl -fsSL https://<hub>/install.sh | sh`, which installs the binary, asks for the link, joins and starts the service.
- `scripts/move-hub.sh user@host`: moves the hub's state to a server and installs it as a service there, on 443 with Let's Encrypt.

## Order of work

1. Clean up: retire the old pi-durables relay and backend.
2. Relay, pairing and end to end, with tests: key substitution, used, expired and cancelled links through a lying relay, and revocation cutting channels.
3. Dock: sessions, snapshots, epochs, terminals, actions, endpoint.
4. Hub: model, sign-in, API, WebSockets; the page, built in parallel against the API above.
5. Locally: relay + hub + dock on one computer, then the relay on the server and docks on both.
6. `attach` and `term` through the relay; the drop-and-reconnect checks.
7. Services, install script, `add-host.sh`, `move-hub.sh`; README.
8. Astra reviews the security-sensitive code (pairing, end to end, sign-in, dock permissions).
