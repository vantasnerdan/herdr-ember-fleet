<script setup lang="ts">
// Ctrl/Cmd-K: find an agent, a machine or a page by typing, and go there. With nothing
// typed it offers agents that need you, then the ones opened lately, then the pages.
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { ArrowRight, LayoutDashboard, List, Plus, Search, Server, Settings } from "@lucide/vue";
import DeviceIcon from "./DeviceIcon.vue";
import HarnessIcon from "./HarnessIcon.vue";
import StatePill from "./StatePill.vue";
import type { PaneInfo } from "../lib/fleet";
import { groupOf, memberKey } from "../lib/groups";
import {
  app,
  machines,
  openPane,
  paneIndex,
  panes,
  recent,
  showAgents,
  showMachine,
  showMachines,
  showOverview,
  showSettings,
} from "../lib/store";

interface Item {
  id: string;
  kind: "agent" | "machine" | "page";
  label: string;
  detail: string;
  hay: string;
  pane?: PaneInfo;
  os?: string;
  icon?: unknown;
  run: () => void;
}

const query = ref("");
const active = ref(0);
const input = ref<HTMLInputElement>();
const list = ref<HTMLElement>();

const groupName = (p: PaneInfo) =>
  groupOf.value.get(memberKey({ kind: "pane", ...p.ref }))?.name ??
  groupOf.value.get(memberKey({ kind: "dock", dock: p.dock.id }))?.name ??
  "";

const agentItems = computed<Item[]>(() =>
  panes.value
    .filter((p) => p.harness || query.value.trim())
    .map((p) => {
      const where = [p.host, p.dock.user !== p.host ? p.dock.user : "", p.workspace?.label].filter(Boolean).join(" · ");
      const group = groupName(p);
      return {
        id: `a:${p.key}`,
        kind: "agent",
        label: p.label,
        detail: [where, p.title || p.cwd].filter(Boolean).join(" — "),
        hay: [p.label, p.harness ?? "shell", p.title, p.cwd, p.host, p.dock.user, p.workspace?.label, group, p.status].join(" "),
        pane: p,
        run: () => openPane(p.ref),
      };
    }),
);

const machineItems = computed<Item[]>(() =>
  machines.value.map((m) => ({
    id: `m:${m.host}`,
    kind: "machine",
    label: m.host,
    detail: `Machine · ${m.docks.map((d) => d.user).join(", ")}`,
    hay: `${m.host} machine ${m.docks.map((d) => `${d.user} ${d.name}`).join(" ")}`,
    os: m.docks[0]?.os,
    run: () => showMachine(m.host),
  })),
);

const pageItems: Item[] = [
  { id: "p:overview", kind: "page", label: "Overview", detail: "The whole fleet at a glance", hay: "overview home dashboard", icon: LayoutDashboard, run: showOverview },
  { id: "p:agents", kind: "page", label: "All agents", detail: "Every agent in one list", hay: "all agents list table", icon: List, run: () => showAgents() },
  { id: "p:needs", kind: "page", label: "Needs you", detail: "Agents waiting for your input", hay: "needs you blocked waiting", icon: List, run: () => showAgents("blocked") },
  { id: "p:machines", kind: "page", label: "Machines", detail: "Every machine and its versions", hay: "machines computers hosts versions", icon: Server, run: showMachines },
  { id: "p:add", kind: "page", label: "Add a machine", detail: "Join another computer to the fleet", hay: "add machine join new computer link", icon: Plus, run: () => (app.adding = true) },
  { id: "p:settings", kind: "page", label: "Settings", detail: "Fleet name and more", hay: "settings preferences general", icon: Settings, run: () => showSettings() },
  { id: "p:pin", kind: "page", label: "Sign-in PIN", detail: "Settings › Signing in", hay: "pin sign in signing laptop password", icon: Settings, run: () => showSettings("signin") },
  { id: "p:browsers", kind: "page", label: "Signed-in browsers", detail: "Settings › Browsers", hay: "browsers sessions sign out devices", icon: Settings, run: () => showSettings("browsers") },
  { id: "p:updates", kind: "page", label: "Updates", detail: "Settings › Updates", hay: "updates versions upgrade herdr", icon: Settings, run: () => showSettings("updates") },
];

/** Higher is better: the name's start, then a word in the name, then anywhere. */
function score(item: Item, words: string[]): number {
  const label = item.label.toLowerCase();
  const hay = `${label} ${item.hay.toLowerCase()}`;
  let s = 0;
  for (const w of words) {
    if (!hay.includes(w)) return -1;
    if (label.startsWith(w)) s += 4;
    else if (label.split(/[^a-z0-9]+/).some((part) => part.startsWith(w))) s += 3;
    else if (label.includes(w)) s += 2;
    else s += 1;
  }
  if (item.pane?.status === "blocked") s += 0.5;
  if (item.pane && !item.pane.online) s -= 1;
  if (item.kind === "page") s -= 0.25;
  return s;
}

