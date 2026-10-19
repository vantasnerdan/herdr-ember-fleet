<script setup lang="ts">
// The harness an agent runs in, as a small tinted monogram. A plain shell gets a prompt.
import { computed } from "vue";
import { SquareTerminal } from "@lucide/vue";
import { harnessOf } from "../lib/fleet";

const props = defineProps<{ harness?: string }>();
const info = computed(() => harnessOf(props.harness));
</script>

<template>
  <span v-if="info" class="harness" :data-h="harness" :title="info.name" role="img" :aria-label="info.name">{{
    info.mono
  }}</span>
  <span v-else class="harness shell" title="Shell" role="img" aria-label="Shell"><SquareTerminal :size="13" /></span>
</template>
