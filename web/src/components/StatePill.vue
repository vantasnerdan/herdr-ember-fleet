<script setup lang="ts">
// An agent's state. Working and blocked are beacons: the light layer breathes or beats around them.
import { computed } from "vue";
import { STATUS_TEXT } from "../lib/fleet";
import type { AgentStatus } from "../lib/types";

const props = defineProps<{ status: AgentStatus; offline?: boolean }>();
const lit = computed(() => !props.offline && (props.status === "working" || props.status === "blocked"));
</script>

<template>
  <span class="state-pill" :class="[offline ? 'offline' : status, { beacon: lit }]"
    ><i />{{ offline ? "offline" : STATUS_TEXT[status] }}</span
  >
</template>
