<script setup lang="ts">
// Every agent in one list (#/agents[/state]): filter by state or by words, sort by any
// column, open one. The overview's counters land here.
import { computed, ref } from "vue";
import { Search, SquareTerminal, X } from "@lucide/vue";
import AgentTable from "./AgentTable.vue";
import { plural } from "../lib/fleet";
import { groupOf, memberKey } from "../lib/groups";
import { page, panes, showAgents, showShells, type AgentFilter } from "../lib/store";

const words = ref("");
const filter = computed<AgentFilter>(() => (page.value.name === "agents" ? page.value.status : "all"));

const live = computed(() => panes.value.filter((p) => p.harness && p.online));
const count = (s: AgentFilter) => (s === "all" ? live.value.length : live.value.filter((p) => p.status === s).length);
const CHIPS: { id: AgentFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "blocked", label: "Needs you" },
  { id: "working", label: "Working" },
  { id: "idle", label: "Idle" },
  { id: "done", label: "Done" },
];

const groupName = (key: string) => groupOf.value.get(key)?.name ?? "";
const shown = computed(() => {
  const terms = words.value.toLowerCase().split(/\s+/).filter(Boolean);
  return panes.value.filter((p) => {
    if (!p.harness && !showShells.value) return false;
    if (filter.value !== "all" && (!p.online || p.status !== filter.value)) return false;
    if (!terms.length) return true;
    const group = groupName(memberKey({ kind: "pane", ...p.ref })) || groupName(memberKey({ kind: "dock", dock: p.dock.id }));
    const hay = [p.label, p.harness, p.title, p.cwd, p.host, p.dock.user, p.workspace?.label, p.status, group]
      .join(" ")
      .toLowerCase();
    return terms.every((t) => hay.includes(t));
  });
});
const shellCount = computed(() => panes.value.filter((p) => !p.harness).length);
</script>

<template>
  <div class="page agents-page">
    <div class="page-inner wide">
      <header class="page-head">
        <span class="eyebrow"><span class="accent-tick" />Fleet</span>
        <h1>Agents</h1>
        <p class="hint">{{ plural(live.length, "agent") }} running. Click one to open it.</p>
      </header>
      <div class="list-tools">
        <div class="chips" role="group" aria-label="Show agents that are">
          <button
            v-for="c in CHIPS"
            :key="c.id"
            class="chip"
            :class="[c.id, { on: filter === c.id }]"
            :aria-pressed="filter === c.id"
            @click="showAgents(c.id)"
          >
            <i v-if="c.id !== 'all'" class="sdot" :class="c.id" />{{ c.label }} <b>{{ count(c.id) }}</b>
          </button>
        </div>
        <div class="tree-filter list-filter">
          <Search :size="14" />
          <input
            v-model="words"
            type="search"
            name="agent-filter"
            placeholder="Filter by name, task, machine, group"
            aria-label="Filter agents"
            spellcheck="false"
            autocomplete="off"
            @keydown.esc="words = ''"
          />
          <button v-if="words" class="icon-button" aria-label="Clear filter" @click="words = ''"><X :size="14" /></button>
        </div>
        <button v-if="shellCount" class="shell-toggle" :aria-pressed="showShells" @click="showShells = !showShells">
          <SquareTerminal :size="14" />{{ showShells ? "Shells shown" : `Show ${plural(shellCount, "shell")}` }}
        </button>
      </div>
      <AgentTable v-if="shown.length" :agents="shown" />
      <p v-else class="panel-empty">
        <template v-if="words">Nothing matches “{{ words }}”.</template>
        <template v-else-if="filter !== 'all'">No agent is {{ filter === "blocked" ? "waiting on you" : filter }} right now.</template>
        <template v-else>No agents are running yet.</template>
      </p>
    </div>
  </div>
</template>
