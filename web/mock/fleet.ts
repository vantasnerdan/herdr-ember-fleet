// The mock hub's fleet: made-up machines, users and agents whose states change on a
// schedule, so the page has something alive to show. Nothing here is real data.

import type { Entry, ScreenModel } from "./screen.ts";

type Status = "working" | "idle" | "blocked" | "done" | "unknown";
type Role = "owner" | "operator" | "viewer";

export interface SimPane {
  id: string;
  ws: string;
  tab: string;
  agent?: string;
  label?: string;
  cwd: string;
  title: string;
  status: Status;
  /** [state, seconds] steps the pane cycles through on its own */
  cycle?: [Status, number][];
  step: number;
  stepAt: number;
  /** a person acted: the cycle waits until then */
  holdUntil: number;
  workSince: number;
  terminal: string;
  revision: number;
  seq: number;
  history: Entry[];
  input: string;
  question?: { title: string; options: string[]; choice: number };
  real: { cols: number; rows: number };
  scroll: number;
  /** another controller holds it (for showing "busy") */
  heldBy?: string;
}

export interface SimTab {
  id: string;
  label: string;
}
export interface SimWorkspace {
  id: string;
  label: string;
  tabs: SimTab[];
}
export interface SimSession {
  name: string;
  epoch: string;
  online: boolean;
  error?: string;
  workspaces: SimWorkspace[];
  panes: SimPane[];
}
export interface SimDock {
  id: string;
  name: string;
  user: string;
  os: string;
  arch: string;
  herdr: string;
  online: boolean;
  lastSeen: number;
  role: Role;
  sessions: SimSession[];
}
export interface SimMachine {
  host: string;
  docks: SimDock[];
}

const HARNESS_NAMES: Record<string, string> = {
  claude: "Claude Code",
  codex: "Codex",
  omp: "omp",
  grok: "Grok",
  gemini: "Gemini",
  opencode: "opencode",
  amp: "Amp",
};

let terminalSeq = 0x5a10;
const term = () => `term_${(terminalSeq++).toString(16)}${Math.random().toString(16).slice(2, 10)}`;
let epochSeq = 1;
export const newEpoch = () => `ep-${(epochSeq++).toString(36)}-${Math.random().toString(36).slice(2, 8)}`;

// ---------------------------------------------------------------- invented work

const PROMPTS = [
  "Refactor the export job to stream rows instead of loading them all first",
  "Add retries with backoff to the webhook sender, capped at five attempts",
  "Write tests for the invoice rounding rules, including the half-cent cases",
  "Why does the nightly build take forty minutes? Find the slow step",
  "Tidy the README install section and check every command still works",
  "Move the feature flags into one typed module",
];
const SAYS = [
  "The export reads every row before it writes any. A cursor keeps memory flat and starts the file sooner.",
  "Two call sites build their retry policy by hand. I'll route both through one helper so the cap lives in one place.",
  "The rounding rule truncates instead of rounding half to even, which is why the totals drift by a cent.",
  "Most of the build time is the image step re-downloading the toolchain; caching that layer should help a lot.",
  "All tests pass. I left the old code path behind a flag so you can compare outputs on real data.",
];
const TOOLS = [
  "Read src/export/job.ts (212 lines)",
  "Search “retryPolicy” in src (6 matches)",
  "Edit src/webhooks/send.ts (+18 −4)",
  "Run npm test -- invoices (32 passed)",
  "Read docs/architecture.md",
  "Bash git diff --stat (3 files changed)",
  "Edit src/flags/index.ts (+41 −0)",
];
const DIFFS: [boolean, string][] = [
  [false, "const rows = await db.all(query);"],
  [true, "for await (const row of db.cursor(query)) {"],
  [true, "  await out.write(encode(row));"],
  [true, "}"],
];
const QUESTIONS = [
  { title: "Allow this edit to src/webhooks/send.ts?", options: ["Yes", "Yes, and don't ask again this session", "No, and tell it what to do instead"] },
  { title: "Run `npm install --save-dev vitest`?", options: ["Yes", "No"] },
  { title: "Overwrite docs/install.md with the new version?", options: ["Yes", "Show me the diff first", "No"] },
];

