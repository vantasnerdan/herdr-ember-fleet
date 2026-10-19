// The hub's web API (BUILD.md, "Web API") and the parts of herdr's own
// `session.snapshot` the page reads. Herdr fields are optional where a server may
// leave them out; the page never assumes more than it shows.

/** herdr's agent states. Anything else a newer herdr reports is shown as unknown. */
export type AgentStatus = "working" | "idle" | "blocked" | "done" | "unknown";

export type Role = "owner" | "operator" | "viewer";

export interface Me {
  signedIn: boolean;
  csrf: string;
  fleet: string;
  /** The hub's key fingerprint, short. */
  hub: string;
  version: string;
}

export interface Machine {
  host: string;
  docks: Dock[];
}

/** One OS user's herdr on one machine. */
export interface Dock {
  id: string;
  name: string;
  user: string;
  os: string;
  arch: string;
  /** herdr version */
  herdr: string;
  online: boolean;
  /** unix ms */
  lastSeen: number;
  role: Role;
  sessions: Session[];
}

export interface Session {
  /** "default" or a named herdr session */
  name: string;
  epoch: string;
  online: boolean;
  error?: string;
  snapshot?: HerdrSnapshot;
}

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface HerdrWorkspace {
  workspace_id: string;
  label: string;
  number?: number;
  tab_count?: number;
  pane_count?: number;
  active_tab_id?: string;
  agent_status?: string;
  focused?: boolean;
}

export interface HerdrTab {
  tab_id: string;
  workspace_id: string;
  label: string;
  number?: number;
  pane_count?: number;
  agent_status?: string;
  focused?: boolean;
}

export interface HerdrPane {
  pane_id: string;
  tab_id: string;
  workspace_id: string;
  /** The harness herdr detected (claude, codex, omp, grok…); absent for a plain shell. */
  agent?: string | null;
  agent_status?: string | null;
  /** Set by pane.rename. */
  label?: string | null;
  cwd?: string;
  foreground_cwd?: string;
  focused?: boolean;
  revision?: number;
  terminal_id?: string;
  terminal_title?: string;
  terminal_title_stripped?: string;
  scroll?: { max_offset_from_bottom: number; offset_from_bottom: number; viewport_rows: number };
}

export interface HerdrLayout {
  workspace_id: string;
  tab_id: string;
  area?: Rect;
  focused_pane_id?: string;
  zoomed?: boolean;
  panes?: { pane_id: string; focused?: boolean; rect: Rect }[];
}

export interface HerdrSnapshot {
  version?: string;
  protocol?: number;
  workspaces: HerdrWorkspace[];
  tabs: HerdrTab[];
  panes: HerdrPane[];
  agents?: (HerdrPane & { state_change_seq?: number })[];
  layouts?: HerdrLayout[];
  focused_workspace_id?: string;
  focused_tab_id?: string;
  focused_pane_id?: string;
}

// ---------------------------------------------------------------- live updates

export type LiveMessage =
  | { type: "hello"; fleet: { machines: Machine[] }; groups?: Group[] }
  | { type: "machine"; machine: Machine }
  | { type: "removed"; host?: string; id?: string }
  | { type: "groups"; groups: Group[] }
  | { type: "fleet"; name: string };

// ---------------------------------------------------------------- groups

/** The swatches a group may wear; "" is none. */
export type GroupColor = "" | "ember" | "amber" | "rose" | "teal" | "sky" | "violet" | "slate";

/** A whole machine user (dock) or a single agent (pane). Each is in at most one group. */
export type Member =
  | { kind: "dock"; dock: string }
  | { kind: "pane"; dock: string; session: string; pane: string };

export interface Group {
  id: string;
  name: string;
  /** "" or a GroupColor; anything else a newer hub sends is shown without a swatch */
  color: string;
  members: Member[];
}

// ---------------------------------------------------------------- uploads

export interface UploadTarget {
  dock: string;
  session: string;
  pane: string;
}

export interface Uploaded {
  /** Absolute path on the agent's machine. */
  path: string;
  name: string;
  size: number;
}

// ---------------------------------------------------------------- terminals

export type TermMode = "observe" | "control";

export type TermServerMessage =
  | { type: "frame"; seq: number; full: boolean; width: number; height: number; data: string }
  | { type: "status"; state: "open" | "closed" | "error" | "busy"; reason?: string };

export type TermClientMessage =
  | { type: "input"; text: string }
  | { type: "resize"; cols: number; rows: number }
  | { type: "scroll"; direction: "up" | "down"; lines: number }
  | { type: "release" };

// ---------------------------------------------------------------- actions

export type HerdrKey = "esc" | "ctrl+c" | "enter" | "up" | "down" | "tab";

export type ActionMethod =
  | "agent.prompt"
  | "pane.send_keys"
  | "pane.rename"
  | "tab.rename"
  | "workspace.rename"
  | "pane.close";

export interface ActionRequest {
  dock: string;
  session: string;
  epoch: string;
  method: ActionMethod;
  params: Record<string, unknown>;
}

export type ActionResult =
  | { ok: true; result?: unknown }
  | { ok: false; error: string; outcome: "not_sent" | "unknown" };

// ---------------------------------------------------------------- admin

export type LinkState = "pending" | "used" | "expired" | "cancelled";

export interface JoinLink {
  id: string;
  name: string;
  /** unix ms or an ISO time; both are accepted */
  created: number | string;
  expires: number | string;
  state: LinkState;
  dock?: string;
}

export interface NewLink {
  id: string;
  link: string;
  command: string;
  expires: number | string;
}
