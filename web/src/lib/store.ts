// App state: who is signed in, the fleet as the hub last described it, which pane is
// open, and the small dialogs and notices every component may raise.

import { computed, reactive, ref, shallowRef, watch } from "vue";
import { api, ApiError, errorText, setCsrf, whenFleetRenamed, whenSignedOut } from "./api";
import { addrPart, allPanes, buildTree, parseRefKey, refKey, type PaneInfo, type PaneRef } from "./fleet";
import type { BrowserSession, Group, LiveMessage, Machine, Me, PinStatus, UpdateStatus, SigninAsk } from "./types";
import { connectLive } from "./live";

export type Phase = "loading" | "signed-out" | "signing-in" | "signin-failed" | "unreachable" | "ready";
export type LiveState = "connecting" | "live" | "reconnecting";

export const app = reactive({
  phase: "loading" as Phase,
  signinError: "",
  me: null as Me | null,
  live: "connecting" as LiveState,
  /** Signed out: the hub has a sign-in PIN, so the sign-in page offers it. */
  pinSignin: false,
  /** The hub now runs a newer build than this page: offer a reload. */
  newVersion: "",
  /** "Add a machine" is open. */
  adding: false,
  /** The command palette (Ctrl/Cmd-K) is open. */
  palette: false,
  /** In control of a terminal: Ctrl-K belongs to it, not to the palette. */
  typing: false,
  filter: "",
});

/** Replaced whole on every change, so derived state recomputes once per update. */
export const machines = shallowRef<Machine[]>([]);
export const panes = computed(() => allPanes(machines.value));
export const paneIndex = computed(() => new Map(panes.value.map((p) => [p.key, p])));
export const tree = computed(() => buildTree(machines.value));
const live = (p: PaneInfo) => !!p.harness && p.online;
export const blocked = computed(() => panes.value.filter((p) => live(p) && p.status === "blocked"));
export const done = computed(() => panes.value.filter((p) => live(p) && p.status === "done"));
export const docks = computed(() => machines.value.flatMap((m) => m.docks));

/** The viewer's groups, in display order (lib/groups.ts derives and changes them). */
export const groups = shallowRef<Group[]>([]);

/** Fleet updates (versions, schedule, last run), kept live while settings are open. */
export const updates = shallowRef<UpdateStatus | null>(null);

/** Settings: the PIN's state and the signed-in browsers, shared by the panels that show them. */
export const pinStatus = shallowRef<PinStatus | null>(null);
export const browserSessions = shallowRef<BrowserSession[]>([]);

/** Browsers asking to be signed in, for the approval banner. */
export const signinRequests = shallowRef<SigninAsk[]>([]);

/** Ticks every 15 s so "last seen 3 min ago" stays true. */
export const now = ref(Date.now());
setInterval(() => (now.value = Date.now()), 15_000);

// ---------------------------------------------------------------- per-viewer conveniences

export function load<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    return raw ? (JSON.parse(raw) as T) : fallback;
  } catch {
    return fallback;
  }
}
export function save(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // private window or blocked storage: the page works without it
  }
}

/**
 * Which tree rows are open. Machine rows start folded, so the machine list
 * stays compact and opens only where the viewer clicks; what's inside a
 * machine, and groups (how the viewer gets to agents), start open.
 */
export const collapsed = reactive(new Set<string>(load<string[]>("ember.collapsed", [])));
export const expanded = reactive(new Set<string>(load<string[]>("ember.expanded", [])));
const startsFolded = (key: string) => key.startsWith("m:");
export const isOpen = (key: string) => (startsFolded(key) ? expanded.has(key) : !collapsed.has(key));
export function setOpen(key: string, open: boolean) {
  if (startsFolded(key)) {
    if (open) expanded.add(key);
    else expanded.delete(key);
    save("ember.expanded", [...expanded]);
  } else {
    if (open) collapsed.delete(key);
    else collapsed.add(key);
    save("ember.collapsed", [...collapsed]);
  }
}

export const sidebarWidth = ref(load<number>("ember.sidebar", 372));