const pick = <T>(list: readonly T[], n: number) => list[n % list.length];

function seedHistory(n: number, agent: boolean): Entry[] {
  if (!agent)
    return [
      { kind: "cmd", text: "git status --short" },
      { kind: "out", text: " M src/export/job.ts" },
      { kind: "out", text: " M src/export/job.test.ts" },
      { kind: "cmd", text: "npm test -- export" },
      { kind: "out", text: "  ✓ streams rows in order (12 ms)" },
      { kind: "out", text: "  ✓ handles an empty table (3 ms)" },
      { kind: "out", text: "  2 passed" },
    ];
  return [
    { kind: "prompt", text: pick(PROMPTS, n) },
    { kind: "tool", text: pick(TOOLS, n) },
    { kind: "tool", text: pick(TOOLS, n + 2) },
    { kind: "say", text: pick(SAYS, n) },
    ...DIFFS.map(([add, text]) => ({ kind: "diff" as const, add, text })),
    { kind: "tool", text: pick(TOOLS, n + 3) },
  ];
}

let paneSeed = 0;
function pane(
  ws: string,
  tab: string,
  n: number,
  opts: { agent?: string; cwd: string; title?: string; status?: Status; cycle?: [Status, number][]; heldBy?: string },
): SimPane {
  const seed = paneSeed++;
  const status = opts.status ?? opts.cycle?.[0][0] ?? (opts.agent ? "idle" : "unknown");
  return {
    id: `${ws}:p${n}`,
    ws,
    tab,
    agent: opts.agent,
    cwd: opts.cwd,
    title: opts.title ?? (opts.agent ? pick(PROMPTS, seed) : "bash"),
    status,
    cycle: opts.cycle,
    step: 0,
    stepAt: Date.now(),
    holdUntil: 0,
    workSince: Date.now() - 30_000,
    terminal: term(),
    revision: 1,
    seq: seed,
    history: seedHistory(seed, !!opts.agent),
    input: "",
    question: status === "blocked" ? { ...pick(QUESTIONS, seed), choice: 0 } : undefined,
    real: { cols: 118, rows: 34 },
    scroll: 0,
    heldBy: opts.heldBy,
  };
}

const session = (name: string, workspaces: SimWorkspace[], panes: SimPane[], extra: Partial<SimSession> = {}): SimSession => ({
  name,
  epoch: newEpoch(),
  online: true,
  workspaces,
  panes,
  ...extra,
});

const ws = (id: string, label: string, tabs: string[] = ["main"]): SimWorkspace => ({
  id,
  label,
  tabs: tabs.map((t, i) => ({ id: `${id}:t${i + 1}`, label: t })),
});

const dock = (host: string, user: string, os: string, arch: string, role: Role, sessions: SimSession[], extra: Partial<SimDock> = {}): SimDock => ({
  id: `${host}-${user}`,
  name: host,
  user,
  os,
  arch,
  herdr: "0.9.3",
  online: true,
  lastSeen: Date.now(),
  role,
  sessions,
  ...extra,
});

