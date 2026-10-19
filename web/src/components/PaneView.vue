<script setup lang="ts">
// One pane: where it lives, its live terminal (watching, or in control), the key
// buttons and the prompt bar.
import { computed, defineAsyncComponent, ref, shallowRef, watch } from "vue";
import { ArrowLeft, Keyboard, LogOut, Maximize2, SearchX, TriangleAlert, Info, OctagonAlert } from "@lucide/vue";
import HarnessIcon from "./HarnessIcon.vue";
import StatePill from "./StatePill.vue";
import KeyBar from "./KeyBar.vue";
import PromptBar from "./PromptBar.vue";
import PaneMenu from "./PaneMenu.vue";
import { closePane, confirmAsk, paneIndex } from "../lib/store";
import { harnessOf, type PaneInfo } from "../lib/fleet";
import type { TermState } from "../lib/term";

const props = defineProps<{ paneKey: string }>();

const info = computed(() => paneIndex.value.get(props.paneKey));
// Keep showing the last known pane if it leaves the snapshot, so the view can say so.
const last = shallowRef<PaneInfo>();
watch(info, (i) => i && (last.value = i), { immediate: true });
const p = computed(() => info.value ?? last.value);
const gone = computed(() => !info.value && !!last.value);

// xterm and its WebGL renderer load with the first pane, not with the fleet tree.
const TerminalView = defineAsyncComponent(() => import("./TerminalView.vue"));
interface TerminalControls {
  takeControl: (takeover?: boolean) => void;
  release: () => void;
  fit: () => void;
  dismiss: () => void;
}
const term = ref<TerminalControls>();
const state = ref<TermState>();
const control = computed(() => state.value?.mode === "control");
const controlling = computed(() => control.value && state.value?.phase === "open");

const why = computed(() => {
  const x = p.value;
  if (!x) return "";
  if (gone.value) return "This pane has closed";
  if (!x.dock.online) return `${x.dock.user}@${x.host} is offline`;
  if (!x.online) return "herdr is not reachable";
  if (x.dock.role === "viewer") return `Viewer on ${x.host}: read-only`;
  return "";
});
const canInput = computed(() => !why.value);
const multiTab = computed(() => (p.value?.workspace?.tab_count ?? 1) > 1);

function take() {
  term.value?.takeControl(false);
}

async function takeOver() {
  const x = p.value;
  if (!x) return;
  const ok = await confirmAsk({
    eyebrow: `${x.host} · ${x.dock.user}`,
    title: "Take over this pane?",
    body: `Someone else is in control of ${x.name}${state.value?.busy && state.value.busy !== "another controller" ? ` (${state.value.busy})` : ""}. Taking over disconnects them, and the pane switches to this window’s control.`,
    confirm: "Take over",
    danger: true,
  });
  if (ok) term.value?.takeControl(true);
}

/** What the header calls each level, skipping the ones the tree skips. */
const crumbs = computed(() => {
  const x = p.value;
  if (!x) return [];
  const out = [x.dock.user];
  if (x.session.name !== "default") out.push(x.session.name);
  if (x.workspace?.label) out.push(x.workspace.label);
  if (multiTab.value && x.tab?.label) out.push(x.tab.label);
  return out;
});
</script>

<template>
  <div v-if="p" class="pane-view">
    <header class="pane-head">
      <button class="icon-button back" aria-label="Back to the fleet" @click="closePane"><ArrowLeft :size="20" /></button>
      <HarnessIcon :harness="p.harness" />
      <div class="pane-title">
        <div class="crumbs">
          <span class="host">{{ p.host }}</span>
          <template v-for="c in crumbs" :key="c"><span class="slash">/</span><span>{{ c }}</span></template>
        </div>
        <h1>
          <span class="label">{{ p.name }}</span>
          <span v-if="p.pane.label && p.harness" class="harness-name">{{ harnessOf(p.harness)?.name }}</span>
          <StatePill v-if="p.harness" :status="p.status" :offline="!p.online" />
        </h1>
        <div class="screen-title">
          {{ p.title }}<template v-if="p.title && p.cwd"> · </template>{{ p.cwd }}
        </div>
      </div>
      <div class="pane-actions">
        <span class="mode-badge" :class="{ control: controlling }"
          ><i /><span class="label">{{ controlling ? "In control" : "Watching" }}</span></span
        >
        <button
          v-if="!control"
          class="secondary small"
          :disabled="!canInput || state?.phase === 'stopped'"
          :title="why || 'Type into this pane. A controller sets the pane’s real size; others keep watching.'"
          @click="take"
        >
          <Keyboard :size="15" />Take control
        </button>
        <template v-else>
          <button
            class="secondary small fit"
            :disabled="!controlling"
            title="Resize the real pane to fit this window (everyone sees the new size)"
            @click="term?.fit()"
          >
            <Maximize2 :size="14" /><span class="label">Fit to window</span>
          </button>
          <button class="primary small" title="Give the pane back and keep watching" @click="term?.release()">
            <LogOut :size="14" />Release
          </button>
        </template>
        <PaneMenu :pane="p" :disabled="!canInput" :tabs="multiTab" />
      </div>
    </header>

    <div class="notices" aria-live="polite">
      <div v-if="state?.lostInput" class="notice warn" role="alert">
        <TriangleAlert :size="16" />
        <span>Connection lost — what you typed last may or may not have arrived. You are watching again; check the screen.</span>
        <button class="secondary small" @click="term?.dismiss()">OK</button>
      </div>
      <div v-if="state?.busy" class="notice blocked">
        <OctagonAlert :size="16" />
        <span>Someone else is in control of this pane<template v-if="state.busy !== 'another controller'"> ({{ state.busy }})</template>. You are watching.</span>
        <button class="secondary small" :disabled="!canInput" @click="takeOver">Take over…</button>
        <button class="icon-button" aria-label="Dismiss" @click="term?.dismiss()">×</button>
      </div>
      <div v-if="state?.controlEnded && !state.lostInput" class="notice info">
        <Info :size="16" />
        <span>Control ended: {{ state.controlEnded }}. You are watching again.</span>
        <button class="icon-button" aria-label="Dismiss" @click="term?.dismiss()">×</button>
      </div>
    </div>

    <TerminalView ref="term" :pane="p" :gone="gone" @state="state = $event" />

    <footer class="pane-foot">
      <KeyBar :pane="p" :disabled="!canInput" :why="why || (p.harness || controlling ? '' : 'A plain shell: take control to type')" />
      <PromptBar v-if="p.harness" :pane="p" :disabled="!canInput" />
    </footer>
  </div>

  <div v-else class="pane-missing">
    <SearchX :size="28" />
    <h2>That pane isn’t in the fleet</h2>
    <p class="hint">It may have closed, or its machine may not have reported in yet.</p>
    <button class="secondary" @click="closePane"><ArrowLeft :size="15" />Back to the fleet</button>
  </div>
</template>
