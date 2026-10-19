<script setup lang="ts">
// One pane: where it lives, its live terminal (watching, or in control), the key
// buttons and the prompt bar.
import { computed, defineAsyncComponent, onBeforeUnmount, ref, shallowRef, watch } from "vue";
import {
  ArrowLeft,
  ChevronLeft,
  ChevronRight,
  Expand,
  FileUp,
  Info,
  Keyboard,
  LogOut,
  Maximize2,
  OctagonAlert,
  SearchX,
  Shrink,
  TriangleAlert,
} from "@lucide/vue";
import HarnessIcon from "./HarnessIcon.vue";
import StatePill from "./StatePill.vue";
import KeyBar from "./KeyBar.vue";
import PromptBar from "./PromptBar.vue";
import PaneMenu from "./PaneMenu.vue";
import { app, closePane, confirmAsk, load, notify, openPane, paneIndex, save, showMachine } from "../lib/store";
import { agentOrder } from "../lib/groups";
import { draggingFiles, filesOf } from "../lib/attach";
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
  watchWhole: (size: { cols: number; rows: number } | null) => void;
  takeControl: (takeover?: boolean) => void;
  release: () => void;
  fit: () => void;
  dismiss: () => void;
  uploadAndType: (files: File[]) => Promise<boolean>;
}
const term = ref<TerminalControls>();
const prompt = ref<{ addFiles: (files: File[]) => void }>();
const state = ref<TermState>();
const control = computed(() => state.value?.mode === "control");
const controlling = computed(() => control.value && state.value?.phase === "open");
// While in control, Ctrl-K belongs to the terminal, not to the search palette.
watch(controlling, (c) => (app.typing = c), { immediate: true });
onBeforeUnmount(() => (app.typing = false));

// ---------------------------------------------------------------- watching the whole pane
// Watching asks for a size that fills this box at a readable font; a pane wider than that
// is cut off at the right. "Whole pane" watches it at its real size, scaled down to fit.

const wholePref = ref(load<boolean>("ember.watch-whole", false));
// The preference survives control (which always keeps the pane's real size): the terminal
// uses it whenever it watches.
const whole = computed(() => (wholePref.value ? (p.value?.size ?? null) : null));
function toggleWhole() {
  wholePref.value = !wholePref.value;
  save("ember.watch-whole", wholePref.value);
}

// ---------------------------------------------------------------- the previous and next agent

const order = computed(() => agentOrder.value.filter((x) => x.key === props.paneKey || x.online));
const at = computed(() => order.value.findIndex((x) => x.key === props.paneKey));
const prev = computed(() => (order.value.length > 1 && at.value >= 0 ? order.value[(at.value - 1 + order.value.length) % order.value.length] : undefined));
const next = computed(() => (order.value.length > 1 && at.value >= 0 ? order.value[(at.value + 1) % order.value.length] : undefined));

function stepKeys(e: KeyboardEvent) {
  if (e.defaultPrevented || e.ctrlKey || e.metaKey || e.altKey || app.palette) return;
  const t = e.target as HTMLElement | null;
  if (t?.closest?.("input, textarea, select, [contenteditable], .xterm, [role='dialog']")) return;
  if (e.key === "[" && prev.value) openPane(prev.value.ref);
  else if (e.key === "]" && next.value) openPane(next.value.ref);
  else return;
  e.preventDefault();
}
window.addEventListener("keydown", stepKeys);
onBeforeUnmount(() => window.removeEventListener("keydown", stepKeys));

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

// ---------------------------------------------------------------- files dropped on the pane
// In control they upload and their paths are typed at the cursor; otherwise they attach to
// the prompt. A drop on the prompt bar itself always attaches (PromptBar handles that one).

const dropMode = computed<"terminal" | "prompt" | "none">(() =>
  !canInput.value ? "none" : controlling.value ? "terminal" : p.value?.harness ? "prompt" : "none",
);
const filesOver = ref(false);
let depth = 0;

function onDragEnter(e: DragEvent) {
  if (!draggingFiles(e.dataTransfer)) return;
  depth++;
  filesOver.value = true;
}
function onDragOver(e: DragEvent) {
  if (!draggingFiles(e.dataTransfer)) return;
  e.preventDefault();
  if (e.dataTransfer) e.dataTransfer.dropEffect = dropMode.value === "none" ? "none" : "copy";
}
function onDragLeave(e: DragEvent) {
  if (!draggingFiles(e.dataTransfer)) return;
  depth = Math.max(0, depth - 1);
  if (!depth) filesOver.value = false;
}
function onDrop(e: DragEvent) {
  depth = 0;
  filesOver.value = false;
  if (e.defaultPrevented || !draggingFiles(e.dataTransfer)) return;
  e.preventDefault();
  const files = filesOf(e.dataTransfer);
  if (!files.length) return;
  if (dropMode.value === "terminal") void term.value?.uploadAndType(files);
  else if (dropMode.value === "prompt") prompt.value?.addFiles(files);
  else notify(why.value || "This is a plain shell: take control to drop files into it.", "warn");
}