export function makeFleet(): SimMachine[] {
  const H = (u: string) => `/home/${u}`;
  return [
    {
      host: "studio",
      docks: [
        dock("studio", "admin", "linux", "amd64", "owner", [
          session(
            "default",
            [ws("w1", "fleet-ops", ["main", "logs"])],
            [
              pane("w1", "w1:t1", 1, { agent: "claude", cwd: `${H("admin")}/fleet-ops`, cycle: [["working", 40], ["done", 20], ["idle", 25]] }),
              pane("w1", "w1:t1", 2, { cwd: `${H("admin")}/fleet-ops` }),
              pane("w1", "w1:t2", 3, { cwd: `${H("admin")}/fleet-ops/logs`, title: "tail -f relay.log" }),
            ],
          ),
          session(
            "night-shift",
            [ws("w1", "migrations")],
            [pane("w1", "w1:t1", 1, { agent: "codex", cwd: `${H("admin")}/migrations`, cycle: [["idle", 30], ["working", 30]] })],
          ),
        ]),
        dock("studio", "agent-one", "linux", "amd64", "operator", [
          session(
            "default",
            [ws("w1", "api"), ws("w2", "docs")],
            [
              pane("w1", "w1:t1", 1, { agent: "claude", cwd: `${H("agent-one")}/api`, cycle: [["working", 20], ["blocked", 25], ["working", 15], ["idle", 15]] }),
              pane("w1", "w1:t1", 2, { cwd: `${H("agent-one")}/api` }),
              pane("w2", "w2:t1", 1, {
                agent: "codex",
                cwd: `${H("agent-one")}/docs`,
                cycle: [["blocked", 30], ["working", 30]],
                heldBy: "ember-fleet term on laptop",
              }),
            ],
          ),
        ]),
        dock("studio", "agent-two", "linux", "amd64", "viewer", [
          session(
            "default",
            [ws("w1", "billing"), ws("w2", "infra", ["deploy", "watch"])],
            [
              pane("w1", "w1:t1", 1, { agent: "omp", cwd: `${H("agent-two")}/billing`, status: "idle" }),
              pane("w2", "w2:t1", 1, { agent: "grok", cwd: `${H("agent-two")}/infra`, status: "working" }),
              pane("w2", "w2:t2", 2, { agent: "gemini", cwd: `${H("agent-two")}/infra/dashboards`, status: "done" }),
            ],
          ),
        ]),
      ],
    },
    {
      host: "laptop",
      docks: [
        dock("laptop", "sam", "darwin", "arm64", "owner", [
          session(
            "default",
            [ws("w1", "site"), ws("w2", "notes")],
            [
              pane("w1", "w1:t1", 1, { agent: "claude", cwd: "/Users/sam/site", status: "blocked" }),
              pane("w1", "w1:t1", 2, { cwd: "/Users/sam/site", title: "npm run dev" }),
              pane("w2", "w2:t1", 1, { agent: "opencode", cwd: "/Users/sam/notes", status: "idle" }),
            ],
          ),
        ]),
      ],
    },
    {
      host: "reviewer",
      docks: [
        dock(
          "reviewer",
          "reviewer",
          "linux",
          "arm64",
          "owner",
          [
            session(
              "default",
              [ws("w1", "review-queue")],
              [
                pane("w1", "w1:t1", 1, { agent: "claude", cwd: `${H("reviewer")}/queue`, status: "idle" }),
                pane("w1", "w1:t1", 2, { agent: "amp", cwd: `${H("reviewer")}/queue`, status: "working" }),
              ],
            ),
          ],
          { online: false, lastSeen: Date.now() - 3 * 60_000 },
        ),
      ],
    },
    {
      host: "buildbox",
      docks: [
        dock("buildbox", "ci", "linux", "amd64", "owner", [
          session("default", [], [], {
            online: false,
            error: "herdr is not running (no socket at ~/.config/herdr/herdr.sock)",
          }),
        ]),
      ],
    },
  ];
}

/** A machine that just joined with a link: one agent and a shell. */
export function joinedMachine(host: string): SimMachine {
  return {
    host,
    docks: [
      dock(host, "agent-new", "linux", "arm64", "owner", [
        session(
          "default",
          [ws("w1", "home")],
          [
            pane("w1", "w1:t1", 1, { agent: "claude", cwd: "/home/agent-new", status: "idle" }),
            pane("w1", "w1:t1", 2, { cwd: "/home/agent-new" }),
          ],
        ),
      ]),
    ],
  };
}

// ---------------------------------------------------------------- herdr's shapes

const URGENCY: Status[] = ["blocked", "working", "done", "idle", "unknown"];
function rollup(list: Status[]): Status {
  let best = URGENCY.length - 1;
  for (const s of list) best = Math.min(best, URGENCY.indexOf(s));
  return URGENCY[best];
}