const results = computed<{ title: string; items: Item[] }[]>(() => {
  const words = query.value.toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) {
    const needs = agentItems.value.filter((i) => i.pane?.online && i.pane.status === "blocked");
    const recentItems = recent.value
      .map((k) => paneIndex.value.get(k))
      .filter((p): p is PaneInfo => !!p && !needs.some((n) => n.pane?.key === p.key))
      .map((p) => agentItems.value.find((i) => i.pane?.key === p.key))
      .filter((i): i is Item => !!i);
    return [
      { title: "Needs you", items: needs },
      { title: "Recent", items: recentItems },
      { title: "Go to", items: pageItems.slice(0, 6) },
    ].filter((g) => g.items.length);
  }
  const ranked = [...agentItems.value, ...machineItems.value, ...pageItems]
    .map((item) => ({ item, s: score(item, words) }))
    .filter((x) => x.s >= 0)
    .sort((a, b) => b.s - a.s || a.item.label.localeCompare(b.item.label))
    .slice(0, 40)
    .map((x) => x.item);
  return ranked.length ? [{ title: "Results", items: ranked }] : [];
});
const flat = computed(() => results.value.flatMap((g) => g.items));

watch(query, () => (active.value = 0));

// Focus: starts in the search box, stays inside the palette while it is open, and goes back
// where it was (a terminal in control, say) when the palette closes without going anywhere.
let before: HTMLElement | null = null;
let went = false;
const box = ref<HTMLElement>();

function keepFocusInside(e: FocusEvent) {
  if (app.palette && box.value && !box.value.contains(e.target as Node)) input.value?.focus();
}
document.addEventListener("focusin", keepFocusInside);
onBeforeUnmount(() => document.removeEventListener("focusin", keepFocusInside));

watch(
  () => app.palette,
  async (open) => {
    if (open) {
      before = document.activeElement as HTMLElement | null;
      went = false;
      query.value = "";
      active.value = 0;
      await nextTick();
      input.value?.focus();
      return;
    }
    if (!went && before?.isConnected) before.focus({ preventScroll: true });
    before = null;
  },
  { immediate: true },
);

function close() {
  app.palette = false;
}
function choose(item?: Item) {
  if (!item) return;
  went = true;
  close();
  item.run();
}

/** Keys anywhere in the palette: Escape closes it, Tab stays in the search box. */
function dialogKeys(e: KeyboardEvent) {
  if (e.key === "Escape") close();
  else if (e.key === "Tab") input.value?.focus();
  else return;
  e.preventDefault();
  e.stopPropagation();
}
function move(by: number) {
  const n = flat.value.length;
  if (!n) return;
  active.value = (active.value + by + n) % n;
  void nextTick(() => list.value?.querySelector(".palette-item.active")?.scrollIntoView({ block: "nearest" }));
}
function keydown(e: KeyboardEvent) {
  if (e.key === "ArrowDown") move(1);
  else if (e.key === "ArrowUp") move(-1);
  else if (e.key === "Enter") choose(flat.value[active.value]);
  else return;
  e.preventDefault();
}
const indexOf = (item: Item) => flat.value.indexOf(item);
</script>

<template>
  <div v-if="app.palette" class="palette-backdrop" @mousedown.self="close" @keydown="dialogKeys">
    <div ref="box" class="palette" role="dialog" aria-modal="true" aria-label="Go to an agent, machine or page">
      <div class="palette-input">
        <Search :size="17" />
        <input
          ref="input"
          v-model="query"
          type="text"
          name="palette"
          placeholder="Find an agent, machine or page…"
          spellcheck="false"
          autocomplete="off"
          role="combobox"
          aria-expanded="true"
          aria-controls="palette-list"
          :aria-activedescendant="flat[active] ? `pi-${indexOf(flat[active])}` : undefined"
          @keydown="keydown"
        />
        <kbd>Esc</kbd>
      </div>
      <div id="palette-list" ref="list" class="palette-list" role="listbox" tabindex="-1" aria-label="Results">
        <template v-for="g in results" :key="g.title">
          <div class="palette-group">{{ g.title }}</div>
          <button
            v-for="item in g.items"
            :id="`pi-${indexOf(item)}`"
            :key="item.id"
            class="palette-item"
            :class="{ active: indexOf(item) === active, offline: item.pane && !item.pane.online }"
            role="option"
            :aria-selected="indexOf(item) === active"
            tabindex="-1"
            @mousemove="active = indexOf(item)"
            @click="choose(item)"
          >
            <HarnessIcon v-if="item.kind === 'agent'" :harness="item.pane?.harness" />
            <span v-else-if="item.kind === 'machine'" class="pi-icon"><DeviceIcon :os="item.os" :size="17" /></span>
            <span v-else class="pi-icon"><component :is="item.icon" :size="17" /></span>
            <span class="pi-text"><b>{{ item.label }}</b><small>{{ item.detail }}</small></span>
            <StatePill v-if="item.pane?.harness" :status="item.pane.status" :offline="!item.pane.online" />
            <ArrowRight v-else class="pi-go" :size="15" />
          </button>
        </template>
        <p v-if="!flat.length" class="palette-empty">Nothing matches “{{ query }}”.</p>
      </div>
      <footer class="palette-foot"><span><kbd>↑</kbd><kbd>↓</kbd> choose</span><span><kbd>Enter</kbd> open</span></footer>
    </div>
  </div>
</template>
