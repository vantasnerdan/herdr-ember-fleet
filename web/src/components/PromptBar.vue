<script setup lang="ts">
// A prompt for the agent, sent with agent.prompt. Enter sends, Shift+Enter adds a line.
// A blocked agent is waiting on a question on its screen, so sending to it asks twice.
//
// Files (pasted, dropped here or on the pane, or picked with the paperclip) upload to the
// agent's machine straight away; on send, their paths follow the text, one per line, which
// is how Claude Code, Codex and omp pick up images.
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from "vue";
import { OctagonAlert, Paperclip, SendHorizontal } from "@lucide/vue";
import AttachChips from "./AttachChips.vue";
import { act } from "../lib/actions";
import { draggingFiles, dropAttachment, filesOf, newAttachment, startUpload, type Attachment } from "../lib/attach";
import type { PaneInfo } from "../lib/fleet";
import { notify } from "../lib/store";

const props = defineProps<{ pane: PaneInfo; disabled: boolean }>();
const text = ref("");
const sending = ref(false);
const warned = ref(false);
const field = ref<HTMLTextAreaElement>();
const picker = ref<HTMLInputElement>();
const attachments = reactive<Attachment[]>([]);
const over = ref(false);

const uploading = computed(() => attachments.some((a) => a.state === "uploading"));
const failed = computed(() => attachments.some((a) => a.state === "failed"));
const ready = computed(() => attachments.filter((a) => a.state === "done" && a.path));
const canSend = computed(
  () => !props.disabled && !sending.value && !uploading.value && !failed.value && (!!text.value.trim() || ready.value.length > 0),
);
const sendTitle = computed(() =>
  uploading.value
    ? "Waiting for the files to upload"
    : failed.value
      ? "A file didn’t upload: retry or remove it"
      : warned.value
        ? "Send anyway"
        : "Send (Enter)",
);

watch(
  () => props.pane.status,
  (s) => {
    if (s !== "blocked") warned.value = false;
  },
);

function grow() {
  const el = field.value;
  if (!el) return;
  el.style.height = "auto";
  el.style.height = `${Math.min(el.scrollHeight + 2, 180)}px`;
}
watch(text, () => void nextTick(grow));

function keydown(e: KeyboardEvent) {
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
    e.preventDefault();
    void send();
  }
}

// ---------------------------------------------------------------- files

const target = () => ({ dock: props.pane.dock.id, session: props.pane.session.name, pane: props.pane.ref.pane });

/** Attach files: each starts uploading at once. */
function addFiles(files: File[]) {
  if (props.disabled || !files.length) return;
  for (const file of files) {
    const a = newAttachment(file);
    attachments.push(a);
    void startUpload(a, target());
  }
}

function retry(a: Attachment) {
  void startUpload(a, target());
}

function remove(a: Attachment) {
  dropAttachment(a);
  const i = attachments.indexOf(a);
  if (i >= 0) attachments.splice(i, 1);
}

function onPaste(e: ClipboardEvent) {
  const files = filesOf(e.clipboardData);
  if (!files.length) return; // plain text pastes as usual
  e.preventDefault();
  addFiles(files);
}

function onPick() {
  addFiles([...(picker.value?.files ?? [])]);
  if (picker.value) picker.value.value = "";
}

function onDragOver(e: DragEvent) {
  if (props.disabled || !draggingFiles(e.dataTransfer)) return;
  e.preventDefault();
  if (e.dataTransfer) e.dataTransfer.dropEffect = "copy";
  over.value = true;
}

function onDrop(e: DragEvent) {
  over.value = false;
  if (props.disabled || !draggingFiles(e.dataTransfer)) return;
  // handled here, so the pane around it (which types paths in control mode) leaves it be
  e.preventDefault();
  addFiles(filesOf(e.dataTransfer));
}

onBeforeUnmount(() => attachments.forEach(dropAttachment));

// ---------------------------------------------------------------- sending

async function send() {
  if (!canSend.value) return;
  const paths = ready.value.map((a) => a.path as string);
  const body = text.value.trim() ? text.value : "";
  const value = paths.length ? (body ? `${body}\n\n${paths.join("\n")}` : paths.join("\n")) : body;
  if (props.pane.status === "blocked" && !warned.value) {
    warned.value = true;
    return;
  }
  sending.value = true;
  try {
    const r = await act(props.pane, "agent.prompt", { pane_id: props.pane.ref.pane, text: value }, "Prompt");
    // Ask again next time. A refusal (agent_blocked included) is explained by act().
    warned.value = false;
    if (r.outcome === "ok") {
      text.value = "";
      for (const a of attachments.splice(0)) dropAttachment(a);
      notify(`Prompt sent to ${props.pane.name} on ${props.pane.host}.`, "ok");
    }
  } finally {
    sending.value = false;
  }
}

defineExpose({ addFiles });
</script>

<template>
  <div class="prompt" :class="{ 'drop-files': over }" @dragover="onDragOver" @dragleave="over = false" @drop="onDrop">
    <p v-if="warned" class="prompt-warn" role="alert">
      <OctagonAlert :size="15" />
      <span
        >{{ pane.name }} is blocked: it is waiting for an answer on its screen. Answer there (the keys
        above, or Take control), or press Send again to send this prompt anyway.</span
      >
    </p>
    <AttachChips v-if="attachments.length" :items="attachments" @remove="remove" @retry="retry" />
    <div class="prompt-bar">
      <button
        class="icon-button attach-button"
        type="button"
        :disabled="disabled"
        :title="disabled ? 'Read-only' : 'Attach files (or paste or drop them)'"
        aria-label="Attach files"
        @click="picker?.click()"
      >
        <Paperclip :size="17" />
      </button>
      <input ref="picker" class="attach-input" type="file" multiple tabindex="-1" aria-hidden="true" @change="onPick" />
      <textarea
        ref="field"
        name="prompt"
        v-model="text"
        rows="1"
        :disabled="disabled"
        :placeholder="disabled ? 'Read-only' : `Prompt ${pane.label} (sends without taking control)`"
        :aria-label="`Prompt for ${pane.name}`"
        enterkeyhint="send"
        @keydown="keydown"
        @paste="onPaste"
      />
      <button class="primary" :disabled="!canSend" :title="sendTitle" @click="send">
        <SendHorizontal :size="16" />{{ warned ? "Send anyway" : "Send" }}
      </button>
    </div>
    <p class="prompt-hint">
      Enter to send · Shift+Enter for a new line · Paste or drop files
    </p>
  </div>
</template>