/** The session as herdr's session.snapshot would describe it. */
export function snapshot(s: SimSession) {
  const paneOut = s.panes.map((p) => ({
    pane_id: p.id,
    tab_id: p.tab,
    workspace_id: p.ws,
    ...(p.agent ? { agent: p.agent, agent_status: p.status } : { agent_status: "unknown" }),
    ...(p.label ? { label: p.label } : {}),
    cwd: p.cwd,
    foreground_cwd: p.cwd,
    focused: false,
    revision: p.revision,
    terminal_id: p.terminal,
    terminal_title: p.title,
    terminal_title_stripped: p.title,
    scroll: { max_offset_from_bottom: 0, offset_from_bottom: 0, viewport_rows: p.real.rows },
  }));
  const tabs = s.workspaces.flatMap((w) =>
    w.tabs.map((t, i) => {
      const ps = s.panes.filter((p) => p.tab === t.id);
      return {
        tab_id: t.id,
        workspace_id: w.id,
        label: t.label,
        number: i + 1,
        pane_count: ps.length,
        agent_status: rollup(ps.filter((p) => p.agent).map((p) => p.status)),
        focused: false,
      };
    }),
  );
  const workspaces = s.workspaces.map((w, i) => {
    const ps = s.panes.filter((p) => p.ws === w.id);
    return {
      workspace_id: w.id,
      label: w.label,
      number: i + 1,
      tab_count: w.tabs.length,
      pane_count: ps.length,
      active_tab_id: w.tabs[0]?.id,
      agent_status: rollup(ps.filter((p) => p.agent).map((p) => p.status)),
      focused: i === 0,
    };
  });
  const layouts = s.workspaces.flatMap((w) =>
    w.tabs.map((t) => {
      const ps = s.panes.filter((p) => p.tab === t.id);
      const h = Math.floor(40 / Math.max(1, ps.length));
      return {
        workspace_id: w.id,
        tab_id: t.id,
        area: { x: 0, y: 0, width: 160, height: 40 },
        zoomed: false,
        panes: ps.map((p, i) => ({ pane_id: p.id, focused: i === 0, rect: { x: 0, y: i * h, width: 160, height: h } })),
      };
    }),
  );
  return {
    version: "0.9.3",
    protocol: 22,
    workspaces,
    tabs,
    panes: paneOut,
    agents: paneOut.filter((p) => "agent" in p).map((p, i) => ({ ...p, scroll: undefined, state_change_seq: 100 + i })),
    layouts,
    focused_workspace_id: s.workspaces[0]?.id,
    focused_tab_id: s.workspaces[0]?.tabs[0]?.id,
    focused_pane_id: s.panes[0]?.id,
  };
}

export function machineOut(m: SimMachine) {
  return {
    host: m.host,
    docks: m.docks.map((d) => ({
      id: d.id,
      name: d.name,
      user: d.user,
      os: d.os,
      arch: d.arch,
      herdr: d.herdr,
      online: d.online,
      lastSeen: d.online ? Date.now() : d.lastSeen,
      role: d.role,
      sessions: d.sessions.map((s) => ({
        name: s.name,
        epoch: s.epoch,
        online: s.online && d.online,
        ...(s.error ? { error: s.error } : {}),
        ...(s.online || s.panes.length ? { snapshot: snapshot(s) } : {}),
      })),
    })),
  };
}

// ---------------------------------------------------------------- living

export function screenOf(p: SimPane, d: SimDock, host: string): ScreenModel {
  const home = d.os === "darwin" ? `/Users/${d.user}` : `/home/${d.user}`;
  return {
    harness: p.agent,
    harnessName: p.agent ? (HARNESS_NAMES[p.agent] ?? p.agent) : "shell",
    user: d.user,
    host,
    cwd: p.cwd.startsWith(home) ? "~" + p.cwd.slice(home.length) : p.cwd,
    status: p.status,
    history: p.history,
    input: p.input,
    question: p.question,
    elapsed: Math.max(0, Math.round((Date.now() - p.workSince) / 1000)),
    tick: Math.floor(Date.now() / 450),
    scroll: p.scroll,
  };
}

