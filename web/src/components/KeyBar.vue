<script setup lang="ts">
// Keys for the pane, sent with pane.send_keys: answering a question or stopping an
// agent without taking control of its terminal.
import { ref } from "vue";
import { act } from "../lib/actions";
import type { PaneInfo } from "../lib/fleet";
import type { HerdrKey } from "../lib/types";

const props = defineProps<{ pane: PaneInfo; disabled: boolean; why?: string }>();

const KEYS: { key: HerdrKey; label: string; title: string }[] = [
  { key: "esc", label: "Esc", title: "Escape" },
  { key: "ctrl+c", label: "Ctrl‑C", title: "Interrupt (Ctrl-C)" },
  { key: "enter", label: "Enter", title: "Enter" },
  { key: "up", label: "↑", title: "Arrow up" },
  { key: "down", label: "↓", title: "Arrow down" },
  { key: "tab", label: "Tab", title: "Tab" },
];
const pending = ref<HerdrKey>();

async function press(key: HerdrKey, label: string) {
  if (pending.value) return;
  pending.value = key;
  try {
    await act(props.pane, "pane.send_keys", { pane_id: props.pane.ref.pane, keys: [key] }, `${label} to ${props.pane.name}`);
  } finally {
    pending.value = undefined;
  }
}
</script>

<template>
  <div class="keybar" role="toolbar" aria-label="Send a key to the pane">
    <button
      v-for="k in KEYS"
      :key="k.key"
      class="key"
      :disabled="disabled || !!pending"
      :title="`Send ${k.title} to the pane`"
      :aria-label="`Send ${k.title}`"
      @click="press(k.key, k.label)"
    >
      {{ k.label }}
    </button>
    <span v-if="why" class="why">{{ why }}</span>
  </div>
</template>
