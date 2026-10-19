<script setup lang="ts">
// Every machine in one table (#/machines): who runs herdr there, which groups it feeds,
// its agents, and whether its herdr and ember-fleet are current. A row opens the machine.
import { computed, ref } from "vue";
import { ArrowDown, ArrowUp, Plus } from "@lucide/vue";
import DeviceIcon from "./DeviceIcon.vue";
import { ago, plural, versionCmp } from "../lib/fleet";
import { colorHex, groupOf, memberKey } from "../lib/groups";
import { app, machines, now, panes, showMachine } from "../lib/store";
import type { Group, Machine } from "../lib/types";

const herdrOf = (d: Machine["docks"][number]) => d.herdrServer || d.herdr;
const newestHerdr = computed(() =>
  machines.value.flatMap((m) => m.docks.map(herdrOf)).filter(Boolean).sort(versionCmp).at(-1),
);

const rows = computed(() =>
  machines.value.map((m) => {
    const on = m.docks.filter((d) => d.online);
    const mine = panes.value.filter((p) => p.host === m.host);
    const agents = mine.filter((p) => p.harness && p.online);
    const groups = new Map<string, Group>();
    for (const d of m.docks) {
      const g = groupOf.value.get(memberKey({ kind: "dock", dock: d.id }));
      if (g) groups.set(g.id, g);
    }
    for (const p of mine) {
      const g = groupOf.value.get(memberKey({ kind: "pane", ...p.ref }));
      if (g) groups.set(g.id, g);
    }
    const herdr = [...new Set(m.docks.map(herdrOf).filter(Boolean))];
    const ember = [...new Set(m.docks.map((d) => d.version).filter(Boolean))] as string[];
    return {
      m,
      online: on.length > 0,
      allOnline: on.length === m.docks.length,
      lastSeen: Math.max(...m.docks.map((d) => d.lastSeen || 0)),
      os: m.docks[0]?.os,
      arch: m.docks[0]?.arch,
      groups: [...groups.values()],
      agents: agents.length,
      working: agents.filter((p) => p.status === "working").length,
      blocked: agents.filter((p) => p.status === "blocked").length,
      shells: mine.filter((p) => !p.harness).length,
      herdr,
      herdrBehind: herdr.some((v) => versionCmp(v, newestHerdr.value) < 0),
      ember,
      emberBehind: ember.some((v) => v !== app.me?.version),
    };
  }),
);
const onlineCount = computed(() => rows.value.filter((r) => r.online).length);

