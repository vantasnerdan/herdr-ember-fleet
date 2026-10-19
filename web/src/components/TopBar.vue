<script setup lang="ts">
import { computed } from "vue";
import { LayoutDashboard, Server, Settings } from "@lucide/vue";
import { app, blocked, closePane, docks, machines, openPane, selectedKey } from "../lib/store";

const online = computed(() => machines.value.filter((m) => m.docks.some((d) => d.online)).length);
const summary = computed(() => `${docks.value.filter((d) => d.online).length} of ${docks.value.length} machine users connected`);
const liveText = computed(() => app.live === "live" ? "Live" : app.live === "connecting" ? "Connecting" : "Reconnecting");
</script>

<template>
  <header class="topbar">
    <button class="brand" aria-label="Fleet overview" title="Fleet overview" @click="closePane">
      <span class="brand-symbol"><img class="brandmark" src="/mark.svg" alt="" /></span>
      <span class="brand-identity">
        <span class="brand-caption">Agent workspace</span>
        <span class="fleet-name">{{ app.me?.fleet || "Fleet" }}</span>
      </span>
    </button>
    <div class="top-actions">
      <button class="overview-button" :class="{ active: !selectedKey }" :aria-current="!selectedKey ? 'page' : undefined" @click="closePane">
        <LayoutDashboard :size="15" /><span>Overview</span>
      </button>
      <span v-if="machines.length" class="summary-pill" :title="summary"><Server :size="14" /><b>{{ online }}/{{ machines.length }}</b> machines</span>
      <button v-if="blocked.length" class="needs-pill" :title="`${blocked.length} waiting for input: open the first`" @click="openPane(blocked[0].ref)">
        <i class="sdot blocked" /><span>{{ blocked.length }} need{{ blocked.length === 1 ? "s" : "" }} you</span>
      </button>
      <span class="connection" :class="app.live" role="status" :title="app.live === 'live' ? 'Receiving live updates from the hub' : 'Connecting to the hub for live updates'">
        <i /><span class="label">{{ liveText }}</span>
      </span>
      <button class="icon-button settings-button" title="Settings" aria-label="Fleet settings" @click="app.settings = 'fleet'"><Settings :size="18" /></button>
    </div>
  </header>
</template>
