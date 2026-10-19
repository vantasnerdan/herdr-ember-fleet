<script setup lang="ts">
// The live terminal of one pane. Opens watching; control only ever starts from a click
// (PaneView). When herdr restarts under it, the hub says "stale": the view takes the
// fresh snapshot and watches the pane with the same id, or says it is gone.
//
// In control, pasting (or dropping) a file or an image uploads it to the agent's machine
// and types its path at the cursor. Plain text pastes as usual.
import { computed, onBeforeUnmount, onMounted, reactive, ref, shallowRef, watch } from "vue";
import { CircleOff, Unplug } from "@lucide/vue";
import AttachChips from "./AttachChips.vue";
import { dropAttachment, filesOf, newAttachment, quotePath, startUpload, type Attachment } from "../lib/attach";
import { fontsReady, TermView, type TermState, type TermTarget } from "../lib/term";
import { ago, type PaneInfo } from "../lib/fleet";
import { confirmAsk, notify, now, paneIndex, refreshFleet } from "../lib/store";

const props = defineProps<{
  pane: PaneInfo;
  gone: boolean;
  /** Watch at the pane's real size, scaled to fit, instead of at a size that fits. */
  whole?: { cols: number; rows: number } | null;
}>();
const emit = defineEmits<{ state: [s: TermState] }>();

const host = ref<HTMLElement>();
const view = shallowRef<TermView>();
const state = ref<TermState>();
const vanished = ref(false);
let unmounted = false;

const targetOf = (p: PaneInfo): TermTarget => ({
  dock: p.dock.id,
  session: p.session.name,
  epoch: p.session.epoch,
  pane: p.pane.pane_id,
  terminal: p.pane.terminal_id ?? "",
});

const reachable = computed(() => props.pane.online && !props.gone && !vanished.value);

async function openLink(uri: string) {
  let url: URL;
  try {
    url = new URL(uri);
  } catch {
    return;
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    notify("Only web links open from a terminal.", "warn");
    return;
  }
  await confirmAsk({
    eyebrow: "Link from the terminal",
    title: "Open this link?",
    body: "Anything running in that pane can print a link. Open it only if you expected it.",
    link: url.href,
    confirm: "Open link",
  });
}

async function stale() {
  await refreshFleet();
  if (unmounted) return;
  const fresh = paneIndex.value.get(props.pane.key);
  if (fresh?.online) view.value?.retarget(targetOf(fresh));
  else {
    vanished.value = true;
    view.value?.stop("gone");
  }
}

onMounted(async () => {
  await fontsReady();
  if (unmounted || !host.value) return;
  const v = new TermView(host.value, targetOf(props.pane), {
    onState: (s) => {
      state.value = s;
      emit("state", s);
    },
    onLink: (uri) => void openLink(uri),
    onStale: () => void stale(),
  });
  view.value = v;
  if (import.meta.env.DEV) (window as unknown as { emberTerm?: TermView }).emberTerm = v;
  v.watchWhole(props.whole ?? null);
  if (reachable.value) v.connect("observe");
  else v.stop(props.gone ? "gone" : "offline");
});

onBeforeUnmount(() => {
  unmounted = true;
  // Closing the view or switching panes releases control.
  view.value?.dispose();
});

watch(reachable, (ok) => {
  const v = view.value;
  if (!v) return;
  if (ok) v.connect("observe");
  else v.stop(props.gone ? "gone" : "offline");
});

// A restarted herdr shows up in the snapshot before the old stream notices. While
// watching, follow it at once; a control stream ends on its own and comes back watching.
watch(
  () => [props.pane.session.epoch, props.pane.pane.terminal_id] as const,
  ([epoch, terminal], [oldEpoch, oldTerminal]) => {
    const v = view.value;
    if (!v || !reachable.value || (epoch === oldEpoch && terminal === oldTerminal)) return;
    vanished.value = false;
    if (v.mode === "observe") v.retarget(targetOf(props.pane));
  },
);

