// The hub's HTTP API. JSON both ways, a session cookie for auth, and the CSRF token
// from /api/me on every request that changes something. A 401 from anywhere means
// the browser is signed out.

import type { ActionRequest, ActionResult, Group, JoinLink, Machine, Me, Member, NewLink, Uploaded, UploadTarget, UpdateSettings, UpdateStatus, SigninAsk } from "./types";

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly code?: string,
  ) {
    super(message);
  }
}

let csrf = "";
let onSignedOut: () => void = () => {};
let onFleetRenamed: (name: string) => void = () => {};

export function setCsrf(token: string) {
  csrf = token;
}

/** Called once, by the app, with what to do when the hub says we are signed out. */
export function whenSignedOut(fn: () => void) {
  onSignedOut = fn;
}

/** Called once, by the app: the fleet's new name, after a rename from this page. */
export function whenFleetRenamed(fn: (name: string) => void) {
  onFleetRenamed = fn;
}

interface Options {
  body?: unknown;
  /** Sign-in has no session yet, so no CSRF token and no signed-out handling. */
  anonymous?: boolean;
}

async function call<T>(method: string, path: string, { body, anonymous }: Options = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (method !== "GET" && !anonymous) headers["X-Ember-CSRF"] = csrf;
  const res = await fetch(path, {
    method,
    headers,
    credentials: "same-origin",
    cache: "no-store",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await readJson(res);
  if (res.status === 401 && !anonymous) {
    onSignedOut();
    throw new ApiError(401, "Signed out", "signed_out");
  }
  if (!res.ok) {
    const err = (data && typeof data === "object" ? data : {}) as { error?: string; message?: string; code?: string };
    throw new ApiError(res.status, err.error ?? err.message ?? `${res.status} ${res.statusText}`, err.code ?? err.error);
  }
  return data as T;
}

async function readJson(res: Response): Promise<unknown> {
  const text = await res.text();
  if (!text) return undefined;
  try {
    return JSON.parse(text);
  } catch {
    return { error: text.slice(0, 200) };
  }
}

export const api = {
  me: () => call<Me>("GET", "/api/me"),
  signin: (token: string) => call<unknown>("POST", "/api/signin", { body: { token }, anonymous: true }),
  signout: () => call<unknown>("POST", "/api/signout"),
  fleet: () => call<{ machines: Machine[] }>("GET", "/api/fleet"),

  /**
   * One herdr action. Never retried: when the request may have reached the hub but no
   * answer came back, the outcome is "unknown" and the page says so.
   */
  async action(req: ActionRequest): Promise<ActionResult> {
    let res: Response;
    try {
      res = await fetch("/api/action", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json", "X-Ember-CSRF": csrf },
        credentials: "same-origin",
        cache: "no-store",
        body: JSON.stringify(req),
      });
    } catch {
      return { ok: false, error: "The connection to the hub dropped", outcome: "unknown" };
    }
    if (res.status === 401) {
      onSignedOut();
      return { ok: false, error: "Signed out", outcome: "not_sent" };
    }
    const data = (await readJson(res)) as Partial<ActionResult> & { error?: string } | undefined;
    if (data && typeof data === "object" && "ok" in data) return data as ActionResult;
    if (res.ok) return { ok: true, result: data };
    // A status without an answer we understand: the hub refused before sending
    // (4xx) or something failed on the way (5xx).
    return {
      ok: false,
      error: data?.error ?? `${res.status} ${res.statusText}`,
      outcome: res.status < 500 ? "not_sent" : "unknown",
    };
  },

  links: () => call<{ links: JoinLink[] }>("GET", "/api/links"),
  createLink: (name: string) => call<NewLink>("POST", "/api/links", { body: { name } }),
  cancelLink: (id: string) => call<unknown>("DELETE", `/api/links/${encodeURIComponent(id)}`),
  removeDock: (id: string) => call<unknown>("DELETE", `/api/docks/${encodeURIComponent(id)}`),
  renameDock: (id: string, name: string) =>
    call<unknown>("POST", `/api/docks/${encodeURIComponent(id)}/rename`, { body: { name } }),
  createToken: (name: string) => call<{ token: string }>("POST", "/api/tokens", { body: { name } }),

  /** Renames the fleet; the page shows the new name at once (and again from the live channel). */
  async renameFleet(name: string) {
    const r = await call<{ ok: boolean; name?: string }>("POST", "/api/fleet", { body: { name } });
    onFleetRenamed(r?.name ?? name);
    return r;
  },

  groups: () => call<{ groups: Group[] }>("GET", "/api/groups"),
  createGroup: (name: string, color = "") => call<{ group: Group }>("POST", "/api/groups", { body: { name, color } }),
  updateGroup: (id: string, patch: { name?: string; color?: string }) =>
    call<{ group: Group }>("PATCH", `/api/groups/${encodeURIComponent(id)}`, { body: patch }),
  deleteGroup: (id: string) => call<unknown>("DELETE", `/api/groups/${encodeURIComponent(id)}`),
  /** Puts the member into the group at index (default: the end), out of any other; null ungroups it. */
  moveMember: (member: Member, group: string | null, index?: number) =>
    call<{ groups: Group[] }>("POST", "/api/groups/move", {
      body: index === undefined ? { member, group } : { member, group, index },
    }),
  reorderGroups: (ids: string[]) => call<{ groups: Group[] }>("POST", "/api/groups/reorder", { body: { ids } }),

  /** A browser that isn't signed in asks to be approved by one that is. */
  askSignin: () => call<{ id: string; code: string; poll: string }>("POST", "/api/signin/ask", { body: {}, anonymous: true }),
  pollSignin: (id: string, poll: string) =>
    call<{ state: "pending" | "approved" | "denied" | "expired"; csrf?: string }>("POST", "/api/signin/poll", {
      body: { id, poll },
      anonymous: true,
    }),
  signinRequests: () => call<{ requests: SigninAsk[] }>("GET", "/api/signin/requests"),
  decideSignin: (id: string, approve: boolean) => call<unknown>("POST", "/api/signin/decide", { body: { id, approve } }),

  updates: () => call<UpdateStatus>("GET", "/api/updates"),
  setUpdates: (s: Partial<UpdateSettings>) => call<UpdateStatus>("POST", "/api/updates", { body: s }),
  runUpdates: () => call<UpdateStatus>("POST", "/api/updates/run"),
};

export const MAX_UPLOAD = 50 * 1024 * 1024;

/**
 * Sends one file to the agent's machine (POST /api/upload, the raw file as the body).
 * XHR rather than fetch, for upload progress. The dock answers with the absolute path.
 */
export function uploadFile(
  target: UploadTarget,
  file: Blob,
  name: string,
  onProgress: (fraction: number) => void,
): { done: Promise<Uploaded>; abort: () => void } {
  const xhr = new XMLHttpRequest();
  const done = new Promise<Uploaded>((resolve, reject) => {
    if (file.size > MAX_UPLOAD) {
      reject(new ApiError(413, "Over the 50 MB limit"));
      return;
    }
    xhr.open("POST", `/api/upload?${new URLSearchParams({ ...target })}`);
    xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
    xhr.setRequestHeader("X-Ember-Filename", encodeURIComponent(name));
    xhr.setRequestHeader("X-Ember-CSRF", csrf);
    xhr.setRequestHeader("Accept", "application/json");
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && e.total) onProgress(e.loaded / e.total);
    };
    xhr.onload = () => {
      let data: (Partial<Uploaded> & { ok?: boolean; error?: string }) | undefined;
      try {
        data = xhr.responseText ? JSON.parse(xhr.responseText) : undefined;
      } catch {
        data = { error: xhr.responseText.slice(0, 200) };
      }
      if (xhr.status === 401) {
        onSignedOut();
        return reject(new ApiError(401, "Signed out", "signed_out"));
      }
      if (xhr.status === 413) return reject(new ApiError(413, data?.error ?? "Over the 50 MB limit"));
      if (xhr.status >= 200 && xhr.status < 300 && data?.ok !== false && data?.path)
        return resolve({ path: data.path, name: data.name ?? name, size: data.size ?? file.size });
      reject(new ApiError(xhr.status, data?.error ?? `${xhr.status} ${xhr.statusText}`));
    };
    xhr.onerror = () => reject(new ApiError(0, "The connection to the hub dropped"));
    xhr.onabort = () => reject(new ApiError(0, "Cancelled", "aborted"));
    xhr.send(file);
  });
  return { done, abort: () => xhr.abort() };
}

export function errorText(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof Error) return e.message;
  return String(e);
}

/** ws:// or wss:// for a same-origin path. */
export function wsUrl(path: string): string {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${location.host}${path}`;
}

/** The subprotocol the hub speaks; it echoes this one back. */
export const WS_PROTOCOL = "ember.v1";

/**
 * A same-origin WebSocket to the hub. Browsers can't put headers on a WebSocket, so
 * the CSRF token rides as a second subprotocol, `csrf.<token>`; the hub checks it
 * together with the exact Origin.
 */
export function openSocket(path: string): WebSocket {
  return new WebSocket(wsUrl(path), [WS_PROTOCOL, `csrf.${csrf}`]);
}
