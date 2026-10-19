// Browser notifications when an agent turns blocked, anywhere in the fleet. Only
// changes are announced: an agent already blocked when the page loads is in the
// "Needs you" list, not in a notification. (App.vue owns the page title and its count.)

import { reactive, watch } from "vue";
import { openPane, panes, save } from "./store";
import type { PaneInfo } from "./fleet";

const supported = typeof window !== "undefined" && "Notification" in window;

function stored(): boolean {
  try {
    return localStorage.getItem("ember.alerts") === "1";
  } catch {
    return false;
  }
}

export const alerts = reactive({
  supported,
  permission: (supported ? Notification.permission : "denied") as NotificationPermission,
  /** The viewer's choice; notifications also need the browser's permission. */
  wanted: stored(),
  get on(): boolean {
    return this.supported && this.wanted && this.permission === "granted";
  },
});

export async function enableAlerts() {
  if (!supported) return;
  if (Notification.permission === "default") alerts.permission = await Notification.requestPermission();
  else alerts.permission = Notification.permission;
  alerts.wanted = alerts.permission === "granted";
  save("ember.alerts", alerts.wanted ? 1 : 0);
}

export function disableAlerts() {
  alerts.wanted = false;
  save("ember.alerts", 0);
}

function announce(p: PaneInfo) {
  const user = p.dock.user;
  const where = [user, p.workspace?.label].filter(Boolean).join(" · ");
  try {
    const n = new Notification(`${p.host} · ${p.name} needs you`, {
      body: [where, p.title].filter(Boolean).join("\n"),
      tag: `ember-blocked:${p.key}`,
      icon: "/mark.svg",
    });
    n.onclick = () => {
      window.focus();
      openPane(p.ref);
      n.close();
    };
  } catch {
    // some browsers only allow notifications from a service worker
  }
}

let seen: Map<string, string> | undefined;

export function watchAlerts() {
  watch(
    panes,
    (list) => {
      const next = new Map(list.map((p) => [p.key, p.status]));
      if (seen && alerts.on) {
        for (const p of list) {
          if (p.harness && p.online && p.status === "blocked" && seen.get(p.key) !== "blocked") announce(p);
        }
      }
      // An empty fleet (signed out, first load) is not a baseline to compare against.
      seen = list.length ? next : undefined;
    },
    { immediate: true },
  );
  document.addEventListener("visibilitychange", () => {
    if (supported) alerts.permission = Notification.permission;
  });
}