/** How the sidebar arranges the fleet, and whether it lists plain shells. */
export const treeView = ref<"groups" | "machines">(load<string>("ember.tree-view", "groups") === "machines" ? "machines" : "groups");
export const showShells = ref(load<boolean>("ember.shells", false));
watch(treeView, (v) => save("ember.tree-view", v));
watch(showShells, (v) => save("ember.shells", v));

// ---------------------------------------------------------------- where the viewer is (the URL's #)
//
//   (none)                     overview
//   #/a/<host>/<user>[/<session>]/<pane>   an agent's pane (#/p/<dock>/<session>/<pane> still works)
//   #/agents[/<status>]        every agent in one list
//   #/machines[/<host>]        machines, or one machine
//   #/settings[/<section>]     settings

export type AgentFilter = "all" | "blocked" | "working" | "idle" | "done";
export type Page =
  | { name: "overview" }
  | { name: "pane" }
  | { name: "agents"; status: AgentFilter }
  | { name: "machines" }
  | { name: "machine"; host: string }
  | { name: "settings"; section: string };

const FILTERS: readonly AgentFilter[] = ["all", "blocked", "working", "idle", "done"];

export const page = shallowRef<Page>({ name: "overview" });
/** The open pane's internal key (dock/session/pane), or a placeholder for an address not in the fleet. */
export const selectedKey = ref<string | null>(null);
export const selectedPane = computed(() =>
  selectedKey.value ? paneIndex.value.get(selectedKey.value) : undefined,
);
export const selectedRef = computed<PaneRef | undefined>(() =>
  selectedKey.value ? parseRefKey(selectedKey.value) : undefined,
);

/** A pane address that came in before the fleet did; resolved when it arrives. */
let pendingAddr: string | null = null;
let fleetLoaded = false;

function decodeParts(rest: string): string[] | null {
  try {
    return rest.split("/").map(decodeURIComponent);
  } catch {
    return null;
  }
}

function paneByAddr(addr: string): PaneInfo | undefined {
  return panes.value.find((p) => p.addr === addr);
}

function resolveAddr(addr: string) {
  const p = paneByAddr(addr);
  if (p) {
    pendingAddr = null;
    selectedKey.value = p.key;
  } else {
    // Not here (yet): its machine may still be reconnecting, so keep looking.
    pendingAddr = addr;
    selectedKey.value = fleetLoaded ? `?/${addr}` : null; // PaneView says it isn't in the fleet
  }
}

