<script setup lang="ts">
// A table of agents: name, what it's doing, where, its state and since when. Sortable by
// any column; a row opens the agent's pane. Narrow screens get one card per agent.
import { computed, ref } from "vue";
import { ArrowDown, ArrowUp } from "@lucide/vue";
import HarnessIcon from "./HarnessIcon.vue";
import StatePill from "./StatePill.vue";
import { ago, harnessOf, lasted, type PaneInfo } from "../lib/fleet";
import { now, openPane } from "../lib/store";

const props = defineProps<{
  agents: PaneInfo[];
  /** On a machine's own page the machine goes without saying (its users, sessions and workspaces don't). */
  hideHost?: boolean;
}>();

type Col = "agent" | "task" | "where" | "state" | "activity";
const COLS: { id: Col; label: string }[] = [
  { id: "state", label: "State" },
  { id: "agent", label: "Agent" },
  { id: "task", label: "Task" },
  { id: "where", label: "Where" },
  { id: "activity", label: "Last activity" },
];
const sortBy = ref<Col>("state");
const desc = ref(false);

const URGENT: Record<string, number> = { blocked: 0, working: 1, done: 2, idle: 3, unknown: 4 };
const rank = (p: PaneInfo) => (!p.online ? 9 : !p.harness ? 6 : (URGENT[p.status] ?? 5));
const task = (p: PaneInfo) => p.title || p.cwd || (p.harness ? "" : "shell");
const whereOf = (p: PaneInfo) =>
  [
    props.hideHost ? "" : p.host,
    props.hideHost || p.dock.user !== p.host ? p.dock.user : "",
    p.session.name !== "default" ? p.session.name : "",
    p.workspace?.label,
  ]
    .filter(Boolean)
    .join(" · ");

const rows = computed(() => {
  const by: Record<Col, (a: PaneInfo, b: PaneInfo) => number> = {
    agent: (a, b) => a.label.localeCompare(b.label),
    task: (a, b) => task(a).localeCompare(task(b)),
    where: (a, b) => whereOf(a).localeCompare(whereOf(b)),
    state: (a, b) => rank(a) - rank(b) || (b.since || 0) - (a.since || 0),
    activity: (a, b) => (b.since || 0) - (a.since || 0),
  };
  const list = [...props.agents].sort((a, b) => by[sortBy.value](a, b) || a.label.localeCompare(b.label));
  return desc.value ? list.reverse() : list;
});

function sort(col: Col) {
  if (sortBy.value === col) desc.value = !desc.value;
  else {
    sortBy.value = col;
    desc.value = false;
  }
}
const ariaSort = (col: Col) => (sortBy.value !== col ? "none" : desc.value ? "descending" : "ascending");

</script>

<template>
  <div class="sort-pick">
    <label
      >Sort by
      <select v-model="sortBy" name="agent-sort">
        <option v-for="c in COLS" :key="c.id" :value="c.id">{{ c.label }}</option>
      </select></label
    >
    <button class="icon-button" :aria-label="desc ? 'Sorted descending; sort ascending' : 'Sorted ascending; sort descending'" @click="desc = !desc">
      <ArrowDown v-if="desc" :size="15" /><ArrowUp v-else :size="15" />
    </button>
  </div>
  <table class="agent-table">
    <thead>
      <tr>
        <th :aria-sort="ariaSort('agent')">
          <button @click="sort('agent')">Agent<template v-if="sortBy === 'agent'"><ArrowUp v-if="!desc" :size="12" /><ArrowDown v-else :size="12" /></template></button>
        </th>
        <th class="col-task" :aria-sort="ariaSort('task')">
          <button @click="sort('task')">Task<template v-if="sortBy === 'task'"><ArrowUp v-if="!desc" :size="12" /><ArrowDown v-else :size="12" /></template></button>
        </th>
        <th :aria-sort="ariaSort('where')">
          <button @click="sort('where')">Where<template v-if="sortBy === 'where'"><ArrowUp v-if="!desc" :size="12" /><ArrowDown v-else :size="12" /></template></button>
        </th>
        <th :aria-sort="ariaSort('state')">
          <button @click="sort('state')">State<template v-if="sortBy === 'state'"><ArrowUp v-if="!desc" :size="12" /><ArrowDown v-else :size="12" /></template></button>
        </th>
        <th :aria-sort="ariaSort('activity')">
          <button
            title="When its state last changed (known once it changes after the hub starts)"
            @click="sort('activity')"
          >
            Last activity<template v-if="sortBy === 'activity'"><ArrowUp v-if="!desc" :size="12" /><ArrowDown v-else :size="12" /></template>
          </button>
        </th>
      </tr>
    </thead>
    <tbody>
      <tr
        v-for="p in rows"
        :key="p.key"
        :class="{ offline: !p.online, shell: !p.harness }"
        @click="openPane(p.ref)"
      >
        <td class="col-agent">
          <HarnessIcon :harness="p.harness" />
          <button class="agent-open" :title="`Open ${p.label} (${p.dock.user}@${p.host})`" @click.stop="openPane(p.ref)">
            <b>{{ p.label }}</b>
            <small v-if="p.pane.label && p.harness">{{ harnessOf(p.harness)?.name }}</small>
          </button>
        </td>
        <td class="col-task" :title="[p.title, p.cwd].filter(Boolean).join('\n')">{{ task(p) }}</td>
        <td class="col-where">{{ whereOf(p) }}</td>
        <td class="col-state">
          <StatePill v-if="p.harness" :status="p.status" :offline="!p.online" />
          <span v-else class="muted">shell</span>
          <small v-if="p.since && p.online && p.harness" class="for">for {{ lasted(p.since, now) }}</small>
        </td>
        <td class="col-activity">{{ p.since ? ago(p.since, now) : "—" }}</td>
      </tr>
    </tbody>
  </table>
</template>
