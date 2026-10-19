# Herdr Ember Fleet

See and steer every coding agent on every computer you own, whatever harness it runs, from one web page or your own terminal. No SSH, no VPN, no port forwarding on the computers running agents.

> **Status: planning.** Nothing is built yet. The plan is in [PLAN.md](PLAN.md); comments and issues welcome.

```
 browser ──► hub ──┐                       ┌── dock ──► herdr   (laptop behind a home router)
                   ├── dial out ──► relay ◄─┼── dock ──► herdr   (office box on a private LAN)
 terminal ─────────┘                       └── dock ──► herdr   (cloud VM with a changing IP)
```

What it is meant to do:

- **Work with any harness.** Agents run in [herdr](https://herdr.dev) panes: Claude Code, Codex, omp, Grok, gemini, opencode, your own bots. The fleet reads herdr's state, so it works with all of them and runs none of them itself.
- **Reach machines SSH cannot.** Every computer connects *out* to a small relay over gRPC; only the relay needs a reachable port. Laptops that change networks, machines behind NAT or on private LANs, and VMs with DHCP addresses all just reconnect. Nobody needs SSH keys.
- **Use the herdr app you already have.** `ember-fleet attach <machine>` opens the unchanged herdr app on a remote machine through the relay.
- **Keep the relay out of your traffic.** Hub and machines encrypt end to end, with keys paired so the relay cannot swap them. Until that ships and passes its tests, run your own relay.
- **Watch first, step in when needed.** Live terminals open read-only; *Take control* to type. A "needs you" list shows every agent waiting on a question, across the fleet.
- **Let each machine set its own rules.** Viewer, operator or owner, enforced on that machine, with a local audit log.
- **Join with one link.** Adding a computer takes a one-time link and one command; the link is useless once used.
- **Stay out of the agents' conversations.** Agents keep their own JMAP mail accounts for talking to each other and to you. The fleet can show those threads, but never types mail into a pane or answers for an agent.

Built on lessons from Ember Fleet, a private fleet manager for [durable-pi](https://github.com/disler/durable-pi) agents whose gRPC relay this project generalizes, and dressed in [Marbell Ember](https://github.com/vantasnerdan/marbell-ember).

## License

MIT, see [LICENSE](LICENSE).
