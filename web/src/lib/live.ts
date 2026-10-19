// WS /api/live: the fleet as it changes. Reconnects with backoff (0.5 s doubling to
// 15 s, with jitter), and sooner when the page comes back to the foreground or the
// network returns. A socket that keeps failing may mean the session ended, so
// /api/me is asked before each retry after the first.

import { api, ApiError, openSocket } from "./api";
import type { LiveMessage } from "./types";
import type { LiveState } from "./store";

interface Handlers {
  onState: (s: LiveState) => void;
  onMessage: (m: LiveMessage) => void;
  onSignedOut: () => void;
}

export function connectLive({ onState, onMessage, onSignedOut }: Handlers): () => void {
  let ws: WebSocket | undefined;
  let attempt = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;

  const open = () => {
    timer = undefined;
    if (stopped) return;
    onState(attempt ? "reconnecting" : "connecting");
    const sock = openSocket("/api/live");
    ws = sock;
    sock.onopen = () => {
      attempt = 0;
      onState("live");
    };
    sock.onmessage = (e) => {
      if (typeof e.data !== "string") return;
      try {
        onMessage(JSON.parse(e.data) as LiveMessage);
      } catch {
        // one bad message is not worth dropping the channel for
      }
    };
    sock.onclose = () => {
      if (ws !== sock || stopped) return;
      ws = undefined;
      onState("reconnecting");
      schedule();
    };
  };

  const schedule = () => {
    attempt++;
    const delay = Math.min(15_000, 500 * 2 ** (attempt - 1)) * (0.75 + Math.random() * 0.5);
    timer = setTimeout(() => void retry(), delay);
  };

  const retry = async () => {
    timer = undefined;
    if (attempt > 1) {
      try {
        const me = await api.me();
        if (!me.signedIn) return onSignedOut();
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) return; // api.ts has already signed out
        // the hub is unreachable: keep trying
      }
    }
    open();
  };

  const soon = () => {
    if (stopped || ws || !timer || document.hidden) return;
    clearTimeout(timer);
    void retry();
  };
  document.addEventListener("visibilitychange", soon);
  window.addEventListener("online", soon);

  open();
  return () => {
    stopped = true;
    clearTimeout(timer);
    document.removeEventListener("visibilitychange", soon);
    window.removeEventListener("online", soon);
    ws?.close();
    ws = undefined;
  };
}