/**
 * Where the pane lives: machine / user / session / workspace / tab, skipping levels the tree
 * skips and any that only repeat the one before ("Kelvin / kelvin / kelvin").
 */
const crumbs = computed(() => {
  const x = p.value;
  if (!x) return [];
  const out: { text: string; what: string; go?: () => void }[] = [];
  const add = (text: string | undefined, what: string, go?: () => void) => {
    if (!text) return;
    const last = out.at(-1)?.text.toLowerCase();
    if (last === text.toLowerCase()) {
      out[out.length - 1].what += ` and ${what}`;
      return;
    }
    out.push({ text, what, go });
  };
  add(x.host, "machine", () => showMachine(x.host));
  add(x.dock.user, "OS user", () => showMachine(x.host));
  if (x.session.name !== "default") add(x.session.name, "herdr session");
  add(x.workspace?.label, "workspace");
  if (multiTab.value) add(x.tab?.label, "tab");
  return out;
});
</script>

<template>
  <div
    v-if="p"
    class="pane-view"
    @dragenter="onDragEnter"
    @dragover="onDragOver"
    @dragleave="onDragLeave"
    @drop="onDrop"
  >
    <header class="pane-head">
      <button class="back-button" title="Back to the overview" aria-label="Back to the overview" @click="closePane">
        <ArrowLeft :size="18" /><span class="nav-label">Overview</span>
      </button>
      <HarnessIcon :harness="p.harness" />
      <div class="pane-title">
        <nav class="crumbs" aria-label="Where this pane lives">
          <template v-for="(c, i) in crumbs" :key="c.text + i"
            ><span v-if="i" class="slash">/</span
            ><button v-if="c.go" class="crumb" :class="{ host: !i }" :title="`${c.what}: ${c.text}. Open this machine`" @click="c.go"
              >{{ c.text }}</button
            ><span v-else class="crumb" :title="`${c.what}: ${c.text}`">{{ c.text }}</span></template
          >
        </nav>
        <h1>
          <span class="label" :title="p.label">{{ p.label }}</span>
          <span v-if="p.pane.label && p.harness" class="harness-name">{{ harnessOf(p.harness)?.name }}</span>
          <StatePill v-if="p.harness" :status="p.status" :offline="!p.online" />
        </h1>
        <div class="screen-title">
          {{ p.title }}<template v-if="p.title && p.cwd"> · </template>{{ p.cwd }}
        </div>
      </div>
      <div class="pane-actions">
        <span class="step" role="group" aria-label="Other agents">
          <button
            class="icon-button"
            :disabled="!prev"
            :title="prev ? `Previous agent: ${prev.label} ([)` : 'No other agent'"
            :aria-label="prev ? `Previous agent: ${prev.label}` : 'No previous agent'"
            @click="prev && openPane(prev.ref)"
          >
            <ChevronLeft :size="18" />
          </button>
          <button
            class="icon-button"
            :disabled="!next"
            :title="next ? `Next agent: ${next.label} (])` : 'No other agent'"
            :aria-label="next ? `Next agent: ${next.label}` : 'No next agent'"
            @click="next && openPane(next.ref)"
          >
            <ChevronRight :size="18" />
          </button>
        </span>
        <span class="mode-badge" :class="{ control: controlling }"
          ><i /><span class="label">{{ controlling ? "In control" : "Watching" }}</span></span
        >
        <button
          v-if="!control && p.size"
          class="secondary small whole"
          :aria-pressed="wholePref"
          :title="wholePref ? 'Watch at a size that fits this window (larger text; wide lines may be cut off)' : `Watch the whole pane (${p.size.cols}×${p.size.rows}), scaled down to fit`"
          @click="toggleWhole"
        >
          <Shrink v-if="wholePref" :size="14" /><Expand v-else :size="14" /><span class="label">{{ wholePref ? "Fit text" : "Whole pane" }}</span>
        </button>
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

    <TerminalView ref="term" :pane="p" :gone="gone" :whole="whole" @state="state = $event" />

    <div v-if="filesOver" class="pane-drop" :class="dropMode" aria-hidden="true">
      <FileUp :size="26" />
      <span v-if="dropMode === 'terminal'">Drop to upload and type the paths at the cursor</span>
      <span v-else-if="dropMode === 'prompt'">Drop to attach to the prompt for {{ p.name }}</span>
      <span v-else>{{ why || "Take control to drop files into a shell" }}</span>
    </div>

    <footer class="pane-foot">
      <KeyBar :pane="p" :disabled="!canInput" :why="why || (p.harness || controlling ? '' : 'A plain shell: take control to type')" />
      <PromptBar v-if="p.harness" ref="prompt" :pane="p" :disabled="!canInput" />
    </footer>
  </div>

  <div v-else class="pane-missing">
    <SearchX :size="28" />
    <h2>That pane isn’t in the fleet</h2>
    <p class="hint">It may have closed, or its machine may not have reported in yet.</p>
    <button class="secondary" @click="closePane"><ArrowLeft :size="15" />Back to the overview</button>
  </div>
</template>
