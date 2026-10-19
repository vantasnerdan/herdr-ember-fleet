<script setup lang="ts">
// The top bar: the fleet's name, the three places (Overview, Agents, Machines), search
// (Ctrl/Cmd-K), who needs you, the live connection, and settings.
import { computed } from "vue";
import { LayoutDashboard, List, Search, Server, Settings } from "@lucide/vue";
import { app, blocked, machines, openPane, page, panes, showAgents, showMachines, showOverview, showSettings } from "../lib/store";

const online = computed(() => machines.value.filter((m) => m.docks.some((d) => d.online)).length);
const running = computed(() => panes.value.filter((p) => p.harness && p.online).length);
const liveText = computed(() => (app.live === "live" ? "Live" : app.live === "connecting" ? "Connecting" : "Reconnecting"));
const mac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
const where = computed(() => {
  const n = page.value.name;
  return n === "machine" ? "machines" : n;
});
</script>

<template>
  <header class="topbar">
    <button class="brand" aria-label="Fleet overview" title="Fleet overview" @click="showOverview">
      <span class="brand-symbol"><img class="brandmark" src="/mark.svg" alt="" /></span>
      <span class="brand-identity">
        <span class="brand-caption">Agent workspace</span>
        <span class="fleet-name">{{ app.me?.fleet || "Fleet" }}</span>
      </span>
    </button>
    <nav class="top-nav" aria-label="Places">
      <button
        :class="{ active: where === 'overview' }"
        :aria-current="where === 'overview' ? 'page' : undefined"
        aria-label="Overview"
        title="Overview"
        @click="showOverview"
      >
        <LayoutDashboard :size="15" /><span class="nav-label">Overview</span>
      </button>
      <button
        :class="{ active: where === 'agents' }"
        :aria-current="where === 'agents' ? 'page' : undefined"
        :aria-label="`Agents, ${running} running`"
        :title="`All agents (${running} running)`"
        @click="showAgents()"
      >
        <List :size="15" /><span class="nav-label">Agents</span><span class="nav-count" aria-hidden="true">{{ running }}</span>
      </button>
      <button
        :class="{ active: where === 'machines' }"
        :aria-current="where === 'machines' ? 'page' : undefined"
        :aria-label="`Machines, ${online} of ${machines.length} online`"
        :title="`Machines (${online} of ${machines.length} online)`"
        @click="showMachines"
      >
        <Server :size="15" /><span class="nav-label">Machines</span
        ><span class="nav-count" aria-hidden="true">{{ online }}/{{ machines.length }}</span>
      </button>
    </nav>
    <div class="top-actions">
      <button
        class="search-button"
        :title="`Find an agent, machine or page (${mac ? '⌘' : 'Ctrl'} K)`"
        aria-label="Search agents, machines and pages"
        :aria-keyshortcuts="mac ? 'Meta+K' : 'Control+K'"
        @click="app.palette = true"
      >
        <Search :size="15" /><span class="nav-label">Search</span><kbd>{{ mac ? "⌘" : "Ctrl" }} K</kbd>
      </button>
      <button v-if="blocked.length" class="needs-pill" :title="`${blocked.length} waiting for your input: open the first`" @click="openPane(blocked[0].ref)">
        <i class="sdot blocked" /><span>{{ blocked.length }} need{{ blocked.length === 1 ? "s" : "" }} you</span>
      </button>
      <span class="connection" :class="app.live" role="status" :title="app.live === 'live' ? 'Receiving live updates from the hub' : 'Connecting to the hub for live updates'">
        <i /><span class="label">{{ liveText }}</span>
      </span>
      <button
        class="icon-button settings-button"
        :class="{ on: where === 'settings' }"
        title="Settings"
        aria-label="Settings"
        :aria-current="where === 'settings' ? 'page' : undefined"
        @click="showSettings()"
      >
        <Settings :size="18" />
      </button>
    </div>
  </header>
</template>
