<script setup lang="ts">
import { computed } from "vue";
import { Server, Settings } from "@lucide/vue";
import { app, blocked, docks, machines, openPane } from "../lib/store";

const online = computed(() => machines.value.filter((m) => m.docks.some((d) => d.online)).length);
const summary = computed(() => {
  const users = docks.value.length;
  const usersOn = docks.value.filter((d) => d.online).length;
  return `${usersOn} of ${users} machine users online`;
});
const liveText = computed(() =>
  app.live === "live" ? "Live" : app.live === "connecting" ? "Connecting…" : "Reconnecting…",
);
</script>

<template>
  <header class="topbar">
    <div class="brand">
      <img class="brandmark" src="/mark.svg" alt="Ember Fleet" />
      <strong>Ember Fleet</strong>
      <span class="sep">/</span>
      <span class="fleet-name" :title="`Hub key ${app.me?.hub ?? ''}`">{{ app.me?.fleet }}</span>
    </div>
    <div class="top-actions">
      <button
        v-if="blocked.length"
        class="needs-pill"
        :title="`${blocked.length} blocked: open the first`"
        @click="openPane(blocked[0].ref)"
      >
        <i class="sdot blocked" />{{ blocked.length }} need{{ blocked.length === 1 ? "s" : "" }} you
      </button>
      <span v-if="machines.length" class="summary-pill" :title="summary"
        ><Server :size="14" />{{ online }} of {{ machines.length }} online</span
      >
      <span class="connection" :class="app.live" role="status" :title="app.live === 'live' ? 'Live updates from the hub' : 'The live channel to the hub dropped; reconnecting'"
        ><i /><span class="label">{{ liveText }}</span></span
      >
      <button class="icon-button" title="Fleet settings" aria-label="Fleet settings" @click="app.settings = 'fleet'">
        <Settings :size="18" />
      </button>
    </div>
  </header>
</template>
