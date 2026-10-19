// The hub's HTTP API. JSON both ways, a session cookie for auth, and the CSRF token
// from /api/me on every request that changes something. A 401 from anywhere means
// the browser is signed out.

import type {
  ActionRequest,
  ActionResult,
  JoinLink,
  Machine,
  Me,
  NewLink,
} from "./types";

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

export function setCsrf(token: string) {
  csrf = token;
}

/** Called once, by the app, with what to do when the hub says we are signed out. */
export function whenSignedOut(fn: () => void) {
  onSignedOut = fn;
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
};

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
