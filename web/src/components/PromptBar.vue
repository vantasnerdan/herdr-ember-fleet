<script setup lang="ts">
// A prompt for the agent, sent with agent.prompt. Enter sends, Shift+Enter adds a line.
// A blocked agent is waiting on a question on its screen, so sending to it asks twice.
import { nextTick, ref, watch } from "vue";
import { OctagonAlert, SendHorizontal } from "@lucide/vue";
import { act } from "../lib/actions";
import type { PaneInfo } from "../lib/fleet";
import { notify } from "../lib/store";

const props = defineProps<{ pane: PaneInfo; disabled: boolean }>();
const text = ref("");
const sending = ref(false);
const warned = ref(false);
const field = ref<HTMLTextAreaElement>();

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

async function send() {
  const value = text.value;
  if (!value.trim() || sending.value || props.disabled) return;
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
      notify(`Prompt sent to ${props.pane.name} on ${props.pane.host}.`, "ok");
    }
  } finally {
    sending.value = false;
  }
}
</script>

<template>
  <div class="prompt">
    <p v-if="warned" class="prompt-warn" role="alert">
      <OctagonAlert :size="15" />
      <span
        >{{ pane.name }} is blocked: it is waiting for an answer on its screen. Answer there (the keys
        above, or Take control), or press Send again to send this prompt anyway.</span
      >
    </p>
    <div class="prompt-bar">
      <textarea
        ref="field"
        name="prompt"
        v-model="text"
        rows="1"
        :disabled="disabled"
        :placeholder="disabled ? 'Read-only' : `Prompt ${pane.name}…`"
        :aria-label="`Prompt for ${pane.name}`"
        enterkeyhint="send"
        @keydown="keydown"
      />
      <button
        class="primary"
        :disabled="disabled || sending || !text.trim()"
        :title="warned ? 'Send anyway' : 'Send (Enter)'"
        @click="send"
      >
        <SendHorizontal :size="16" />{{ warned ? "Send anyway" : "Send" }}
      </button>
    </div>
    <p class="prompt-hint">Enter sends · Shift+Enter for a new line · sent with herdr’s agent.prompt</p>
  </div>
</template>
