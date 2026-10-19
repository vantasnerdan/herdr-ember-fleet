// The fleet tree, derived from the hub's machines and each session's herdr snapshot:
//
//   machine (host) → dock (OS user) → session → workspace → tab → pane
//
// Levels that carry no choice are skipped: the dock when a host has one, the session
// when it is only "default", the tab when a workspace has one. What a skipped level
// would have said (its user, its error) moves up onto the row that stays.

import type {
  AgentStatus,
  Dock,
  Group,
  Member,
  HerdrPane,
  HerdrSnapshot,
  HerdrTab,
  HerdrWorkspace,
  Machine,
  Session,
} from "./types";

// ---------------------------------------------------------------- states

const KNOWN: readonly AgentStatus[] = ["working", "idle", "blocked", "done", "unknown"];
/** Rollups show the most urgent state below them. */
const URGENCY: readonly AgentStatus[] = ["blocked", "working", "done", "idle", "unknown"];

export function statusOf(s?: string | null): AgentStatus {
  return s && (KNOWN as readonly string[]).includes(s) ? (s as AgentStatus) : "unknown";
}

export function rollup(states: Iterable<AgentStatus>): AgentStatus | undefined {
  let best: number | undefined;
  for (const s of states) {
    const i = URGENCY.indexOf(s);
    if (best === undefined || i < best) best = i;
  }
  return best === undefined ? undefined : URGENCY[best];
}

export const STATUS_TEXT: Record<AgentStatus, string> = {
  working: "working",
  idle: "idle",
  blocked: "blocked",
  done: "done",
  unknown: "unknown",
};

// ---------------------------------------------------------------- harnesses

interface Harness {
  name: string;
  mono: string;
}

const HARNESSES: Record<string, Harness> = {
  claude: { name: "Claude Code", mono: "Cl" },
  codex: { name: "Codex", mono: "Cx" },
  omp: { name: "omp", mono: "om" },
  grok: { name: "Grok", mono: "Gk" },
  gemini: { name: "Gemini", mono: "Ge" },
  cursor: { name: "Cursor", mono: "Cu" },
  opencode: { name: "opencode", mono: "oc" },
  amp: { name: "Amp", mono: "Am" },
  pi: { name: "pi", mono: "π" },
  droid: { name: "Droid", mono: "Dr" },
  aider: { name: "aider", mono: "Ai" },
  goose: { name: "Goose", mono: "Go" },
  copilot: { name: "Copilot", mono: "Co" },
  qwen: { name: "Qwen", mono: "Qw" },
  kimi: { name: "Kimi", mono: "Ki" },
  crush: { name: "Crush", mono: "Cr" },
  cline: { name: "Cline", mono: "Cn" },
  kilo: { name: "Kilo", mono: "Kl" },
};

export function harnessOf(agent?: string | null): Harness | undefined {
  if (!agent) return undefined;
  const key = agent.toLowerCase();
  return HARNESSES[key] ?? { name: agent, mono: agent.slice(0, 2).replace(/^./, (c) => c.toUpperCase()) };
}

// ---------------------------------------------------------------- paths and times

export function homeOf(dock: Pick<Dock, "os" | "user">): string {
  if (dock.user === "root") return dock.os === "darwin" ? "/var/root" : "/root";
  return dock.os === "darwin" ? `/Users/${dock.user}` : `/home/${dock.user}`;
}

/** `~` for the dock user's home folder. */
export function tildePath(path: string | undefined, dock: Pick<Dock, "os" | "user">): string {
  if (!path) return "";
  const home = homeOf(dock);
  if (path === home) return "~";
  if (path.startsWith(home + "/")) return "~" + path.slice(home.length);
  return path;
}

/** A path short enough for a tree row: its last two folders. */
export function shortPath(path: string): string {
  const parts = path.split("/").filter(Boolean);
  if (parts.length <= 3) return path;
  return `…/${parts.slice(-2).join("/")}`;
}

