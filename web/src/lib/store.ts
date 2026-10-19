// App state: who is signed in, the fleet as the hub last described it, which pane is
// open, and the small dialogs and notices every component may raise.

import { computed, reactive, ref, shallowRef } from "vue";
import { api, ApiError, errorText, setCsrf, whenFleetRenamed, whenSignedOut } from "./api";
import { allPanes, buildTree, parseRefKey, refKey, type PaneInfo, type PaneRef } from "./fleet";
import type { Group, LiveMessage, Machine, Me } from "./types";
import { connectLive } from "./live";

export type Phase = "loading" | "signed-out" | "signing-in" | "signin-failed" | "unreachable" | "ready";
export type LiveState = "connecting" | "live" | "reconnecting";

export const app = reactive({
  phase: "loading" as Phase,
  signinError: "",
  me: null as Me | null,
  live: "connecting" as LiveState,
  /** Fleet settings: which view is open, if any. */
  settings: null as null | "fleet" | "add",
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

// ---------------------------------------------------------------- the open pane (#/p/<dock>/<session>/<pane>)

export const selectedKey = ref<string | null>(keyFromHash());
export const selectedPane = computed(() =>
  selectedKey.value ? paneIndex.value.get(selectedKey.value) : undefined,
);
export const selectedRef = computed<PaneRef | undefined>(() =>
  selectedKey.value ? parseRefKey(selectedKey.value) : undefined,
);

function keyFromHash(): string | null {
  const m = /^#\/p\/(.+)$/.exec(location.hash);
  return m && parseRefKey(m[1]) ? m[1] : null;
}

export function openPane(ref: PaneRef) {
  const key = refKey(ref);
  // Opening a pane never unfolds the machine tree; the viewer folds and
  // unfolds machines by hand.
  if (selectedKey.value === key) return;
  history.pushState({ ember: true }, "", `${location.pathname}${location.search}#/p/${key}`);
  selectedKey.value = key;
}

/** Back to the tree (on a phone) or to the overview. */
export function closePane() {
  if (!selectedKey.value) return;
  if ((history.state as { ember?: boolean } | null)?.ember) history.back();
  else {
    history.replaceState(null, "", `${location.pathname}${location.search}`);
    selectedKey.value = null;
  }
}

window.addEventListener("popstate", () => {
  selectedKey.value = keyFromHash();
});

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
  app.settings = null;
  if (app.phase !== "signin-failed") app.phase = "signed-out";
}
whenSignedOut(signedOut);

function fleetRenamed(name: string) {
  if (app.me && name) app.me.fleet = name;
}
whenFleetRenamed(fleetRenamed);

export function applyLive(msg: LiveMessage) {
  if (msg.type === "hello") {
    machines.value = msg.fleet.machines ?? [];
    if (msg.groups) groups.value = msg.groups;
  } else if (msg.type === "groups") groups.value = msg.groups ?? [];
  else if (msg.type === "fleet") fleetRenamed(msg.name);
  else if (msg.type === "machine") {
    const list = machines.value.filter((m) => m.host !== msg.machine.host);
    machines.value = msg.machine.docks.length ? [...list, msg.machine] : list;
  } else if (msg.type === "removed") {
    const host = msg.host ?? msg.id;
    machines.value = machines.value.filter((m) => m.host !== host && !m.docks.some((d) => d.id === host));
  }
}

/** Re-reads the whole fleet, for when a live update may have been missed. */
export async function refreshFleet() {
  try {
    machines.value = (await api.fleet()).machines ?? [];
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
    if (!me.signedIn) return signedOut();
    setCsrf(me.csrf);
    app.me = me;
    machines.value = (await api.fleet()).machines ?? [];
    groups.value = await loadGroups();
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