function readHash() {
  const h = location.hash.replace(/^#\/?/, "");
  const [head, ...rest] = h.split("/");
  pendingAddr = null;
  if (head === "a" && rest.length >= 3) {
    page.value = { name: "pane" };
    // the same address however it was escaped (a pasted link may differ)
    const parts = decodeParts(rest.join("/"));
    resolveAddr(parts ? parts.map(addrPart).join("/") : rest.join("/"));
    return;
  }
  selectedKey.value = null;
  const legacy = head === "p" ? parseRefKey(rest.join("/")) : undefined;
  if (legacy) {
    page.value = { name: "pane" };
    selectedKey.value = refKey(legacy); // one spelling, however the link escaped it
    const info = paneIndex.value.get(selectedKey.value);
    if (info) history.replaceState({ ember: true }, "", `${location.pathname}${location.search}#/a/${info.addr}`);
  } else if (head === "agents") {
    const status = (FILTERS as readonly string[]).includes(rest[0] ?? "") ? (rest[0] as AgentFilter) : "all";
    page.value = { name: "agents", status };
  } else if (head === "machines" && rest[0]) {
    const host = decodeParts(rest[0])?.[0];
    page.value = host ? { name: "machine", host } : { name: "machines" };
  } else if (head === "machines") page.value = { name: "machines" };
  else if (head === "settings") page.value = { name: "settings", section: rest[0] || "general" };
  else page.value = { name: "overview" };
}
readHash();

function go(hash: string, replace = false) {
  const url = `${location.pathname}${location.search}${hash}`;
  if (url === `${location.pathname}${location.search}${location.hash}`) return;
  if (replace) history.replaceState({ ember: true }, "", url);
  else history.pushState({ ember: true }, "", url);
  readHash();
}

export const showOverview = () => go("");
export const showAgents = (status: AgentFilter = "all") => go(status === "all" ? "#/agents" : `#/agents/${status}`, page.value.name === "agents");
export const showMachines = () => go("#/machines");
export const showMachine = (host: string) => go(`#/machines/${encodeURIComponent(host)}`);
export const showSettings = (section = "general") =>
  go(section === "general" ? "#/settings" : `#/settings/${section}`, page.value.name === "settings");

/** Agents opened lately, newest first (for the palette). */
export const recent = ref<string[]>(load<string[]>("ember.recent", []));
function remember(key: string) {
  recent.value = [key, ...recent.value.filter((k) => k !== key)].slice(0, 8);
  save("ember.recent", recent.value);
}

export function openPane(ref: PaneRef) {
  const key = refKey(ref);
  remember(key);
  // Opening a pane never unfolds the machine tree; the viewer folds and
  // unfolds machines by hand.
  if (page.value.name === "pane" && selectedKey.value === key) return;
  const info = paneIndex.value.get(key);
  go(info ? `#/a/${info.addr}` : `#/p/${key}`);
}

/** Back to the overview. (The browser's Back still returns to the pane.) */
export const closePane = showOverview;

window.addEventListener("popstate", readHash);

// Any new fleet data (the live feed's hello, a machine update, a refresh) may hold an agent
// whose address came in before it did.
watch(machines, () => {
  if (fleetLoaded && pendingAddr) fleetArrived();
});

/** Called once the fleet has loaded, and when it changes while an address waits. */
function fleetArrived() {
  fleetLoaded = true;
  if (pendingAddr && page.value.name === "pane") resolveAddr(pendingAddr);
  // An open pane keeps a readable address once it is known.
  else if (page.value.name === "pane" && selectedKey.value && location.hash.startsWith("#/p/")) {
    const info = paneIndex.value.get(selectedKey.value);
    if (info) history.replaceState({ ember: true }, "", `${location.pathname}${location.search}#/a/${info.addr}`);
  }
}

// ---------------------------------------------------------------- notices

export type Tone = "info" | "ok" | "warn" | "error";
export interface Toast {
  id: number;
  text: string;
  tone: Tone;
}
export const toasts = reactive<Toast[]>([]);
let toastId = 0;
export function notify(text: string, tone: Tone = "info", ms = tone === "error" || tone === "warn" ? 7000 : 3500) {
  const id = ++toastId;
  toasts.push({ id, text, tone });
  if (toasts.length > 4) toasts.shift();
  setTimeout(() => dismiss(id), ms);
}
export function dismiss(id: number) {
  const i = toasts.findIndex((t) => t.id === id);
  if (i >= 0) toasts.splice(i, 1);
}

// ---------------------------------------------------------------- asking the person

export interface AskOptions {
  eyebrow?: string;
  title: string;
  body?: string;
  confirm: string;
  cancel?: string;
  danger?: boolean;
  /** Ask for one line of text, prefilled. */
  input?: { label: string; value?: string; placeholder?: string; maxlength?: number };
  /** The confirm button is a real link to this URL, so the browser opens it from the click itself. */
  link?: string;
}
interface Asking extends AskOptions {
  resolve: (value: string | boolean | null) => void;
}
export const asking = shallowRef<Asking | null>(null);

function ask(opts: AskOptions): Promise<string | boolean | null> {
  asking.value?.resolve(null);
  return new Promise((resolve) => {
    asking.value = {
      ...opts,
      resolve: (v) => {
        asking.value = null;
        resolve(v);
      },
    };
  });
}
export const confirmAsk = async (opts: Omit<AskOptions, "input">) => (await ask(opts)) === true;
export const promptAsk = async (opts: AskOptions & { input: NonNullable<AskOptions["input"]> }) => {
  const v = await ask(opts);
  return typeof v === "string" ? v : null;
};

// ---------------------------------------------------------------- signing in and staying current

let stopLive: (() => void) | undefined;
let retry: ReturnType<typeof setTimeout> | undefined;

function signedOut() {
  stopLive?.();
  stopLive = undefined;
  machines.value = [];
  groups.value = [];
  app.me = null;
  app.adding = false;
  if (app.phase !== "signin-failed") app.phase = "signed-out";
}
whenSignedOut(signedOut);

function fleetRenamed(name: string) {
  if (app.me && name) app.me.fleet = name;
}
whenFleetRenamed(fleetRenamed);

const byHost = (list: Machine[]) => [...list].sort((a, b) => a.host.localeCompare(b.host));

export function applyLive(msg: LiveMessage) {
  if (msg.type === "hello") {
    machines.value = byHost(msg.fleet.machines ?? []);
    if (msg.groups) groups.value = msg.groups;
    signinRequests.value = msg.signinRequests ?? [];
    // The live feed reconnects after the hub restarts; a new build means this page is old.
    if (msg.version && app.me?.version && msg.version !== app.me.version) app.newVersion = msg.version;
  } else if (msg.type === "groups") groups.value = msg.groups ?? [];
  else if (msg.type === "updates") updates.value = msg.updates;
  else if (msg.type === "signin-requests") signinRequests.value = msg.requests ?? [];
  else if (msg.type === "fleet") fleetRenamed(msg.name);
  else if (msg.type === "machine") {
    // Replace in place and keep the order stable (by name): appending moved
    // whichever machine last reported to the end of every list.
    const list = machines.value.filter((m) => m.host !== msg.machine.host);
    machines.value = byHost(msg.machine.docks.length ? [...list, msg.machine] : list);
  } else if (msg.type === "removed") {
    const host = msg.host ?? msg.id;
    machines.value = machines.value.filter((m) => m.host !== host && !m.docks.some((d) => d.id === host));
  }
}

/** Re-reads the whole fleet, for when a live update may have been missed. */
export async function refreshFleet() {
  try {
    machines.value = byHost((await api.fleet()).machines ?? []);
  } catch {
    // the live channel will catch up
  }
}

async function signinFromFragment() {
  const token = location.hash.slice(1);
  // The token is single use, but keep it out of the address bar and history either way.
  history.replaceState(null, "", "/");
  if (!token) return;
  app.phase = "signing-in";
  try {
    await api.signin(token);
  } catch (e) {
    app.phase = "signin-failed";
    const status = e instanceof ApiError ? e.status : 0;
    app.signinError =
      status === 401 || status === 403 || status === 404 || status === 409 || status === 410
        ? "This sign-in link was already used or has expired."
        : `Sign-in failed: ${errorText(e)}`;
  }
}

export async function boot() {
  clearTimeout(retry);
  if (location.pathname === "/signin") await signinFromFragment();
  if (app.phase === "signin-failed") return;
  try {
    const me = await api.me();
    app.pinSignin = !!me.pin;
    if (!me.signedIn) return signedOut();
    setCsrf(me.csrf);
    app.me = me;
    machines.value = byHost((await api.fleet()).machines ?? []);
    groups.value = await loadGroups();
    fleetArrived();
    app.phase = "ready";
    stopLive?.();
    stopLive = connectLive({
      onState: (s) => (app.live = s),
      onMessage: applyLive,
      onSignedOut: signedOut,
    });
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) return signedOut();
    app.phase = "unreachable";
    app.signinError = errorText(e);
    retry = setTimeout(() => void boot(), 4000);
  }
}

/** Groups are optional: a hub without them still shows the fleet. */
async function loadGroups(): Promise<Group[]> {
  try {
    return (await api.groups()).groups ?? [];
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) throw e;
    return [];
  }
}

export async function signOut() {
  try {
    await api.signout();
  } catch (e) {
    if (!(e instanceof ApiError && e.status === 401)) return notify(`Sign-out failed: ${errorText(e)}`, "error");
  }
  signedOut();
}
