// `npm run dev:mock`: the hub's web API from synthetic data, inside Vite's dev server.
//
//   HTTP   /api/me, signin, signout, fleet, action, links, docks, tokens
//   WS     /api/live (hello, machine, removed) and /api/term (frames; input echoes in control)
//
// It checks what the real hub checks that the page is responsible for: the CSRF header
// on every change, the CSRF subprotocol on every socket, and the session epoch and
// terminal ID on /api/term. Sign in at /signin#demo; /signin#used shows a used link.
//
// Groups (/api/groups…) and uploads (/api/upload) are mocked too: an upload's body is read
// and dropped, and the answer is a made-up path under the agent user's
// ~/.cache/ember-fleet/uploads/<date>/. A file name with "fail" in it fails, to try retry.
//
// Mock-only helpers, for trying failure paths:
//   POST /api/mock/restart?dock=<id>&session=<name>   herdr restarts: new epoch, new terminal ids
//   POST /api/mock/toggle?dock=<id>                   the dock goes offline / comes back
//   POST /api/mock/drop                               every terminal socket drops without a word

import type { IncomingMessage, ServerResponse } from "node:http";
import type { Duplex } from "node:stream";
import { randomBytes } from "node:crypto";
import type { Plugin } from "vite";
import type { Group, Member } from "../src/lib/types.ts";
import { WebSocketServer, type WebSocket } from "ws";
import {
  joinedMachine,
  machineOut,
  makeFleet,
  newEpoch,
  screenOf,
  setStatus,
  tick,
  typeInto,
  type SimDock,
  type SimMachine,
  type SimPane,
  type SimSession,
} from "./fleet.ts";
import { fullFrame, partialFrame, renderScreen } from "./screen.ts";

const CSRF = "mock-csrf-7f3a9c";
const SIGNED_OUT = "ember_mock_out";
const PROTOCOL = "ember.v1";

interface Link {
  id: string;
  name: string;
  created: number;
  expires: number;
  state: "pending" | "used" | "expired" | "cancelled";
  dock?: string;
}

interface Viewer {
  ws: WebSocket;
  dock: SimDock;
  host: string;
  session: SimSession;
  pane: SimPane;
  mode: "observe" | "control";
  cols: number;
  rows: number;
  seq: number;
  last: string[];
}