export function setStatus(p: SimPane, status: Status, hold = 0) {
  if (p.status === status) return false;
  if (status === "working" && p.status !== "working") p.workSince = Date.now();
  if (status === "blocked") p.question = { ...pick(QUESTIONS, p.seq++), choice: 0 };
  else p.question = undefined;
  if (status === "done") p.history.push({ kind: "say", text: pick(SAYS, 4) });
  p.status = status;
  p.revision++;
  if (hold) p.holdUntil = Date.now() + hold;
  return true;
}

/** One second of life: cycles advance, working agents make progress. Returns whether the snapshot changed. */
export function tick(p: SimPane, now: number): { snapshot: boolean; screen: boolean } {
  let snap = false;
  let screen = p.status === "working"; // the spinner moves
  if (p.cycle && now >= p.holdUntil) {
    const [, secs] = p.cycle[p.step % p.cycle.length];
    if (now - p.stepAt > secs * 1000) {
      p.step = (p.step + 1) % p.cycle.length;
      p.stepAt = now;
      snap = setStatus(p, p.cycle[p.step][0]) || snap;
      screen = true;
    }
  }
  if (p.status === "working" && Math.random() < 0.28) {
    const r = p.seq++;
    p.history.push(
      r % 4 === 0
        ? { kind: "say", text: pick(SAYS, r) }
        : r % 4 === 1
          ? { kind: "diff", add: r % 3 !== 0, text: pick(DIFFS, r)[1] }
          : { kind: "tool", text: pick(TOOLS, r) },
    );
    if (p.history.length > 300) p.history.splice(0, p.history.length - 300);
    screen = true;
  }
  return { snapshot: snap, screen };
}

/** Typing into a pane in control: an agent takes a prompt on Enter, a shell runs a pretend command. */
export function typeInto(p: SimPane, text: string): boolean {
  let snap = false;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (ch === "\x1b") {
      // an escape sequence (arrows and so on) or a lone Esc
      const m = /^\x1b(\[[0-9;?]*[ -/]*[@-~]|O.)/.exec(text.slice(i));
      if (m) {
        const k = m[0];
        if (p.question && (k === "\x1b[A" || k === "\x1bOA")) p.question.choice = Math.max(0, p.question.choice - 1);
        if (p.question && (k === "\x1b[B" || k === "\x1bOB")) p.question.choice = Math.min(p.question.options.length - 1, p.question.choice + 1);
        i += k.length - 1;
        continue;
      }
      if (p.agent && p.status === "working") {
        p.history.push({ kind: "note", text: "Interrupted. What should it do instead?" });
        snap = setStatus(p, "idle", 30_000) || snap;
      }
    } else if (ch === "\r" || ch === "\n") {
      if (p.agent && p.status === "blocked") {
        const q = p.question;
        p.history.push({ kind: "note", text: `${q?.options[q.choice] ?? "Yes"}.` });
        snap = setStatus(p, "working", 20_000) || snap;
      } else if (p.input.trim()) {
        if (p.agent) {
          p.history.push({ kind: "prompt", text: p.input });
          p.title = p.input.slice(0, 60);
          snap = setStatus(p, "working", 25_000) || true;
        } else {
          p.history.push({ kind: "cmd", text: p.input });
          p.history.push({ kind: "out", text: `(mock) ran: ${p.input}` });
        }
      } else if (!p.agent) p.history.push({ kind: "cmd", text: "" });
      p.input = "";
    } else if (ch === "\x7f" || ch === "\b") p.input = p.input.slice(0, -1);
    else if (ch === "\x03") {
      if (p.agent && p.status === "working") {
        p.history.push({ kind: "note", text: "Interrupted." });
        snap = setStatus(p, "idle", 30_000) || snap;
      } else {
        if (!p.agent) p.history.push({ kind: "cmd", text: p.input + "^C" });
        p.input = "";
      }
    } else if (ch >= " " || ch === "\t") p.input += ch === "\t" ? "  " : ch;
  }
  p.scroll = 0;
  return snap;
}
