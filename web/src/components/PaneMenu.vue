<script setup lang="ts">
// Rename the pane, its tab or its workspace; close the pane (asks first).
import { onBeforeUnmount, ref } from "vue";
import { Ellipsis, Pencil, X } from "@lucide/vue";
import { act } from "../lib/actions";
import { harnessOf, type PaneInfo } from "../lib/fleet";
import { confirmAsk, notify, promptAsk } from "../lib/store";

const props = defineProps<{ pane: PaneInfo; disabled: boolean; tabs: boolean }>();
const open = ref(false);
const wrap = ref<HTMLElement>();

function outside(e: Event) {
  if (wrap.value && !wrap.value.contains(e.target as Node)) open.value = false;
}
function toggle() {
  open.value = !open.value;
  if (open.value) document.addEventListener("pointerdown", outside, true);
  else document.removeEventListener("pointerdown", outside, true);
}
function close() {
  open.value = false;
  document.removeEventListener("pointerdown", outside, true);
}
onBeforeUnmount(close);

async function rename(kind: "pane" | "tab" | "workspace") {
  close();
  const p = props.pane;
  const current = kind === "pane" ? (p.pane.label ?? "") : kind === "tab" ? (p.tab?.label ?? "") : (p.workspace?.label ?? "");
  const label = await promptAsk({
    eyebrow: `${p.host} · ${p.dock.user}`,
    title: `Rename ${kind}`,
    confirm: "Rename",
    input: { label: `New ${kind} name`, value: current, placeholder: kind === "pane" ? p.name : current, maxlength: 60 },
  });
  if (!label) return;
  const r =
    kind === "pane"
      ? await act(p, "pane.rename", { pane_id: p.ref.pane, label }, "Rename")
      : kind === "tab"
        ? await act(p, "tab.rename", { tab_id: p.pane.tab_id, label }, "Rename")
        : await act(p, "workspace.rename", { workspace_id: p.pane.workspace_id, label }, "Rename");
  if (r.outcome === "ok") notify(`Renamed to ${label}.`, "ok");
}

async function closePane() {
  close();
  const p = props.pane;
  const ok = await confirmAsk({
    eyebrow: `${p.host} · ${p.dock.user}`,
    title: `Close ${p.name}?`,
    body: `This closes the pane in herdr on ${p.host}${p.harness ? `, and ends ${harnessOf(p.harness)?.name ?? p.harness} running in it` : ""}. It can’t be undone from here.`,
    confirm: "Close pane",
    danger: true,
  });
  if (!ok) return;
  const r = await act(p, "pane.close", { pane_id: p.ref.pane }, "Close");
  if (r.outcome === "ok") notify(`Closed ${p.name}.`, "ok");
}
</script>

<template>
  <div ref="wrap" class="menu-wrap">
    <button
      class="icon-button"
      :aria-expanded="open"
      aria-haspopup="menu"
      aria-label="More pane actions"
      title="Rename, close"
      @click="toggle"
      @keydown.esc="close"
    >
      <Ellipsis :size="18" />
    </button>
    <div v-if="open" class="menu" role="menu" @keydown.esc="close">
      <button role="menuitem" :disabled="disabled" @click="rename('pane')"><Pencil :size="14" />Rename pane…</button>
      <button v-if="tabs && pane.tab" role="menuitem" :disabled="disabled" @click="rename('tab')">
        <Pencil :size="14" />Rename tab…
      </button>
      <button v-if="pane.workspace" role="menuitem" :disabled="disabled" @click="rename('workspace')">
        <Pencil :size="14" />Rename workspace…
      </button>
      <hr />
      <button role="menuitem" class="danger-text" :disabled="disabled" @click="closePane"><X :size="14" />Close pane…</button>
    </div>
  </div>
</template>