export function ago(ms: number, now = Date.now()): string {
  if (!ms) return "never";
  const s = Math.max(0, Math.round((now - ms) / 1000));
  if (s < 10) return "just now";
  if (s < 60) return `${s} s ago`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m} min ago`;
  const h = Math.round(m / 60);
  if (h < 48) return `${h} h ago`;
  return `${Math.round(h / 24)} d ago`;
}

export const plural = (n: number, word: string, many = `${word}s`) => `${n} ${n === 1 ? word : many}`;

// ---------------------------------------------------------------- panes

export interface PaneRef {
  dock: string;
  session: string;
  pane: string;
}

export const refKey = (r: PaneRef) =>
  [r.dock, r.session, r.pane].map(encodeURIComponent).join("/");

export function parseRefKey(key: string): PaneRef | undefined {
  const parts = key.split("/");
  if (parts.length !== 3 || parts.some((p) => !p)) return undefined;
  try {
    const [dock, session, pane] = parts.map(decodeURIComponent);
    return { dock, session, pane };
  } catch {
    return undefined;
  }
}

/** One pane, with everything a row, the pane view or a notification needs. */
export interface PaneInfo {
  key: string;
  ref: PaneRef;
  host: string;
  dock: Dock;
  session: Session;
  workspace?: HerdrWorkspace;
  tab?: HerdrTab;
  pane: HerdrPane;
  /** The harness name herdr reports, lowercased; undefined for a plain shell. */
  harness?: string;
  status: AgentStatus;
  /** What the row calls it: its label, else its harness, else "shell". */
  name: string;
  title: string;
  /** cwd with ~ for the dock user's home */
  cwd: string;
  /** The dock and the session are both reachable now. */
  online: boolean;
}

function paneInfo(host: string, dock: Dock, session: Session, snap: HerdrSnapshot, pane: HerdrPane): PaneInfo {
  const ref = { dock: dock.id, session: session.name, pane: pane.pane_id };
  const harness = pane.agent ? pane.agent.toLowerCase() : undefined;
  return {
    key: refKey(ref),
    ref,
    host,
    dock,
    session,
    workspace: snap.workspaces.find((w) => w.workspace_id === pane.workspace_id),
    tab: snap.tabs.find((t) => t.tab_id === pane.tab_id),
    pane,
    harness,
    status: harness ? statusOf(pane.agent_status) : "unknown",
    name: pane.label || (harness ? (harnessOf(harness)?.name ?? harness) : "shell"),
    title: pane.terminal_title_stripped ?? pane.terminal_title ?? "",
    cwd: tildePath(pane.cwd || pane.foreground_cwd, dock),
    online: dock.online && session.online,
  };
}

/** Panes in reading order: by their place on screen when herdr says, else as listed. */
function orderPanes(snap: HerdrSnapshot, panes: HerdrPane[]): HerdrPane[] {
  const place = new Map<string, { x: number; y: number }>();
  for (const layout of snap.layouts ?? []) for (const p of layout.panes ?? []) place.set(p.pane_id, p.rect);
  if (!place.size) return panes;
  return panes
    .map((p, i) => ({ p, i, r: place.get(p.pane_id) }))
    .sort((a, b) => (a.r && b.r ? a.r.y - b.r.y || a.r.x - b.r.x : a.i - b.i))
    .map((e) => e.p);
}

export function allPanes(machines: readonly Machine[]): PaneInfo[] {
  const out: PaneInfo[] = [];
  for (const m of machines)
    for (const d of m.docks)
      for (const s of d.sessions) {
        const snap = s.snapshot;
        if (!snap) continue;
        for (const p of snap.panes ?? []) out.push(paneInfo(m.host, d, s, snap, p));
      }
  return out;
}

// ---------------------------------------------------------------- the tree

export type NodeKind = "machine" | "dock" | "session" | "workspace" | "tab" | "pane" | "group" | "missing";

export interface Counts {
  agents: number;
  working: number;
  blocked: number;
  done: number;
  idle: number;
  shells: number;
}

export interface TreeNode {
  key: string;
  kind: NodeKind;
  label: string;
  children: TreeNode[];
  /** Reachable now; offline rows are dimmed and take no input. */
  online: boolean;
  /** The pane's own state, or the most urgent one below a container. */
  status?: AgentStatus;
  counts: Counts;
  /** Words matched by the filter box. */
  text: string;
  host: string;
  dock?: Dock;
  /** The dock a machine row stands for when its host has only one. */
  only?: Dock;
  session?: Session;
  /** The session a dock or machine row stands for when it is only "default". */
  onlySession?: Session;
  workspace?: HerdrWorkspace;
  tab?: HerdrTab;
  pane?: PaneInfo;
  /** Why a session can't be read: herdr not running, and so on. */
  error?: string;
  /** A group's header row. */
  group?: Group;
  /** The group member this row stands for (top-level rows inside a group). */
  member?: Member;
  /** Rows inside a group's body: which group, and the index of the member they belong to. */
  inGroup?: Group;
  memberIndex?: number;
  /** Where a row inside a group lives, since the machine tree above it isn't there: user@host. */
  context?: string;
}

const zero = (): Counts => ({ agents: 0, working: 0, blocked: 0, done: 0, idle: 0, shells: 0 });

function sum(children: TreeNode[]): Counts {
  const c = zero();
  for (const n of children)
    for (const k of Object.keys(c) as (keyof Counts)[]) c[k] += n.counts[k];
  return c;
}

export function container(
  base: Omit<TreeNode, "counts" | "status" | "text"> & { text?: string },
): TreeNode {
  const counts = sum(base.children);
  const states = base.children.map((c) => c.status).filter((s): s is AgentStatus => !!s);
  return { ...base, text: base.text ?? base.label, counts, status: rollup(states) };
}

export function paneNode(info: PaneInfo): TreeNode {
  const counts = zero();
  if (info.harness) {
    counts.agents = 1;
    if (info.status === "working") counts.working = 1;
    else if (info.status === "blocked") counts.blocked = 1;
    else if (info.status === "done") counts.done = 1;
    else if (info.status === "idle") counts.idle = 1;
  } else counts.shells = 1;
  return {
    key: `p:${info.key}`,
    kind: "pane",
    label: info.name,
    children: [],
    online: info.online,
    status: info.harness ? info.status : undefined,
    counts,
    text: [info.name, info.harness, info.title, info.cwd, info.harness ? info.status : "shell"].join(" "),
    host: info.host,
    dock: info.dock,
    session: info.session,
    workspace: info.workspace,
    tab: info.tab,
    pane: info,
  };
}

const sessionKey = (d: Dock, s: Session) => `${encodeURIComponent(d.id)}/${encodeURIComponent(s.name)}`;

function sessionError(s: Session, d: Dock): string | undefined {
  if (s.error) return s.error;
  if (d.online && !s.online) return "herdr is not answering";
  return undefined;
}

function workspacesOf(host: string, dock: Dock, s: Session): TreeNode[] {
  const snap = s.snapshot;
  if (!snap) return [];
  const online = dock.online && s.online;
  const base = sessionKey(dock, s);
  const workspaces = [...(snap.workspaces ?? [])].sort((a, b) => (a.number ?? 0) - (b.number ?? 0));
  return workspaces.map((w) => {
    const panes = orderPanes(
      snap,
      (snap.panes ?? []).filter((p) => p.workspace_id === w.workspace_id),
    ).map((p) => paneNode(paneInfo(host, dock, s, snap, p)));
    const tabs = [...(snap.tabs ?? [])]
      .filter((t) => t.workspace_id === w.workspace_id)
      .sort((a, b) => (a.number ?? 0) - (b.number ?? 0));
    const children =
      tabs.length <= 1
        ? panes
        : tabs.map((t) =>
            container({
              key: `t:${base}/${encodeURIComponent(t.tab_id)}`,
              kind: "tab",
              label: t.label || `tab ${t.number ?? ""}`.trim(),
              children: panes.filter((p) => p.pane?.pane.tab_id === t.tab_id),
              online,
              host,
              dock,
              session: s,
              workspace: w,
              tab: t,
            }),
          );
    return container({
      key: `w:${base}/${encodeURIComponent(w.workspace_id)}`,
      kind: "workspace",
      label: w.label || `workspace ${w.number ?? ""}`.trim(),
      children,
      online,
      host,
      dock,
      session: s,
      workspace: w,
      tab: tabs.length === 1 ? tabs[0] : undefined,
    });
  });
}

const bySession = (a: Session, b: Session) =>
  a.name === "default" ? -1 : b.name === "default" ? 1 : a.name.localeCompare(b.name);

function dockContents(host: string, dock: Dock): { children: TreeNode[]; onlySession?: Session; error?: string } {
  const sessions = [...dock.sessions].sort(bySession);
  if (sessions.length === 1 && sessions[0].name === "default") {
    const s = sessions[0];
    return { children: workspacesOf(host, dock, s), onlySession: s, error: sessionError(s, dock) };
  }
  return {
    children: sessions.map((s) =>
      container({
        key: `s:${sessionKey(dock, s)}`,
        kind: "session",
        label: s.name,
        children: workspacesOf(host, dock, s),
        online: dock.online && s.online,
        host,
        dock,
        session: s,
        error: sessionError(s, dock),
      }),
    ),
  };
}

/** One machine user as a row of its own, labelled user@host (for groups). */
export function dockNode(host: string, dock: Dock): TreeNode {
  const { children, onlySession, error } = dockContents(host, dock);
  return container({
    key: `d:${encodeURIComponent(dock.id)}`,
    kind: "dock",
    label: `${dock.user}@${host}`,
    children,
    online: dock.online,
    text: `${dock.user} ${host} ${dock.name}`,
    host,
    dock,
    onlySession,
    error,
  });
}

export function buildTree(machines: readonly Machine[]): TreeNode[] {
  return [...machines]
    .sort((a, b) => a.host.localeCompare(b.host))
    .map((m) => {
      const docks = [...m.docks].sort((a, b) => a.user.localeCompare(b.user) || a.id.localeCompare(b.id));
      const users = docks.map((d) => d.user).join(" ");
      if (docks.length === 1) {
        const d = docks[0];
        const { children, onlySession, error } = dockContents(m.host, d);
        return container({
          key: `m:${m.host}`,
          kind: "machine",
          label: m.host,
          children,
          online: d.online,
          text: `${m.host} ${d.name} ${users}`,
          host: m.host,
          only: d,
          dock: d,
          onlySession,
          error,
        });
      }
      const children = docks.map((d) => {
        const { children: kids, onlySession, error } = dockContents(m.host, d);
        return container({
          key: `d:${encodeURIComponent(d.id)}`,
          kind: "dock",
          label: d.user,
          children: kids,
          online: d.online,
          text: `${d.user} ${d.name}`,
          host: m.host,
          dock: d,
          onlySession,
          error,
        });
      });
      return container({
        key: `m:${m.host}`,
        kind: "machine",
        label: m.host,
        children,
        online: docks.some((d) => d.online),
        text: `${m.host} ${users}`,
        host: m.host,
      });
    });
}

/** Keeps rows that match every word, with their ancestors; a matching container keeps all it holds. */
export function filterTree(nodes: TreeNode[], query: string): TreeNode[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return nodes;
  const visit = (list: TreeNode[], inherited: string): TreeNode[] => {
    const out: TreeNode[] = [];
    for (const n of list) {
      const hay = `${inherited} ${n.text}`.toLowerCase();
      if (words.every((w) => hay.includes(w))) {
        out.push(n);
        continue;
      }
      const kids = visit(n.children, hay);
      if (kids.length) out.push({ ...n, children: kids });
    }
    return out;
  };
  return visit(nodes, "");
}

export interface Row {
  node: TreeNode;
  depth: number;
  parent?: string;
}

export function flatten(nodes: TreeNode[], isOpen: (n: TreeNode) => boolean): Row[] {
  const out: Row[] = [];
  const walk = (list: TreeNode[], depth: number, parent?: string) => {
    for (const node of list) {
      out.push({ node, depth, parent });
      if (node.children.length && isOpen(node)) walk(node.children, depth + 1, node.key);
    }
  };
  walk(nodes, 0);
  return out;
}

/** Every ancestor key of a pane, so selecting it can open the way down to it. */
export function pathTo(nodes: TreeNode[], key: string): string[] {
  const trail: string[] = [];
  const walk = (list: TreeNode[]): boolean => {
    for (const n of list) {
      if (n.key === key) return true;
      trail.push(n.key);
      if (walk(n.children)) return true;
      trail.pop();
    }
    return false;
  };
  return walk(nodes) ? trail : [];
}
