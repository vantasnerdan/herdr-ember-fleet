<script setup lang="ts">
// The stage when no pane is open: how the fleet is doing, and who is waiting.
import { computed } from "vue";
import { MousePointerClick } from "@lucide/vue";
import HarnessIcon from "./HarnessIcon.vue";
import { blocked, docks, machines, openPane, panes } from "../lib/store";

const agents = computed(() => panes.value.filter((p) => p.harness && p.online));
const count = (s: string) => agents.value.filter((p) => p.status === s).length;
const tiles = computed(() => [
  {
    label: "machines online",
    value: `${machines.value.filter((m) => m.docks.some((d) => d.online)).length}/${machines.value.length}`,
    sub: `${docks.value.filter((d) => d.online).length} of ${docks.value.length} users`,
  },
  { label: "working", value: String(count("working")), cls: "working" },
  { label: "need you", value: String(count("blocked")), cls: "blocked" },
  { label: "idle", value: String(count("idle")) },
  { label: "done", value: String(count("done")), cls: "done" },
]);
</script>

<template>
  <div class="overview">
    <div class="overview-inner">
      <span class="eyebrow">Your fleet</span>
      <h1>{{ agents.length }} agents on {{ machines.length }} machines</h1>
      <div class="tiles">
        <div v-for="t in tiles" :key="t.label" class="tile" :class="t.cls">
          <b>{{ t.value }}</b>
          <span>{{ t.label }}</span>
          <small v-if="t.sub">{{ t.sub }}</small>
        </div>
      </div>

      <template v-if="blocked.length">
        <h2 class="section-title">Waiting on you</h2>
        <div class="wait-cards">
          <button v-for="p in blocked" :key="p.key" class="wait-card" @click="openPane(p.ref)">
            <HarnessIcon :harness="p.harness" />
            <span class="wait-text">
              <span class="where">{{ p.host }} · {{ p.dock.user }}<template v-if="p.workspace"> · {{ p.workspace.label }}</template></span>
              <b>{{ p.name }}</b>
              <span v-if="p.title" class="q">{{ p.title }}</span>
            </span>
            <span class="state-pill blocked beacon"><i />blocked</span>
          </button>
        </div>
      </template>

      <p class="hint pick">
        <MousePointerClick :size="16" />Pick a pane in the tree to watch its screen. Watching is read-only; take
        control to type, and release when you’re done.
      </p>
    </div>
  </div>
</template>
