<script setup lang="ts">
import { CircleAlert, CircleCheck, Info, TriangleAlert, X } from "@lucide/vue";
import { dismiss, toasts } from "../lib/store";

const icons = { info: Info, ok: CircleCheck, warn: TriangleAlert, error: CircleAlert };
</script>

<template>
  <div class="toasts" aria-live="polite">
    <div v-for="t in toasts" :key="t.id" class="toast" :class="t.tone" :role="t.tone === 'error' ? 'alert' : 'status'">
      <component :is="icons[t.tone]" :size="16" />
      <span>{{ t.text }}</span>
      <button class="icon-button" aria-label="Dismiss" @click="dismiss(t.id)"><X :size="14" /></button>
    </div>
  </div>
</template>
