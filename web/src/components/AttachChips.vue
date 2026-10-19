<script setup lang="ts">
// Files on their way to (or already on) the agent's machine: a thumbnail or an icon, the
// name and size, progress, and remove; a failed one says why and offers a retry.
import { FileText, RotateCw, X } from "@lucide/vue";
import { formatBytes, type Attachment } from "../lib/attach";

defineProps<{ items: Attachment[] }>();
const emit = defineEmits<{ remove: [a: Attachment]; retry: [a: Attachment] }>();

function status(a: Attachment) {
  if (a.state === "failed") return a.error ?? "failed";
  if (a.state === "done") return `Uploaded · ${formatBytes(a.size)}`;
  return a.progress >= 1 ? "saving…" : `${formatBytes(a.size)} · ${Math.round(a.progress * 100)}%`;
}
</script>

<template>
  <ul class="attach-chips" aria-label="Attached files">
    <li
      v-for="a in items"
      :key="a.id"
      class="attach-chip"
      :class="a.state"
      :title="a.path ? `${a.name}\n${a.path}` : a.name"
    >
      <img v-if="a.preview" class="thumb" :src="a.preview" alt="" />
      <span v-else class="thumb file"><FileText :size="16" /></span>
      <span class="meta">
        <span class="name">{{ a.name }}</span>
        <span class="status" :role="a.state === 'failed' ? 'alert' : undefined">{{ status(a) }}</span>
      </span>
      <span v-if="a.state === 'uploading'" class="bar" aria-hidden="true"
        ><i :style="{ width: `${Math.round(a.progress * 100)}%` }"
      /></span>
      <button
        v-if="a.state === 'failed'"
        class="icon-button"
        :aria-label="`Retry ${a.name}`"
        title="Try again"
        @click="emit('retry', a)"
      >
        <RotateCw :size="14" />
      </button>
      <button
        class="icon-button"
        :aria-label="a.state === 'uploading' ? `Cancel ${a.name}` : `Remove ${a.name}`"
        :title="a.state === 'uploading' ? 'Cancel' : 'Remove'"
        @click="emit('remove', a)"
      >
        <X :size="14" />
      </button>
    </li>
  </ul>
</template>
