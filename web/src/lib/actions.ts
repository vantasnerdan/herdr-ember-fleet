// herdr actions on a pane, with honest results. Nothing here retries: a request that
// may have reached the pane is reported as "sent, outcome unknown", and the person
// decides what to do next by looking at the screen.

import { api } from "./api";
import type { PaneInfo } from "./fleet";
import { notify, refreshFleet } from "./store";
import type { ActionMethod } from "./types";

export type Outcome = "ok" | "unknown" | "not_sent";

const REASONS: Record<string, string> = {
  agent_blocked: "the agent is blocked, waiting for an answer on its screen",
  stale: "herdr restarted on that machine since this view loaded; the fleet has been refreshed",
  epoch_mismatch: "herdr restarted on that machine since this view loaded; the fleet has been refreshed",
  forbidden: "your role on that machine does not allow it",
  not_allowed: "your role on that machine does not allow it",
  offline: "that machine is offline",
  pane_not_found: "that pane no longer exists",
};

export function explain(error: string): string {
  return REASONS[error] ?? error;
}

export async function act(
  p: PaneInfo,
  method: ActionMethod,
  params: Record<string, unknown>,
  what: string,
): Promise<{ outcome: Outcome; error?: string }> {
  const r = await api.action({
    dock: p.dock.id,
    session: p.session.name,
    epoch: p.session.epoch,
    method,
    params,
  });
  if (r.ok) return { outcome: "ok" };
  if (r.error === "stale" || r.error === "epoch_mismatch") void refreshFleet();
  if (r.outcome === "unknown") {
    notify(`${what}: sent, outcome unknown — check the screen. (${explain(r.error)})`, "warn", 9000);
    return { outcome: "unknown", error: r.error };
  }
  notify(`${what} was not sent: ${explain(r.error)}.`, "error");
  return { outcome: "not_sent", error: r.error };
}