// ---------------------------------------------------------------- sorting
type Col = "machine" | "agents" | "shells" | "herdr" | "ember" | "seen";
const COLS: { id: Col; label: string }[] = [
  { id: "machine", label: "Machine" },
  { id: "agents", label: "Agents" },
  { id: "shells", label: "Shells" },
  { id: "herdr", label: "herdr" },
  { id: "ember", label: "ember-fleet" },
  { id: "seen", label: "Last seen" },
];
const sortBy = ref<Col>("machine");
const desc = ref(false);
type Row = (typeof rows.value)[number];
const by: Record<Col, (a: Row, b: Row) => number> = {
  machine: (a, b) => a.m.host.localeCompare(b.m.host),
  agents: (a, b) => b.blocked - a.blocked || b.agents - a.agents,
  shells: (a, b) => b.shells - a.shells,
  herdr: (a, b) => versionCmp(a.herdr[0], b.herdr[0]),
  ember: (a, b) => versionCmp(a.ember[0], b.ember[0]),
  seen: (a, b) => Number(b.online) - Number(a.online) || b.lastSeen - a.lastSeen,
};
const sorted = computed(() => {
  const list = [...rows.value].sort((a, b) => by[sortBy.value](a, b) || a.m.host.localeCompare(b.m.host));
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
  <div class="page machines-page">
    <div class="page-inner wide">
      <header class="page-head with-action">
        <div>
          <span class="eyebrow"><span class="accent-tick" />Fleet</span>
          <h1>Machines</h1>
          <p class="hint">{{ onlineCount }} of {{ plural(rows.length, "machine") }} online. Click one for its details.</p>
        </div>
        <button class="secondary" @click="app.adding = true"><Plus :size="15" />Add a machine</button>
      </header>
      <div v-if="rows.length" class="sort-pick">
        <label
          >Sort by
          <select v-model="sortBy" name="machine-sort">
            <option v-for="c in COLS" :key="c.id" :value="c.id">{{ c.label }}</option>
          </select></label
        >
        <button class="icon-button" :aria-label="desc ? 'Sorted descending; sort ascending' : 'Sorted ascending; sort descending'" @click="desc = !desc">
          <ArrowDown v-if="desc" :size="15" /><ArrowUp v-else :size="15" />
        </button>
      </div>
      <table v-if="rows.length" class="machine-table">
        <thead>
          <tr>
            <th
              v-for="c in COLS.slice(0, 1)"
              :key="c.id"
              :aria-sort="ariaSort(c.id)"
            ><button @click="sort(c.id)">{{ c.label }}<template v-if="sortBy === c.id"><ArrowUp v-if="!desc" :size="12" /><ArrowDown v-else :size="12" /></template></button></th>
            <th>Users</th>
            <th class="col-groups">Groups</th>
            <th
              v-for="c in COLS.slice(1)"
              :key="c.id"
              :class="{ 'col-num': c.id === 'shells' }"
              :aria-sort="ariaSort(c.id)"
            ><button @click="sort(c.id)">{{ c.label }}<template v-if="sortBy === c.id"><ArrowUp v-if="!desc" :size="12" /><ArrowDown v-else :size="12" /></template></button></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in sorted" :key="r.m.host" :class="{ offline: !r.online }" @click="showMachine(r.m.host)">
            <td class="col-machine">
              <span class="mark"><DeviceIcon :os="r.os" :size="18" /><i class="dot" :class="r.online ? 'on' : 'off'" /></span>
              <button class="agent-open" :title="`Open ${r.m.host}`" @click.stop="showMachine(r.m.host)">
                <b>{{ r.m.host }}</b><small>{{ r.os }}/{{ r.arch }}</small>
              </button>
            </td>
            <td data-label="Users">
              <span v-for="d in r.m.docks" :key="d.id" class="user-chip" :class="{ off: !d.online }"
                >{{ d.user }}<span v-if="d.role !== 'owner'" class="badge">{{ d.role }}</span></span
              >
            </td>
            <td class="col-groups" data-label="Groups">
              <span
                v-for="g in r.groups"
                :key="g.id"
                class="group-tag"
                :style="colorHex(g.color) ? { '--group': colorHex(g.color) } : undefined"
                >{{ g.name }}</span
              >
              <span v-if="!r.groups.length" class="muted">—</span>
            </td>
            <td data-label="Agents">
              <template v-if="r.agents">
                <b>{{ r.agents }}</b>
                <small v-if="r.blocked" class="count blocked"><i class="sdot blocked" />{{ r.blocked }}</small>
                <small v-if="r.working" class="count working"><i class="sdot working" />{{ r.working }}</small>
              </template>
              <span v-else class="muted">none</span>
            </td>
            <td class="col-num" data-label="Shells">{{ r.shells || "—" }}</td>
            <td data-label="herdr">
              <span :class="{ behind: r.herdrBehind }">{{ r.herdr.join(", ") || "?" }}</span>
              <span v-if="r.herdrBehind" class="badge warn" :title="`Newest in the fleet: ${newestHerdr}`">behind</span>
            </td>
            <td data-label="ember-fleet">
              <span :class="{ behind: r.emberBehind }">{{ r.ember.join(", ") || "?" }}</span>
              <span v-if="r.emberBehind" class="badge warn" :title="`This hub hands out ${app.me?.version}`">behind</span>
            </td>
            <td data-label="Last seen">{{ r.online ? (r.allOnline ? "online" : "partly online") : ago(r.lastSeen, now) }}</td>
          </tr>
        </tbody>
      </table>
      <p v-else class="panel-empty">No machines yet. Add one to see its agents here.</p>
    </div>
  </div>
</template>
