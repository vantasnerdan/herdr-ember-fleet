# Herdr Ember Fleet: plan

Status: **plan, not built yet.** Written in October 2026 by Claude (Opus 5.5) and Astra (Codex). Herdr facts are checked against herdr 0.9.3 ([source at the v0.9.3 release commit](https://github.com/herdrdev/herdr/tree/7b116c05bfda646af39d2524c54e70c751f57ee8)) and by running it; see [Evidence](#evidence).

## In one paragraph

Herdr Ember Fleet shows every coding agent on every computer you own in one place, whatever harness it runs (Claude Code, Codex, omp, Grok, your own bots), and lets you watch and type into them from a web page or your own terminal, including the unchanged herdr app. It reaches the [herdr](https://herdr.dev) server on each computer; it does not run agents itself. Computers connect **out** to one small relay over gRPC, so no computer running agents needs SSH, SSH keys, a VPN, port forwarding, an inbound port or a fixed IP address: a laptop that moves from home Wi-Fi to a hotel to a phone hotspot simply reconnects. The relay is the one thing that must be reachable.

## Why

Today a remote agent is reached with `herdr --remote user@host` or a saved herdr machine. Both ride on SSH, which breaks exactly where agents now live:

- **Laptops and home networks**: no inbound reachability behind a home router, CGNAT or hotel Wi-Fi.
- **Changing addresses**: DHCP virtual machines and laptops get new IPs, and SSH targets go stale.
- **Private networks**: a box on an office LAN is unreachable from outside without a VPN.
- **People without SSH set up**: an executive's MacBook should be one link and one command, not sshd, keys and `authorized_keys`.

The first Ember Fleet (private, built on [durable-pi](https://github.com/disler/durable-pi)) solved reachability with a gRPC dial-out relay and proved it in daily use: stations behind NAT come online, and in our tests relay restarts and cut connections recovered in about 3 seconds. But it is tied to one opinionated agent runtime (pi, driven through its backend). Most of the maintainer's agents run in other harnesses, in herdr panes, and talk to each other and to their operator over their own **JMAP** mail accounts. This project keeps what worked (the dial-out relay, join links, the fleet view and its look) and points it at herdr instead.

## What it is, and what it is not

It **is**:
- a fleet view of herdr servers: machines → sessions → workspaces → tabs → panes, with each pane's agent, harness and state (working, idle, blocked, done);
- live terminals for any pane, in the browser (xterm.js) and in your own terminal;
- the **stock herdr app** attached to a remote machine through the relay, with no SSH;
- light control: send a prompt, send keys (Esc, Ctrl-C), start an agent in a new pane, open a workspace in a folder (creating the folder if asked), rename, close;
- the relay and join flow that connect machines;
- later, a view of the agents' real mail correspondence, from the operator's own JMAP account.

It is **not**:
- an agent runtime. No pi, no durable jobs, no transcript store, no model or provider settings: each harness keeps its own.
- a puppeteer. Agents run their own harness in their own panes and coordinate over JMAP. The fleet never turns mail into typed prompts and never answers approval dialogs by itself.
- a herdr fork. It uses herdr's public CLI and socket API plus one existing client entry point, and keeps herdr as a separate install.
- a mail server, mailbox provisioning or mail administration.

## What herdr already gives us

| Herdr feature | What we use it for |
|---|---|
| **Socket API**: newline-delimited JSON over a Unix socket (`~/.config/herdr/herdr.sock`, one per named session). About a hundred methods; `herdr api schema --json` prints the JSON Schema of the installed version. | Inventory and actions. Capabilities are discovered per server, never assumed. |
| **`session.snapshot`**, `*.list` / `*.get` reads, and **`events.subscribe`** (workspace, tab, pane, layout and agent-status events) | The fleet tree, kept fresh without polling. Events are treated as *invalidations* that trigger authoritative reads, never replayed onto a snapshot (see [Keeping the tree current](#keeping-the-tree-current)). |
| **Agent detection**: claude, codex, omp, grok, gemini, cursor, opencode, amp and ~20 more, reported as `working / idle / blocked / done / unknown` | Harness-agnostic status. Harnesses herdr does not know (Grok Bot today) need a detection manifest or their own `pane.report_agent` hook. |
| **`herdr terminal session observe`**: NDJSON `terminal.frame` records (base64 ANSI; `seq`, `full`, `width`, `height`) rendered at the size the viewer asks for | Read-only live terminals. Any number at once. |
| **`herdr terminal session control [--takeover]`**: the same frames, plus NDJSON `terminal.input`, `resize`, `scroll`, `mouse` and `release` | Typing into a pane from the browser or a terminal. |
| **`herdr client`** (hidden) with `HERDR_CLIENT_SOCKET_PATH`, and **`herdr remote-client-bridge`** on the server side | The unchanged herdr app attached through the relay. See [The stock herdr app over the relay](#the-stock-herdr-app-over-the-relay). |
| `agent.prompt` (with an atomic wait), `agent.send_keys`, `agent.start`, `workspace.create`, `pane.split` | Actions that do not need a terminal. |
| **Plugins**: manifest actions, startup hooks, popup and split panes | An optional plugin with a *Fleet* picker inside herdr. |

### What we measured

On herdr 0.9.3, in a throwaway named session:

- **One controller per pane.** A second `control` without `--takeover` is refused ("already has an attached client; retry with --takeover"). `--takeover` disconnects the first controller with the reason "terminal attach taken over". `release` ends cleanly.
- **Observers run alongside a controller**, each receiving frames at the size it asked for, and never change the pane's size. (An observer's frame size is its own rendering size, not the pane's real size.)
- **A controller sets the pane's real size** (read back from herdr's own pane metrics), even while someone has that pane open in the herdr app. Their window resizes are ignored until the controller releases, and then the pane snaps back to their size.
- **Control is not an input lock.** While a controller held the pane, typing in the herdr app still reached it, and so does `pane.send_text`. Permissions therefore have to be enforced by the dock, not by herdr's controller check.
- A 100×30 view of a busy Codex pane: 4 frames, 24 KB in 3 seconds.

## How it fits together

```
 browser ──HTTPS + WebSocket──► hub ──┐                       ┌── dock ──► herdr   (laptop behind a home router)
                                      ├── gRPC, dial out ──► relay ◄──┼── dock ──► herdr   (office box on a private LAN)
 your terminal: ember-fleet attach ───┘                       └── dock ──► herdr   (cloud VM with a changing IP)
```

Four roles, shipped as **one binary**, `ember-fleet` (`ember` alone clashes with the Ember.js CLI):

| Role | Runs on | Does |
|---|---|---|
| `ember-fleet dock` | Every computer that runs herdr, **once per OS user** (never as root, never reaching other users' sockets) | Dials the relay and keeps the connection up. Serves only the herdr sessions configured for the fleet, always naming the session explicitly (`--session default` included). Uses the socket API, and runs the installed `herdr` for terminal sessions and the client bridge, with a clean environment (no inherited `HERDR_*` variables). Keeps that herdr server running. Enforces this computer's permissions and keeps an audit log. |
| `ember-fleet relay` | One computer everyone can reach, with one inbound port open (443, or 4782). A 1 GB cloud VM is plenty. | Meeting point. Authenticates docks and hubs, tracks who is online, and joins a hub's channel to a dock's. |
| `ember-fleet hub` | Your laptop by default (web UI on 127.0.0.1), or a server with sign-in | A real backend: holds the credentials, keeps a live model of every machine, and translates the browser's WebSocket traffic into gRPC channels to docks. Browsers cannot speak bidirectional gRPC (gRPC-Web supports only unary and server streaming), and pinned certificates belong in native code, not browser fetch. |
| `ember-fleet` CLI | Anywhere | `join`, `status`, `attach` (the stock herdr app for a remote machine), `term` (one remote pane in your terminal). |

The relay and a hub may share a process on a public server, or a laptop hub can dial a separately hosted relay.

**Keeping it running.** By default the dock (and the herdr server it serves) is a launchd *user agent* on macOS, which starts when the user logs in, reconnects after a reboot once they log in, and stops when they log out. Machines that must stay reachable while nobody is logged in get an optional administrator-installed launchd daemon that runs as that (non-root) user. On Linux it is a systemd user service with lingering enabled, which the installer turns on or tells the user how to (it can need an administrator).

### Connections

**1. To the relay: gRPC over TLS, always dialed out.** TLS uses a real certificate when the relay has a domain name, or a pinned self-signed certificate (`sha256:` pin in the link) when it only has an IP. Pin checks fail closed: the client fetches the certificate without credentials, checks the hash, and only then opens the authenticated channel, as Ember Fleet does today. After enrollment, docks and hubs authenticate to the relay with their own keys (TLS client certificates), so no reusable bearer secret crosses the relay; the relay stores public keys only. A machine's identity is its key, never its IP, so a new address is just a reconnect.

**Detecting a dead connection and reconnecting** are separate:
- *Detection.* A connection that closes or resets is noticed at once. A silent blackhole is noticed by HTTP/2 keepalive: a ping every 10 s and a 10 s timeout, so at worst about 20 s. OS network-change notifications (where the platform has them) trigger an immediate reconnect and reset the backoff.
- *Reconnect.* Jittered backoff from 0.5 s, capped at 15 s, reset after a minute of healthy connection.
- *Targets* are measured, not assumed: the 95th percentile from detection (or from connectivity returning) to a usable connection, reported per case in M0.

Port 443 gets through most networks, though restrictive proxies can still block HTTP/2.

**2. End to end, hub ↔ dock.** Each channel the relay joins carries TLS 1.3 between the hub and the dock, each pinning the other's key, so that **the relay cannot read or alter the application traffic**: screens, keystrokes, prompts, actions, and which pane a channel is for. What the relay does see: who is connected, which hub talks to which dock, how much and when, and the one-time enrollment ticket when it is redeemed (see [Pairing](#pairing-and-permissions)).

That guarantee rests on **pairing keys the relay cannot substitute**. The hub's key fingerprint travels in the join link, and the dock's key is authenticated to the hub with a pairing secret from the link that the relay never receives; both are described under [Pairing](#pairing-and-permissions). Ember Fleet's relay sees plaintext today, so all of this is new work. It is part of M1, and the "relay cannot read" claim is made only once it ships and passes the key-substitution test. Until then the documentation says plainly that the relay must be run by the fleet's owner.

**3. Typed channels, not a generic tunnel.** Ember Fleet's relay wire is HTTP-shaped (start, path, response head, a 40 MiB cap per request, both directions ending with the response), which suits REST and SSE but not terminals. The new protocol keeps its proven parts (the dock's outbound control stream, authentication, jittered redial, bounded frames and queues, revocation) and adds:

```proto
// The relay: authenticates, routes, never parses payloads.
package ember.relay.v1;
service Relay {
  rpc Dock(stream DockUp) returns (stream DockDown);        // a dock's control line: hello, pings, "open channel X"
  rpc Accept(stream Chunk) returns (stream Chunk);          // the dock claims channel X
  rpc Open(stream Chunk) returns (stream Chunk);            // a hub opens a channel to a dock it is paired with
  rpc Directory(Empty) returns (stream DirectorySnapshot);  // which of the hub's docks are online, since when
  rpc Enroll(EnrollRequest) returns (EnrollResult);         // redeem a one-time ticket, register the dock's key
  rpc Revoke(DockRef) returns (Empty);                      // and cut that dock's live channels
}
// Open carries only routing: which dock, and an opaque channel id. Chunks carry bytes,
// independent half-close per direction, cancel and error. Per-hub and per-dock channel
// quotas; no lifetime byte cap on terminal channels.
```

The first message *inside* the end-to-end channel says what the channel is for: kind, target, protocol version and the operator acting. The dock checks it there, where the relay cannot change it:

| Channel kind | Carries | Permission |
|---|---|---|
| `inventory` | `Info` (host, OS, herdr version and capabilities, sessions) and allow-listed reads | `inventory.read` |
| `events` | invalidations for the fleet tree | `inventory.read` |
| `observe` | one pane's frames, read only | `terminal.observe` |
| `control` | one pane's frames + input, resize, scroll, release | `terminal.control` (`terminal.takeover` to take it from someone) |
| `action` | one allow-listed herdr method, with targets and arguments validated and responses filtered | `session.manage` for creating, closing and starting; `terminal.control` for prompts and keys |
| `endpoint` | the raw herdr client byte stream for `attach` | `endpoint.attach` |

The dock never forwards arbitrary loopback URLs or runs arbitrary commands. Herdr's ~100 methods are not passed through wholesale: each allowed method is listed with its own validation, and anything else is refused.

**On a lost connection** the dock and hub cancel every channel, mark the machine offline (its last state stays visible, dimmed, and input is disabled), redial, re-authorize, and start fresh: new event subscriptions and reads, new terminal sessions, a new herdr handshake for `attach`. The fleet **never resends anything on its own**, and it reports honestly what happened to work in flight:
- *not sent*: it never left the hub; the user can send it again;
- *sent, outcome unknown*: it may already have run on the machine. The fleet re-reads the machine's state and shows the result, and never retries automatically.

Keystrokes have no per-key acknowledgement, so a terminal that drops mid-typing is reported as *outcome unknown* and the fresh screen shows what landed.

**Direct mode (optional).** On a LAN the hub can reach a dock directly, with the same layers 2 and 3 and no relay. This is the one case where a dock listens on a port, and it is off unless turned on.

### Keeping the tree current

Herdr's JSON API has no global event sequence and no server boot ID, and it keeps only the last 512 events. So for M0 the contract is simple and safe:
- Herdr's API answers one request per socket connection and then closes it. So the dock keeps one long-lived connection per herdr session for `events.subscribe`, and opens a fresh connection for every read and action.
- Events are **coalesced invalidations**: "workspace w1 changed" triggers an authoritative read of what changed (or a full `session.snapshot`). The dock schedules refreshes one at a time, with another queued if events arrive meanwhile. Event payloads are never replayed onto a snapshot.
- On `events_lost`, a reconnect, or any doubt that it is talking to the same server, the dock starts a new **attachment epoch**: it resubscribes, reloads everything, and invalidates every pane and terminal handle from the old epoch.
- **Each epoch is tied to one herdr server process**, identified by its process ID and start time. Before writing an action, the dock checks the peer of the socket it has *just connected* (`SO_PEERCRED` on Linux, `LOCAL_PEERPID` on macOS, plus the process start time). If that is not the epoch's server, or cannot be determined, the action fails closed and the dock starts a new epoch. A restarted server can therefore never receive an action aimed at the old server's `w1:p1`, even if it restarts between the check and the dispatch. If this check proves unworkable on a platform, that platform documents the gap instead of claiming the guarantee.

A smarter merge can come later, once it is shown to be correct.

## Terminals

- The dock runs the machine's own `herdr terminal session observe|control` next to its server. Those commands use herdr's private same-install protocol (22 today), so we do not reimplement terminal serialization in Go.
- Frames pass through with `seq`, `full`, `width`, `height`, and the browser decodes the ANSI into xterm.js (WebGL renderer). There is no "send me a full frame" request, so recovery after a gap is: for a view, open a new observer and wait for its first `full` frame; for control, release and re-acquire only if the dock still holds it, never by taking over someone else's newer controller. Each new stream resets the viewer's sequence. A gap that a `full` frame fills on its own needs nothing more. Resizing is never used as a refresh trick.
- Only panes on screen stream. Closing a view ends its herdr process and releases control.
- Images in panes (Kitty graphics) do not come through this path; the UI says so rather than promise parity.
- **Take control** shows who holds the pane now and asks before taking it over. It starts at the pane's current size: the dock reads it from herdr's pane metrics and layout before attaching (M0 confirms which matches the real terminal), and resizes only when the user asks. It releases on close, role change or expiry.

## The stock herdr app over the relay

Astra traced `herdr --remote` through the source and found a route that needs no SSH and no herdr changes.

**How SSH attach works today.** After discovering, and if needed installing, herdr on the remote, it runs roughly `ssh -C -T [generated config, control socket] TARGET '<ready marker>; exec herdr [--session NAME] remote-client-bridge'`. That bridge makes sure a compatible server is running, connects to its client socket and copies stdin and stdout. The bytes are herdr's binary endpoint protocol (length-prefixed frames, a generation-1 hello and welcome, negotiated codecs), not the JSON API or ANSI. Locally, the SSH launcher then starts the hidden `herdr client` with `HERDR_CLIENT_SOCKET_PATH` pointing at its end.

**What `ember-fleet attach studio [--session agents]` does instead:**
1. Opens a private Unix socket (mode 0600, in a 0700 directory) and an `endpoint` channel to the dock.
2. The dock runs `herdr --session agents remote-client-bridge` (with `--session default` when none is given, and a clean environment) and joins its stdio to the channel. Byte order, half-close, cancellation and separate stderr are preserved. gRPC chunk boundaries need not match herdr's frames, and there is no SSH ready-marker to imitate.
3. Starts the unchanged local herdr:

```bash
env -u HERDR_SOCKET_PATH \
  HERDR_CLIENT_SOCKET_PATH="$private_dir/endpoint.sock" \
  HERDR_REMOTE_KEYBINDINGS=local \
  HERDR_REATTACH_COMMAND='ember-fleet attach studio --session agents' \
  herdr client
```

Do not pass `--session` to the local client: it overrides the socket. `HERDR_REMOTE_KEYBINDINGS=local` also marks the attach as remote (no shared filesystem assumed, saved SSH machines not loaded).

**Run it from an ordinary terminal**, not from inside a herdr pane: `herdr client` refuses to start inside herdr unless the user has turned on herdr's `allow_nested` setting, and the fleet never changes that setting for them.

**Verified so far:** installed herdr 0.9.3, in a terminal with an isolated config and a test listener, connected through that socket and sent a well-formed 446-byte generation-1 hello. **Not yet verified:** a full session through the relay, and a network drop. Herdr's standalone client reports a lost connection; the launcher then offers to reattach while the remote agents keep running.

**Limits.** One remote session per `attach`; relay machines do not appear in herdr's own machine sidebar. `herdr client` and its variables are undocumented, so each herdr release is smoke-tested and the launcher checks versions. This stream carries every keystroke and every command the herdr app can send (herdr even accepts `integration.install` on it), so `endpoint.attach` is a **whole-session, full-trust** permission. It cannot be narrowed to one pane or read-only without parsing herdr's protocol.

**Alternatives we rejected.** Herdr has no setting for its SSH program (`ssh` and `scp` are hard-coded), and a fake `ssh` on PATH would have to emulate discovery, scp and control sockets. An SSH `ProxyCommand` over the relay does work through herdr's generated config, and it fixes reachability, but it still needs sshd and SSH authentication on the target. Keep it as a documented option for people who already use SSH.

**Upstream.** herdr's contributing guide closes unsolicited implementation PRs, so we start a Discussion. The first ask is to document the `herdr client` launch contract; later, a transport type for saved machines, so relay machines can join herdr's sidebar. The project does not depend on either.

## Who is who

- **A location** is station + OS user + herdr session + attachment epoch + pane (and terminal) ID. IDs like `w1:p1` repeat across servers and restarts, so they are never used alone.
- **An agent** is separate from its location: a name, a harness and optionally a mail address, registered on the dock (`agents.toml`). When a pane is reused or moved, the new occupant does not inherit the previous agent's mailbox or permissions.
- Agents that start other agents through herdr show up on their own, from herdr's events.

## Pairing and permissions

**Adding a machine** (the executive case):
1. In the web UI: *Add a machine*. The hub makes a join link holding the relay's address and pin, the hub's key fingerprint, and a fresh 256-bit secret, for example `ember-join://relay.example.com/#v1.<52 characters>`.
2. On that machine, one line in Terminal: `curl -fsSL https://…/install.sh | sh` (or the installer package). It asks for the link (paste it), so the link stays out of process lists and shell history.
3. The dock makes its own key pair. From the link's secret it derives two keys: a *ticket*, which it shows the relay to enroll its public key, and a *pairing key*, which never leaves the dock. It proves to the hub that it owns its public key using the pairing key; the hub checks the proof, and the dock checks the hub's key against the fingerprint from the link. A relay that swaps either key makes pairing fail, and both sides say so.
4. The relay records the ticket as used and keeps only the dock's public key. **The hub decides independently:** it keeps its own record of every link it issued (expiry, cancelled, used), and in one atomic, durable step binds a link to exactly one dock public key. It refuses any later enrollment with that link whatever the relay says, so a relay that ignores its own records gains nothing. The machine appears in the fleet.

The link is **secret until it is used or expires** (24 hours, and the hub can cancel it early): anyone holding it can enroll a machine in its place. Once used, it is worthless. In Ember Fleet today the link carries the station's reusable token for life, and presenting it can replace a live dock. Removing a machine revokes its key and **cuts its live channels**. Keys and certificates have a rotation command. Secrets stay out of command lines, logs and browser storage; credentials the relay does receive (the ticket at enrollment) are compared by hash and never logged.

**The hub's own web page needs sign-in, even on 127.0.0.1**, because any website you visit can try to reach localhost. From M0, the hub prints a one-time sign-in link when it starts; following it sets a session cookie (HttpOnly, SameSite=Strict). The hub rejects HTTP and WebSocket requests whose `Host` or `Origin` is not its own, and every change needs a CSRF token. Multiple users and role management come later; this minimum does not.

**Permissions**, enforced at the dock (the machine's owner decides, in `dock.toml`) and again at the hub. The dock trusts only the authenticated hub and operator identity inside the end-to-end channel, never names supplied by a browser:

| Permission | Allows | Viewer | Operator | Owner |
|---|---|:-:|:-:|:-:|
| `inventory.read` | fleet tree, agent states | ✓ | ✓ | ✓ |
| `terminal.observe` | read-only terminals | ✓ | ✓ | ✓ |
| `terminal.control` | type, prompts, keys, explicit control lease | | ✓ | ✓ |
| `terminal.takeover` | take control from someone else | | ✓ | ✓ |
| `session.manage` | create and close workspaces, tabs and panes; start agents; worktrees | | | ✓ |
| `endpoint.attach` | the whole herdr app (`attach`) | | | ✓ |
| `fleet.admin` | add, remove and re-role machines | | | ✓ |
| `mail.read`, `mail.send` | the operator's mail views, granted separately | | | |

The fleet's own `action` and `inventory` channels never offer `server.stop`, `server.live_handoff`, `server.ssh_agent.register`, plugin or integration install, link or enable, or `client.*`. That is a limit on those channels only. **A terminal and `attach` are full trust**: typing into a terminal is running commands as that user, so anyone with `terminal.control` or `endpoint.attach` can do all of the above another way. Operator is full trust in practice; Viewer is the role that really limits.

**Audit**, at the dock and the hub: who, when, station, session, target, method, channel ID, control taken, taken over and released, enrollments, revocations and outcomes. Raw keystrokes, terminal contents and mail bodies are not logged by default, and an `attach` is logged as one session, not per command. Terminal and mail content is untrusted, so links and clipboard writes from it need a click.

## The web UI

Ported from Ember Fleet's Vue front end and its Marbell Ember look (navy glass, Space Mono, coral as the only thing that glows), without the pi-specific parts.

- **Sidebar:** Fleet → machine → (session) → workspace → tab → pane. Agent rows show name, harness icon, state pill, folder and mail address. Offline machines stay visible, dimmed, with "last seen".
- **Needs you:** every `blocked` agent across the fleet (optionally `done` ones too), with the question on its screen, plus browser notifications. Answering is always a person's click.
- **Pane view:** a live terminal, opened read-only; *Take control* as above; a prompt bar that sends `agent.prompt`; buttons for Esc, Ctrl-C and Enter.
- **Actions:** new workspace in a folder, with *create the folder* (the dock makes it, then opens the workspace); start any agent kind herdr knows, with name and arguments, in a new pane; rename; close with confirmation.
- **Fleet settings:** rename the fleet, the relay link, add a machine, each machine's role and last seen, remove.
- **Phone-sized layout** for checking in and answering a blocked agent, when the hub is reachable from the phone.

## In your terminal

- `ember-fleet attach studio`: the stock herdr app for that machine, through the relay. Run it from an ordinary terminal (see above).
- `ember-fleet term studio/reviewer`: one remote pane in your terminal, built on `terminal session control`, with a detach key like herdr's direct attach. It is our own small renderer, not herdr, so it also works inside a herdr pane.
- An optional herdr plugin with a popup *Fleet* picker that lists agents across machines and opens `term` in a split. Opening `attach` from the plugin either starts an external terminal window or, if the user has turned on herdr's `allow_nested`, a split; it never changes that setting for them.

## Mail (JMAP)

The fleet is not in the message path. Agents send from their own accounts; the hub reads and sends as the **operator**.

- **Correspondence view:** next to the terminal view, not instead of it. Per agent: its address, Inbox and Sent threads with sender, recipients, subject, time, unread count and attachment names. Other agents' mailboxes appear only where the operator has delegated read access. The hub never holds agents' credentials, and mail credentials never cross the relay.
- **Protocol:** works with JMAP servers and accounts that advertise the Mail capability (`urn:ietf:params:jmap:mail`), and for sending the Submission capability (`urn:ietf:params:jmap:submission`) with an Identity. Anything an account does not offer (a shared read-only mailbox, say) is shown as unavailable. Stalwart and Fastmail are the first targets, to be tested, not yet proven. Session discovery (capabilities, accounts, `eventSourceUrl`), then `Mailbox/get`, `Email/query`, `Email/get` and `Thread/get`. Push `StateChange` events trigger `/changes` for the subscribed types that have it, with a full resync on `cannotCalculateChanges`. IDs are scoped by server and account; `Message-ID`, `In-Reply-To` and `References` link copies across accounts. Sending uses `Email/set` + `EmailSubmission/set` ([RFC 8620](https://www.rfc-editor.org/rfc/rfc8620.html), [RFC 8621](https://www.rfc-editor.org/rfc/rfc8621.html)).
- **Honest states:** *sent*, *delivery failed or unknown*, the agent's *harness state*, and *acknowledged* are shown separately. Submission or `$seen` is not acceptance; only the agent's own reply is.

**The gap to close: mail does not wake a harness.** A message arriving does nothing until the agent reads it. Each harness needs its own mail-reading contract (a mail CLI, tool or skill, plus a poll or hook it supports), with its own cursor and de-duplication. The fleet documents and tests that contract per harness, and shows how each agent checks mail: hook, poll every N minutes, or manual. It never types incoming mail into a pane.

Left out of v1: mailbox provisioning, running a mail server, mail administration, and answering approval dialogs automatically.

## What carries over from Ember Fleet

| Kept | Dropped |
|---|---|
| The relay's outbound control stream, presence, pinned TLS, enrollment and revocation, rewritten in Go with key-based relay auth and typed channels | `dpi-backend`, pi agents, jobs, transcripts, attachment store |
| The relay's tests, as a conformance reference for the behaviour we keep | Models, providers, sign-ins, daily model lists |
| Join links, now one-time and with authenticated pairing | The upstream-rebase sync machinery |
| Service installs: launchd user agents, systemd user units with linger | The private Node installer: one static binary needs no runtime |
| The fleet tree, fleet dialog and Marbell Ember theme (Vue) | The hub's "anyone who can reach it drives everything" model |

This repository starts from clean, selected sources: no private configs, station files or history. Copied code and assets keep their copyright and license notices.

## Technology

- **Go** for the `ember-fleet` binary: static builds for macOS and Linux (arm64, x86-64), later Windows; mature grpc-go; the web UI embedded with `go:embed`. No Node, Python or package manager on any machine, which was Ember Fleet's biggest setup cost. Herdr stays a separate install; all boundaries with it (socket JSON, terminal-session NDJSON, raw endpoint bytes) stay opaque, so no Rust is needed.
- **Vue 3 + Vite + xterm.js** for the web UI.
- **Distribution:** GitHub Releases with checksums; a Homebrew tap; a signed and notarized macOS installer, because Gatekeeper warnings stop exactly the non-technical people this is for; `install.sh` for Linux.

```
cmd/ember-fleet/   one binary: relay, dock, hub, join, attach, term, status
internal/relay/    meeting point and its client
internal/dock/     herdr adapters (socket, terminal sessions, endpoint bridge), permissions, audit
internal/hub/      fleet model, HTTP + WebSocket API for the UI, sign-in, JMAP client
internal/pair/     join links, ticket and pairing-key derivation, key proofs
internal/herdr/    socket client, types generated from `herdr api schema`
proto/             ember/relay/v1, ember/dock/v1
web/               Vue + xterm.js
plugin/            the optional herdr plugin
```

## Milestones

**M0, prove the risky parts.** Each is a go/no-go, riskiest first:
1. **The stock herdr app through the relay**: a full `attach` session across a real relay, then a network drop and rejoin, with nothing replayed.
2. **Pairing a machine behind NAT** with a one-time link: it joins outbound and its tree reaches the hub. A relay that substitutes either key is rejected by both sides. Used, expired and cancelled links are refused by the hub even through a test relay that deliberately ignores those records, including after the hub restarts. Revoking the machine closes its open channels.
3. **Observe, then control and release** a remote pane in the browser, with the hub's sign-in, Host/Origin and CSRF checks in place and permissions checked at the dock (a Viewer cannot type by any path). Measure echo latency, direct vs relayed (target: 95th percentile under 30 ms added within one region).
4. **An action whose connection drops after the machine ran it but before the reply arrived**: it is not resent, it is shown as *outcome unknown*, and the re-read state shows what happened. And **a herdr server restart at the moment of dispatch**: the action aimed at the old server fails closed and never reaches the new server's pane with the same ID.
5. **Connection loss, four ways**, measuring detection and reconnect time separately (95th percentile): an explicit network change on a laptop, silent packet loss, waking from sleep, and a relay restart. Open terminals recover from a fresh full frame without a page reload.
6. **Two agents with their own mail accounts**, in different harnesses, exchange a request and a reply, with no prompt typed into either pane by the fleet.

(Controller and observer behaviour is already measured; see [What we measured](#what-we-measured).)

**M1, replaces `herdr --remote` for daily use.** Relay, dock, hub with sign-in, pairing, the end-to-end layer, minimum dock permissions with revoke, fleet tree, Needs you, live terminals with take control, prompt bar, `attach`, `term`, services as described in [Keeping it running](#how-it-fits-together), relay on 443, install for macOS and Linux.

**M2, for everyone else.** Role management in the UI and the audit log viewer, workspace and agent creation (with folder creation), credential rotation, the herdr plugin, notarized macOS installer, Homebrew tap, update command, the optional macOS daemon install.

**M3, mail.** Agent addresses, operator correspondence view, per-harness mail-reading contracts and their status.

**M4, wider.** Windows docks (herdr already runs on Windows), multi-user hub, more than one relay per fleet, a WebSocket fallback for networks that block HTTP/2.

## How we will know it works

- A laptop behind a home router, an office box on a private LAN and a DHCP VM all show live, with **no inbound port or port forwarding on any of them and no SSH**. Only the relay listens.
- Reconnect times meet the targets set from M0's measurements, reported as percentiles per failure case. The fleet never resends input or actions on its own.
- A non-technical person on a Mac gets from link to online in under 5 minutes, with one Terminal line or a double-clicked installer.
- A relay that tries to swap keys during pairing is detected and refused. The relay never logs credentials, and a memory dump taken mid-session contains none of the session's test text (supporting evidence, alongside the key-substitution test).
- Typing through the relay feels local: under 30 ms added echo latency in-region.
- Removing a machine cuts its channels at once, and its old join link stays useless.
- A Viewer cannot type, by any path: terminal, prompt bar or action. `attach` is for Owners only.

## Risks

| Risk | Plan |
|---|---|
| Herdr's API changes | Discover capabilities per server; generate types from `herdr api schema`; CI against herdr stable and preview. |
| `herdr client` is undocumented | Version checks and smoke tests per herdr release; `term` as the fallback; the upstream Discussion. |
| Control is not an input lock in herdr | Permissions enforced at the dock for every input path; show who holds control. |
| A controller resizes the owner's pane | Take control at the current size; resize only when asked; release promptly. |
| A malicious relay swaps keys or replays used links | Hub fingerprint in the link, a pairing key the relay never sees, the hub's own record of every link, and tests that prove rejection. |
| The relay is a single point of failure | Small and quick to restart; docks may list two relays; LAN-direct mode. |
| Control means shell access | End-to-end layer, per-machine permissions, audit, one-time links, revocation that cuts live channels. |
| Mail does not wake harnesses | Per-harness mail contracts, shown honestly; no prompt injection. |
| Networks that block gRPC or odd ports | Relay on 443 with ALPN `h2`; WebSocket fallback in M4. |

## Open questions

1. **Where the hub runs:** your laptop only (simplest), or a server so a phone can reach it (pulls the multi-user sign-in forward)?
2. **Binary name:** `ember-fleet` is used above; `ember` clashes with Ember.js.
3. **Windows** docks early, or later?
4. **Which JMAP server** the agents use (Stalwart, Fastmail, other); it decides how the hub signs in.
5. **License:** MIT is proposed (durable-pi, which Ember Fleet came from, is MIT; herdr is Apache-2.0).
6. **Machines that must stay online with nobody logged in** (a Mac mini in a closet): is the optional administrator-installed daemon needed in M1?

## Evidence

Herdr source at the [v0.9.3 release commit](https://github.com/herdrdev/herdr/tree/7b116c05bfda646af39d2524c54e70c751f57ee8):

| Finding | Where |
|---|---|
| Hidden `herdr client` and its socket override | `src/main.rs` (~564), `src/server/socket_paths.rs`, `src/client/mod.rs` (~217), `src/client/handshake.rs` (~35) |
| The nested-herdr guard | `src/main.rs` (~447–472) |
| What SSH attach runs remotely; `remote-client-bridge` | `src/remote/attach.rs` (~375, 513, 1205, 3005, 3088, 3324), `src/remote/host.rs`, `src/platform/unix_common.rs` (~147) |
| Endpoint framing and generation-1 handshake | `src/protocol/wire.rs` (~1670), `src/protocol/endpoint.rs` |
| The endpoint accepts `integration.install` | `src/server/client_commands.rs` (~15–19) |
| `ssh` and `scp` hard-coded | `src/remote/attach.rs` (~802, 1786, 2904) |
| 512 retained events, no replay cursor, no boot ID in the JSON snapshot | `src/api/event_hub.rs` (~19), `src/api/schema/events.rs`, `src/api/schema/session.rs` (~9), `src/api/subscriptions.rs` |
| One request per API connection | `src/api/server.rs` (~296–310, 456–484), `src/api/client.rs` (~55–60) |
| Terminal sessions: same-install protocol, no full-frame request | `src/client/terminal_sessions.rs` (~160–201) |
| Controller check vs `pane.send_text` | `src/server/headless.rs` (~1739), `src/app/api/panes.rs` (~1905) |
| Contribution policy | `CONTRIBUTING.md` |

Line numbers are approximate pointers into that commit. Other references: [gRPC-Web streaming support](https://github.com/grpc/grpc-web#streaming-support), [WebSocket Origin](https://www.rfc-editor.org/rfc/rfc6455.html#section-10.2), [launchd jobs](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html), [OpenSSH ProxyCommand](https://man.openbsd.org/ssh_config#ProxyCommand).
