<script setup lang="ts">
import { computed } from "vue";
import { ArrowUpRight, CheckCheck, ChevronRight, Plus, SquareTerminal } from "@lucide/vue";
import HarnessIcon from "./HarnessIcon.vue";
import DeviceIcon from "./DeviceIcon.vue";
import StatePill from "./StatePill.vue";
import { plural } from "../lib/fleet";
import { app, blocked, machines, openPane, panes } from "../lib/store";

const agents = computed(() => panes.value.filter((p) => p.harness && p.online));
const online = computed(() => machines.value.filter((m) => m.docks.some((d) => d.online)).length);
const count = (state: string) => agents.value.filter((p) => p.status === state).length;
const tiles = computed(() => [
  { label: "Working", value: count("working"), cls: "working", note: "In progress" },
  { label: "Needs you", value: count("blocked"), cls: "blocked", note: "Waiting for input" },
  { label: "Idle", value: count("idle"), cls: "idle", note: "Between tasks" },
  { label: "Done", value: count("done"), cls: "done", note: "Ready to review" },
]);
const working = computed(() => agents.value.filter((p) => p.status === "working"));
const stations = computed(() => machines.value.map((m) => ({
  host: m.host,
  os: m.docks[0]?.os,
  users: m.docks.filter((d) => d.online).length,
  totalUsers: m.docks.length,
  agents: panes.value.filter((p) => p.host === m.host && p.harness && p.online).length,
  first: panes.value.find((p) => p.host === m.host && p.online),
})));
</script>

<template>
  <div class="overview">
    <div class="overview-inner">
      <header class="overview-intro">
        <div>
          <span class="eyebrow"><span class="accent-tick" />Fleet overview</span>
          <h1>Across the fleet<span class="coral">.</span></h1>
          <p>{{ plural(agents.length, "agent") }} connected. {{ online }} of {{ machines.length }} machines online.</p>
        </div>
        <button class="secondary add-station" @click="app.settings = 'add'"><Plus :size="15" />Add a machine</button>
      </header>

      <div class="readouts" aria-label="Connected agent states">
        <div v-for="t in tiles" :key="t.label" class="readout" :class="t.cls">
          <span class="readout-label"><i class="sdot" :class="[t.cls, { beacon: t.value > 0 && (t.cls === 'working' || t.cls === 'blocked') }]" />{{ t.label }}</span>
          <b>{{ String(t.value).padStart(2, '0') }}</b>
          <span class="readout-note">{{ t.note }}</span>
          <span class="readout-track" aria-hidden="true"><i :style="{ width: `${agents.length ? t.value / agents.length * 100 : 0}%` }" /></span>
        </div>
      </div>

      <section class="overview-section attention-section">
        <header class="section-heading">
          <h2>Waiting on you <span class="section-count">{{ blocked.length }}</span></h2>
          <span class="section-meta">Open an agent to respond</span>
        </header>
        <div v-if="blocked.length" class="wait-cards">
          <button v-for="p in blocked" :key="p.key" class="wait-card" @click="openPane(p.ref)">
            <HarnessIcon :harness="p.harness" />
            <span class="wait-text">
              <span class="wait-identity"><b>{{ p.name }}</b><span class="where">{{ p.host }} / {{ p.dock.user }}</span></span>
              <span class="q">{{ p.title || p.cwd || "Waiting for your input" }}</span>
            </span>
            <StatePill status="blocked" />
            <ArrowUpRight class="row-arrow" :size="17" />
          </button>
        </div>
        <div v-else class="all-clear"><CheckCheck :size="22" /><div><b>No agents waiting.</b><p>You’re caught up. Anything that needs your input will appear here.</p></div></div>
      </section>

      <div class="overview-columns">
        <section class="overview-section activity-section">
          <header class="section-heading"><h2>In progress <span class="section-count">{{ working.length }}</span></h2><span class="section-meta">Live state</span></header>
          <div class="console-panel">
            <button v-for="p in working" :key="p.key" class="activity-row" @click="openPane(p.ref)">
              <i class="sdot working beacon" />
              <span class="activity-text"><b>{{ p.name }}</b><span>{{ p.title || p.cwd }}</span><small>{{ p.host }} / {{ p.dock.user }}<template v-if="p.workspace"> / {{ p.workspace.label || p.workspace.workspace_id }}</template></small></span>
              <ChevronRight :size="15" />
            </button>
            <p v-if="!working.length" class="panel-empty">No agents working right now.</p>
          </div>
        </section>
        <section class="overview-section stations-section">
          <header class="section-heading"><h2>Machines <span class="section-count">{{ stations.length }}</span></h2><button class="text-button" @click="app.settings = 'fleet'">Manage <ArrowUpRight :size="12" /></button></header>
          <div class="console-panel">
            <button v-for="m in stations" :key="m.host" class="station-row" :class="{ unavailable: !m.users }" :disabled="!m.first" :title="m.first ? `Open a pane on ${m.host}` : 'No reachable panes on this machine'" @click="m.first && openPane(m.first.ref)">
              <DeviceIcon :os="m.os" :size="18" />
              <span class="station-text"><b>{{ m.host }}</b><small>{{ m.users ? `${m.users}/${m.totalUsers} users connected` : "Offline" }}</small></span>
              <span class="station-count">{{ plural(m.agents, "agent") }}</span>
              <i class="dot" :class="m.users ? 'on' : 'off'" />
            </button>
          </div>
        </section>
      </div>
      <footer class="overview-foot"><SquareTerminal :size="16" /><p>Open any pane to watch. <span>Take control when you’re ready to type.</span></p><span class="foot-signature">herdr / ember</span></footer>
    </div>
  </div>
</template>