const chip = computed(() => {
  const s = state.value;
  if (!s || !reachable.value) return "";
  switch (s.phase) {
    case "connecting":
      return s.mode === "control" ? "Taking control…" : "Connecting…";
    case "open":
      return s.cols ? `${s.cols}×${s.rows}${s.renderer === "dom" ? " · dom" : ""}` : "";
    case "stale":
      return "herdr restarted — reloading…";
    case "lost":
      return "Connection lost — reconnecting…";
    case "closed":
    case "error":
      return `${s.reason ? `${s.reason} — ` : ""}reconnecting…`;
    default:
      return "";
  }
});

const offline = computed(() => {
  const p = props.pane;
  if (!p.dock.online)
    return {
      title: `${p.dock.user}@${p.host} is offline`,
      text: `Last seen ${ago(p.dock.lastSeen, now.value)}. This view picks up again when it reconnects.`,
    };
  return {
    title: `herdr is not reachable for ${p.dock.user} on ${p.host}`,
    text: p.session.error ?? "The session is not answering. This view picks up again when it does.",
  };
});

function clickHost() {
  if (state.value?.mode === "control") view.value?.focus();
}

// ---------------------------------------------------------------- files, in control

const uploads = reactive<Attachment[]>([]);
const controlling = () => state.value?.mode === "control" && state.value.phase === "open";

function forget(a: Attachment, after = 0) {
  setTimeout(() => {
    const i = uploads.indexOf(a);
    if (i >= 0) uploads.splice(i, 1);
    dropAttachment(a);
  }, after);
}

/** Uploads one file and types its path where the cursor is, if this view is still in control. */
async function place(a: Attachment, first: boolean) {
  const target = { dock: props.pane.dock.id, session: props.pane.session.name, pane: props.pane.ref.pane };
  if (!(await startUpload(a, target)) || !a.path) return;
  const typed = view.value?.typeInput(`${first ? "" : " "}${quotePath(a.path)}`);
  if (!typed) notify(`${a.name} is on ${props.pane.host} at ${a.path}, but control ended before it could be typed.`, "warn", 12000);
  forget(a, 1500);
}

/** In control: upload files (in order) and type their paths at the cursor. */
async function uploadAndType(files: File[]) {
  if (!controlling() || !files.length) return false;
  const batch = files.map((f) => {
    const a = newAttachment(f);
    uploads.push(a);
    return a;
  });
  let first = true;
  for (const a of batch) {
    await place(a, first);
    first = false;
  }
  return true;
}

function onPaste(e: ClipboardEvent) {
  if (!controlling()) return;
  const files = filesOf(e.clipboardData);
  if (!files.length) return; // text goes to xterm, which pastes it
  e.preventDefault();
  e.stopPropagation();
  void uploadAndType(files);
}

onBeforeUnmount(() => uploads.forEach(dropAttachment));

watch(
  () => (props.whole ? `${props.whole.cols}x${props.whole.rows}` : ""),
  () => view.value?.watchWhole(props.whole ?? null),
);

defineExpose({
  takeControl: (takeover = false) => view.value?.takeControl(takeover),
  release: () => view.value?.release(),
  fit: () => view.value?.fitToWindow(),
  dismiss: () => view.value?.dismissNotices(),
  watchWhole: (size: { cols: number; rows: number } | null) => view.value?.watchWhole(size),
  uploadAndType,
});
</script>

<template>
  <div class="term-wrap">
    <div
      ref="host"
      class="term-host"
      :class="{ control: state?.mode === 'control' && state.phase === 'open' }"
      @click="clickHost"
      @paste.capture="onPaste"
    />
    <AttachChips
      v-if="uploads.length"
      class="term-uploads"
      :items="uploads"
      @retry="(a) => place(a, false)"
      @remove="(a) => forget(a)"
    />
    <div v-if="gone || vanished" class="term-overlay">
      <CircleOff :size="26" />
      <h2>This pane has closed</h2>
      <p>It is no longer in {{ pane.host }}’s herdr{{ vanished ? " after herdr restarted" : "" }}.</p>
    </div>
    <div v-else-if="!pane.online" class="term-overlay">
      <Unplug :size="26" />
      <h2>{{ offline.title }}</h2>
      <p>{{ offline.text }}</p>
    </div>
    <span v-if="chip" class="term-status" aria-live="polite">{{ chip }}</span>
  </div>
</template>