export function mockApi(): Plugin {
  const machines: SimMachine[] = makeFleet();
  const links: Link[] = [];
  const live = new Set<WebSocket>();
  let fleetName = "Home fleet";
  let groupSeq = 3;
  let groups: Group[] = [
    {
      id: "g1",
      name: "Coding",
      color: "ember",
      members: [
        { kind: "pane", dock: "studio-agent-one", session: "default", pane: "w1:p1" },
        { kind: "dock", dock: "laptop-sam" },
      ],
    },
    {
      id: "g2",
      name: "Physics",
      color: "sky",
      members: [
        { kind: "pane", dock: "studio-admin", session: "night-shift", pane: "w1:p1" },
        { kind: "pane", dock: "studio-agent-two", session: "default", pane: "w2:p1" },
        // a pane herdr has not restored (yet): shown as not running
        { kind: "pane", dock: "studio-agent-one", session: "default", pane: "w3:p1" },
      ],
    },
    { id: "g3", name: "Burst", color: "", members: [] },
  ];
  const viewers = new Set<Viewer>();
  const controllers = new Map<SimPane, Viewer>();

  // ---------------------------------------------------------------- lookups

  const find = (dockId: string) => {
    for (const m of machines) for (const d of m.docks) if (d.id === dockId) return { m, d };
    return undefined;
  };
  const broadcast = (msg: unknown) => {
    const text = JSON.stringify(msg);
    for (const ws of live) if (ws.readyState === ws.OPEN) ws.send(text);
  };
  const changed = (m: SimMachine) => broadcast({ type: "machine", machine: machineOut(m) });

  // ---------------------------------------------------------------- frames

  const sizeOf = (v: Viewer) =>
    v.mode === "control" ? v.pane.real : { cols: v.cols || 100, rows: v.rows || 30 };

  function send(v: Viewer, full: boolean) {
    if (v.ws.readyState !== v.ws.OPEN) return;
    const { cols, rows } = sizeOf(v);
    const screen = renderScreen(screenOf(v.pane, v.dock, v.host), cols, rows);
    const showCursor = v.mode === "control";
    const data = full
      ? fullFrame(screen.rows, screen.cursor, showCursor)
      : partialFrame(screen.rows, v.last, screen.cursor, showCursor);
    if (!data) return;
    v.last = screen.rows;
    v.ws.send(
      JSON.stringify({
        type: "frame",
        seq: ++v.seq,
        full,
        width: cols,
        height: rows,
        data: Buffer.from(data, "utf8").toString("base64"),
      }),
    );
  }
  const refresh = (p: SimPane) => {
    for (const v of viewers) if (v.pane === p) send(v, false);
  };

  // ---------------------------------------------------------------- the clock

  let beat = 0;
  setInterval(() => {
    const now = Date.now();
    beat++;
    for (const m of machines) {
      let dirty = false;
      for (const d of m.docks) {
        if (!d.online) continue;
        for (const s of d.sessions)
          for (const p of s.panes) {
            const r = tick(p, now);
            if (r.snapshot) dirty = true;
            if (r.screen) refresh(p);
          }
      }
      if (dirty) changed(m);
    }
    // herdr sends a full frame now and then
    if (beat % 20 === 0) for (const v of viewers) send(v, true);
    for (const l of links) if (l.state === "pending" && l.expires < now) l.state = "expired";
  }, 1000);

  // ---------------------------------------------------------------- HTTP

  const json = (res: ServerResponse, status: number, body: unknown) => {
    res.statusCode = status;
    res.setHeader("Content-Type", "application/json");
    res.setHeader("Cache-Control", "no-store");
    res.end(JSON.stringify(body));
  };
  const signedIn = (req: IncomingMessage) => !(req.headers.cookie ?? "").includes(`${SIGNED_OUT}=1`);
  const readBody = (req: IncomingMessage) =>
    new Promise<Record<string, unknown>>((resolve) => {
      let raw = "";
      req.on("data", (c: Buffer) => (raw += c.toString("utf8")));
      req.on("end", () => {
        try {
          resolve(raw ? (JSON.parse(raw) as Record<string, unknown>) : {});
        } catch {
          resolve({});
        }
      });
    });
  const later = (ms: number) => new Promise((r) => setTimeout(r, ms));

  async function action(body: Record<string, unknown>) {
    await later(120 + Math.random() * 180);
    const found = find(String(body.dock));
    if (!found) return { ok: false, error: "no such machine", outcome: "not_sent" };
    const { m, d } = found;
    if (!d.online) return { ok: false, error: "offline", outcome: "not_sent" };
    if (d.role === "viewer") return { ok: false, error: "forbidden", outcome: "not_sent" };
    const s = d.sessions.find((x) => x.name === body.session);
    if (!s || !s.online) return { ok: false, error: "herdr is not running", outcome: "not_sent" };
    if (s.epoch !== body.epoch) return { ok: false, error: "stale", outcome: "not_sent" };
    const params = (body.params ?? {}) as Record<string, unknown>;
    const p = s.panes.find((x) => x.id === params.pane_id);
    switch (body.method) {
      case "agent.prompt": {
        if (!p) return { ok: false, error: "pane_not_found", outcome: "not_sent" };
        if (!p.agent) return { ok: false, error: "no agent in this pane", outcome: "not_sent" };
        if (p.status === "blocked") return { ok: false, error: "agent_blocked", outcome: "not_sent" };
        const text = String(params.text ?? "");
        p.history.push({ kind: "prompt", text });
        p.title = text.slice(0, 60);
        setStatus(p, "working", 25_000);
        p.revision++;
        refresh(p);
        changed(m);
        if (text.includes("#unknown")) return { ok: false, error: "no answer from herdr in time", outcome: "unknown" };
        return { ok: true, result: { accepted: true } };
      }
      case "pane.send_keys": {
        if (!p) return { ok: false, error: "pane_not_found", outcome: "not_sent" };
        const keys = (params.keys as string[] | undefined) ?? [];
        const map: Record<string, string> = { esc: "\x1b", "ctrl+c": "\x03", enter: "\r", up: "\x1b[A", down: "\x1b[B", tab: "\t" };
        const snap = typeInto(p, keys.map((k) => map[k] ?? "").join(""));
        refresh(p);
        if (snap) changed(m);
        return { ok: true, result: null };
      }
      case "pane.rename":
        if (!p) return { ok: false, error: "pane_not_found", outcome: "not_sent" };
        p.label = String(params.label ?? "");
        p.revision++;
        changed(m);
        return { ok: true, result: null };
      case "workspace.rename": {
        const w = s.workspaces.find((x) => x.id === params.workspace_id);
        if (!w) return { ok: false, error: "no such workspace", outcome: "not_sent" };
        w.label = String(params.label ?? "");
        changed(m);
        return { ok: true, result: null };
      }
      case "tab.rename": {
        const t = s.workspaces.flatMap((w) => w.tabs).find((x) => x.id === params.tab_id);
        if (!t) return { ok: false, error: "no such tab", outcome: "not_sent" };
        t.label = String(params.label ?? "");
        changed(m);
        return { ok: true, result: null };
      }
      case "pane.close": {
        if (!p) return { ok: false, error: "pane_not_found", outcome: "not_sent" };
        s.panes = s.panes.filter((x) => x !== p);
        for (const v of [...viewers]) if (v.pane === p) end(v, "closed", "pane closed");
        changed(m);
        return { ok: true, result: null };
      }
      default:
        return { ok: false, error: "method not allowed", outcome: "not_sent" };
    }
  }

  async function handle(req: IncomingMessage, res: ServerResponse) {
    const url = new URL(req.url ?? "/", "http://mock");
    const path = url.pathname;
    const method = req.method ?? "GET";

    if (path === "/api/me" && method === "GET") {
      return json(res, 200, signedIn(req)
        ? { signedIn: true, csrf: CSRF, fleet: fleetName, hub: "SHA256:k7Qe…9fXw", version: "0.1.0-mock" }
        : { signedIn: false, csrf: "", fleet: "", hub: "", version: "0.1.0-mock" });
    }
    if (path === "/api/signin" && method === "POST") {
      const { token } = await readBody(req);
      await later(400);
      if (token === "demo" || token === "mock") {
        res.setHeader("Set-Cookie", `${SIGNED_OUT}=0; Path=/; HttpOnly; SameSite=Strict`);
        return json(res, 200, { ok: true });
      }
      return json(res, token === "used" ? 410 : 401, { error: token === "used" ? "link already used" : "invalid or expired link" });
    }
    if (!signedIn(req)) return json(res, 401, { error: "signed out" });
    if (method !== "GET" && req.headers["x-ember-csrf"] !== CSRF) return json(res, 403, { error: "csrf" });

    if (path === "/api/signout" && method === "POST") {
      res.setHeader("Set-Cookie", `${SIGNED_OUT}=1; Path=/; HttpOnly; SameSite=Strict`);
      return json(res, 200, { ok: true });
    }
    if (path === "/api/fleet" && method === "GET") return json(res, 200, { machines: machines.map(machineOut) });
    if (path === "/api/action" && method === "POST") return json(res, 200, await action(await readBody(req)));

    if (path === "/api/links" && method === "GET") return json(res, 200, { links });
    if (path === "/api/links" && method === "POST") {
      const name = String((await readBody(req)).name ?? "").trim() || "machine";
      const id = randomBytes(6).toString("hex");
      const secret = randomBytes(18).toString("base64url");
      const link: Link = { id, name, created: Date.now(), expires: Date.now() + 24 * 3600_000, state: "pending" };
      links.unshift(link);
      const url = `ember-link://relay.example.net:4782/${id}?pin=sha256:3b9f…c21e#${secret}`;
      // the machine "joins" a little later
      setTimeout(() => {
        if (link.state !== "pending") return;
        const host = name.toLowerCase().replace(/[^a-z0-9-]+/g, "-").replace(/^-+|-+$/g, "") || "machine";
        const m = joinedMachine(machines.some((x) => x.host === host) ? `${host}-2` : host);
        machines.push(m);
        link.state = "used";
        link.dock = m.docks[0].id;
        changed(m);
      }, 12_000);
      return json(res, 200, {
        id,
        link: url,
        command: `curl -fsSL https://hub.example.net/install.sh | sh -s -- '${url}'`,
        expires: link.expires,
      });
    }
    const linkMatch = /^\/api\/links\/([^/]+)$/.exec(path);
    if (linkMatch && method === "DELETE") {
      const l = links.find((x) => x.id === linkMatch[1]);
      if (!l) return json(res, 404, { error: "no such link" });
      if (l.state === "pending") l.state = "cancelled";
      return json(res, 200, { ok: true });
    }
    const dockMatch = /^\/api\/docks\/([^/]+)(\/rename)?$/.exec(path);
    if (dockMatch) {
      const found = find(decodeURIComponent(dockMatch[1]));
      if (!found) return json(res, 404, { error: "no such machine" });
      const { m, d } = found;
      if (dockMatch[2] && method === "POST") {
        d.name = String((await readBody(req)).name ?? d.name);
        changed(m);
        return json(res, 200, { ok: true });
      }
      if (!dockMatch[2] && method === "DELETE") {
        m.docks = m.docks.filter((x) => x !== d);
        for (const v of [...viewers]) if (v.dock === d) end(v, "error", "machine removed");
        if (!m.docks.length) {
          machines.splice(machines.indexOf(m), 1);
          broadcast({ type: "removed", host: m.host });
        } else changed(m);
        return json(res, 200, { ok: true });
      }
    }
    if (path === "/api/fleet" && method === "POST") {
      const name = String((await readBody(req)).name ?? "").trim();
      if (!name || name.length > 40 || /[\u0000-\u001f\u007f]/.test(name))
        return json(res, 400, { ok: false, error: "A fleet name is 1 to 40 characters, with no control characters" });
      fleetName = name;
      broadcast({ type: "fleet", name });
      return json(res, 200, { ok: true, name });
    }

    // ---------------------------------------------------------------- groups
    const sendGroups = () => broadcast({ type: "groups", groups });
    const memberKey = (m: Member) => (m.kind === "dock" ? `d:${m.dock}` : `p:${m.dock}/${m.session}/${m.pane}`);
    const validMember = (m: unknown): m is Member => {
      const x = m as Partial<Member> & Record<string, unknown>;
      if (!x || typeof x.dock !== "string" || !x.dock) return false;
      if (x.kind === "dock") return true;
      return x.kind === "pane" && typeof x.session === "string" && typeof x.pane === "string";
    };
    const nameError = (name: unknown) =>
      typeof name !== "string" || !name.trim() || name.trim().length > 40 ? "A group name is 1 to 40 characters" : "";
    const COLORS = ["", "ember", "amber", "rose", "teal", "sky", "violet", "slate"];

    if (path === "/api/groups" && method === "GET") return json(res, 200, { groups });
    if (path === "/api/groups" && method === "POST") {
      const body = await readBody(req);
      const err = nameError(body.name);
      if (err) return json(res, 400, { ok: false, error: err });
      const color = String(body.color ?? "");
      if (!COLORS.includes(color)) return json(res, 400, { ok: false, error: "unknown colour" });
      const group: Group = { id: `g${++groupSeq}`, name: String(body.name).trim(), color, members: [] };
      groups = [...groups, group];
      sendGroups();
      return json(res, 200, { group });
    }
    if (path === "/api/groups/move" && method === "POST") {
      const body = await readBody(req);
      if (!validMember(body.member)) return json(res, 400, { ok: false, error: "bad member" });
      const member: Member =
        body.member.kind === "dock"
          ? { kind: "dock", dock: body.member.dock }
          : { kind: "pane", dock: body.member.dock, session: body.member.session, pane: body.member.pane };
      const to = body.group === null ? null : String(body.group ?? "");
      if (to !== null && !groups.some((g) => g.id === to)) return json(res, 404, { ok: false, error: "no such group" });
      const key = memberKey(member);
      groups = groups.map((g) => ({ ...g, members: g.members.filter((m) => memberKey(m) !== key) }));
      if (to) {
        const g = groups.find((x) => x.id === to)!;
        const at = typeof body.index === "number" ? Math.max(0, Math.min(body.index, g.members.length)) : g.members.length;
        g.members = [...g.members.slice(0, at), member, ...g.members.slice(at)];
      }
      sendGroups();
      return json(res, 200, { groups });
    }
    if (path === "/api/groups/reorder" && method === "POST") {
      const ids = (await readBody(req)).ids;
      if (!Array.isArray(ids)) return json(res, 400, { ok: false, error: "ids must be a list" });
      const byId = new Map(groups.map((g) => [g.id, g]));
      const next = ids.map((id) => byId.get(String(id))).filter((g): g is Group => !!g);
      for (const g of groups) if (!next.includes(g)) next.push(g);
      groups = next;
      sendGroups();
      return json(res, 200, { groups });
    }
    const groupMatch = /^\/api\/groups\/([^/]+)$/.exec(path);
    if (groupMatch) {
      const id = decodeURIComponent(groupMatch[1]);
      const g = groups.find((x) => x.id === id);
      if (!g) return json(res, 404, { ok: false, error: "no such group" });
      if (method === "PATCH") {
        const body = await readBody(req);
        if (body.name !== undefined) {
          const err = nameError(body.name);
          if (err) return json(res, 400, { ok: false, error: err });
        }
        if (body.color !== undefined && !COLORS.includes(String(body.color)))
          return json(res, 400, { ok: false, error: "unknown colour" });
        const group: Group = {
          ...g,
          ...(body.name !== undefined ? { name: String(body.name).trim() } : {}),
          ...(body.color !== undefined ? { color: String(body.color) } : {}),
        };
        groups = groups.map((x) => (x.id === id ? group : x));
        sendGroups();
        return json(res, 200, { group });
      }
      if (method === "DELETE") {
        groups = groups.filter((x) => x.id !== id);
        sendGroups();
        return json(res, 200, { ok: true });
      }
    }

    // ---------------------------------------------------------------- uploads
    if (path === "/api/upload" && method === "POST") {
      const found = find(url.searchParams.get("dock") ?? "");
      if (!found || !found.d.online) return json(res, 404, { ok: false, error: "that machine is offline" });
      if (found.d.role === "viewer") return json(res, 403, { ok: false, error: "forbidden" });
      let name = "file";
      try {
        name = decodeURIComponent(String(req.headers["x-ember-filename"] ?? "file")) || "file";
      } catch {
        // keep "file"
      }
      const LIMIT = 50 * 1024 * 1024;
      let size = 0;
      let over = false;
      await new Promise<void>((done) => {
        req.on("data", (c: Buffer) => {
          size += c.length;
          if (size > LIMIT) over = true;
        });
        req.on("end", () => done());
        req.on("error", () => done());
      });
      if (over) return json(res, 413, { ok: false, error: "over the 50 MB limit" });
      await later(500 + Math.min(2500, size / 4000));
      if (/fail/i.test(name)) return json(res, 500, { ok: false, error: "disk full (mock)" });
      const safe = name.replace(/[^\w.@+-]+/g, "-").slice(-80) || "file";
      const day = new Date().toISOString().slice(0, 10);
      const home = found.d.os === "darwin" ? `/Users/${found.d.user}` : `/home/${found.d.user}`;
      const path = `${home}/.cache/ember-fleet/uploads/${day}/${randomBytes(3).toString("hex")}-${safe}`;
      return json(res, 200, { ok: true, path, name, size });
    }

    if (path === "/api/tokens" && method === "POST") {
      await readBody(req);
      return json(res, 200, { token: `eft_${randomBytes(24).toString("base64url")}` });
    }

    // mock-only helpers
    if (path === "/api/mock/restart" && method === "POST") {
      const found = find(url.searchParams.get("dock") ?? "");
      const s = found?.d.sessions.find((x) => x.name === (url.searchParams.get("session") ?? "default"));
      if (!found || !s) return json(res, 404, { error: "no such session" });
      s.epoch = newEpoch();
      for (const p of s.panes) p.terminal = `term_${randomBytes(8).toString("hex")}`;
      for (const v of [...viewers]) if (v.session === s) v.ws.close(1011, "herdr restarted");
      changed(found.m);
      return json(res, 200, { ok: true, epoch: s.epoch });
    }
    if (path === "/api/mock/toggle" && method === "POST") {
      const found = find(url.searchParams.get("dock") ?? "");
      if (!found) return json(res, 404, { error: "no such machine" });
      found.d.online = !found.d.online;
      found.d.lastSeen = Date.now();
      if (!found.d.online) for (const v of [...viewers]) if (v.dock === found.d) end(v, "error", "machine offline");
      changed(found.m);
      return json(res, 200, { ok: true, online: found.d.online });
    }
    if (path === "/api/mock/drop" && method === "POST") {
      for (const v of [...viewers]) v.ws.terminate();
      return json(res, 200, { ok: true });
    }
    return json(res, 404, { error: "not found" });
  }

  // ---------------------------------------------------------------- WebSockets

  const wss = new WebSocketServer({
    noServer: true,
    handleProtocols: (protocols) => (protocols.has(PROTOCOL) ? PROTOCOL : false),
  });

  function refuse(socket: Duplex, status: string) {
    socket.write(`HTTP/1.1 ${status}\r\nConnection: close\r\n\r\n`);
    socket.destroy();
  }

  function end(v: Viewer, state: "closed" | "error" | "busy", reason?: string) {
    if (v.ws.readyState === v.ws.OPEN) v.ws.send(JSON.stringify({ type: "status", state, reason }));
    v.ws.close(1000);
    viewers.delete(v);
    if (controllers.get(v.pane) === v) controllers.delete(v.pane);
  }

  function openTerm(ws: WebSocket, url: URL) {
    const q = url.searchParams;
    const status = (state: string, reason?: string) => ws.send(JSON.stringify({ type: "status", state, reason }));
    const found = find(q.get("dock") ?? "");
    if (!found || !found.d.online) {
      status("error", "machine offline");
      return ws.close(1000);
    }
    const { m, d } = found;
    const s = d.sessions.find((x) => x.name === q.get("session"));
    if (!s || !s.online) {
      status("error", "herdr is not running");
      return ws.close(1000);
    }
    const p = s.panes.find((x) => x.id === q.get("pane"));
    if (s.epoch !== q.get("epoch") || (p && p.terminal !== q.get("terminal"))) {
      status("error", "stale");
      return ws.close(1000);
    }
    if (!p) {
      status("error", "pane not found");
      return ws.close(1000);
    }
    const mode = q.get("mode") === "control" ? "control" : "observe";
    const v: Viewer = {
      ws,
      dock: d,
      host: m.host,
      session: s,
      pane: p,
      mode,
      cols: Math.max(0, Number(q.get("cols")) || 0),
      rows: Math.max(0, Number(q.get("rows")) || 0),
      seq: 0,
      last: [],
    };
    if (mode === "control") {
      if (d.role === "viewer") {
        status("error", "your role here is viewer");
        return ws.close(1000);
      }
      const takeover = q.get("takeover") === "1";
      const holder = controllers.get(p);
      if ((holder || p.heldBy) && !takeover) {
        status("busy", holder ? "another browser" : p.heldBy);
        return ws.close(1000);
      }
      if (holder) end(holder, "closed", "terminal attach taken over");
      p.heldBy = undefined;
      controllers.set(p, v);
      if (v.cols && v.rows) p.real = { cols: v.cols, rows: v.rows };
    }
    viewers.add(v);
    status("open");
    send(v, true);

    ws.on("message", (raw) => {
      let msg: { type?: string; text?: string; cols?: number; rows?: number; direction?: string; lines?: number };
      try {
        msg = JSON.parse(raw.toString()) as typeof msg;
      } catch {
        return;
      }
      if (msg.type === "resize" && msg.cols && msg.rows) {
        if (v.mode === "control") {
          p.real = { cols: Math.min(400, msg.cols), rows: Math.min(200, msg.rows) };
          for (const o of viewers) if (o.pane === p && o.mode === "control") send(o, true);
        } else {
          // the dock restarts the observer at the new size: a fresh stream, a fresh full frame
          v.cols = msg.cols;
          v.rows = msg.rows;
          v.seq = 0;
          send(v, true);
        }
      } else if (v.mode !== "control") return;
      else if (msg.type === "input" && typeof msg.text === "string") {
        if (typeInto(p, msg.text)) changed(m);
        refresh(p);
      } else if (msg.type === "scroll") {
        const n = Math.max(1, Math.min(50, Number(msg.lines) || 1));
        p.scroll = Math.max(0, p.scroll + (msg.direction === "up" ? n : -n));
        refresh(p);
      } else if (msg.type === "release") end(v, "closed", "released");
    });
    ws.on("close", () => {
      viewers.delete(v);
      if (controllers.get(p) === v) controllers.delete(p);
    });
  }

  function upgrade(req: IncomingMessage, socket: Duplex, head: Buffer) {
    const url = new URL(req.url ?? "/", "http://mock");
    if (url.pathname !== "/api/live" && url.pathname !== "/api/term") return;
    const offered = String(req.headers["sec-websocket-protocol"] ?? "")
      .split(",")
      .map((s) => s.trim());
    if (!signedIn(req)) return refuse(socket, "401 Unauthorized");
    if (!offered.includes(PROTOCOL) || !offered.includes(`csrf.${CSRF}`)) return refuse(socket, "403 Forbidden");
    wss.handleUpgrade(req, socket, head, (ws) => {
      if (url.pathname === "/api/live") {
        live.add(ws);
        ws.on("close", () => live.delete(ws));
        ws.send(JSON.stringify({ type: "hello", fleet: { machines: machines.map(machineOut) }, groups }));
      } else openTerm(ws, url);
    });
  }

  return {
    name: "ember-mock-hub",
    configureServer(server) {
      server.httpServer?.on("upgrade", upgrade);
      server.middlewares.use((req, res, next) => {
        if (!req.url?.startsWith("/api/")) return next();
        handle(req, res).catch((e: unknown) => json(res, 500, { error: String(e) }));
      });
    },
  };
}
