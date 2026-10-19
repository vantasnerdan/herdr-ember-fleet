<script setup lang="ts">
// One machine (#/machines/<host>): each OS user that runs herdr there, their versions and
// last update, the agents running there, and (in the danger zone) removing it.
import { computed, onMounted, ref } from "vue";
import { ArrowLeft, Pencil, Trash2, UserRound } from "@lucide/vue";
import AgentTable from "./AgentTable.vue";
import DeviceIcon from "./DeviceIcon.vue";
import { api, errorText } from "../lib/api";
import { ago, plural } from "../lib/fleet";
import { app, confirmAsk, machines, notify, now, page, panes, promptAsk, showMachines, showShells } from "../lib/store";
import type { Dock, UpdateStatus } from "../lib/types";

const host = computed(() => (page.value.name === "machine" ? page.value.host : ""));
const machine = computed(() => machines.value.find((m) => m.host === host.value));
const online = computed(() => machine.value?.docks.some((d) => d.online) ?? false);
const mine = computed(() => panes.value.filter((p) => p.host === host.value));
const agents = computed(() => mine.value.filter((p) => p.harness));
const shells = computed(() => mine.value.filter((p) => !p.harness));
const listed = computed(() => (showShells.value ? mine.value : agents.value));
const updates = ref<UpdateStatus | null>(null);
const error = ref("");

onMounted(async () => {
  try {
    updates.value = await api.updates();
  } catch {
    // versions still show from the fleet itself
  }
});

const herdrDown = computed(() => machine.value?.docks.some((d) => d.online && d.sessions.some((x) => x.error)) ?? false);
const resultOf = (d: Dock) => updates.value?.docks.find((u) => u.id === d.id)?.result;
const status = computed(() => {
  const m = machine.value;
  if (!m) return "";
  const d = m.docks[0];
  const where = d ? `${d.os}/${d.arch}` : "";
  if (online.value) return `online · ${where}`;
  return `offline · last seen ${ago(Math.max(...m.docks.map((x) => x.lastSeen || 0)), now.value)} · ${where}`;
});

async function rename(d: Dock) {
  const name = await promptAsk({
    eyebrow: `${host.value} · ${d.user}`,
    title: "Rename this machine user",
    confirm: "Rename",
    input: { label: "Display name", value: d.name, maxlength: 60 },
  });
  if (!name || name === d.name) return;
  try {
    await api.renameDock(d.id, name);
    notify(`Renamed to ${name}.`, "ok");
  } catch (e) {
    error.value = errorText(e);
  }
}

async function remove(d: Dock) {
  const ok = await confirmAsk({
    eyebrow: `${host.value} · ${d.user}`,
    title: `Remove ${d.user} on ${host.value}?`,
    body:
      "This revokes its key and cuts its live channels now. Its agents keep running on that computer; " +
      "to bring it back, join it again with a new link.",
    confirm: "Remove",
    danger: true,
  });
  if (!ok) return;
  // Decided before the request: the live feed may drop the machine before it answers.
  const last = (machine.value?.docks.length ?? 0) <= 1;
  const where = host.value;
  try {
    await api.removeDock(d.id);
    notify(`Removed ${d.user} on ${where}.`, "ok");
    if (last || !machine.value) showMachines();
  } catch (e) {
    error.value = errorText(e);
  }
}
</script>

<template>
  <div class="page machine-page">
    <div class="page-inner wide">
      <button class="text-button back-link" @click="showMachines"><ArrowLeft :size="14" />Machines</button>
      <template v-if="machine">
        <header class="page-head machine-head">
          <span class="mark big"><DeviceIcon :os="machine.docks[0]?.os" :size="26" /><i class="dot" :class="online ? 'on' : 'off'" /></span>
          <div>
            <h1>{{ machine.host }}</h1>
            <p class="hint">{{ status }}</p>
          </div>
        </header>

        <section class="page-section">
          <h2>Users running herdr <span class="section-count">{{ machine.docks.length }}</span></h2>
          <div class="dock-cards">
            <article v-for="d in machine.docks" :key="d.id" class="dock-card" :class="{ offline: !d.online }">
              <header>
                <span class="mark"><UserRound :size="17" /><i class="dot" :class="d.online ? 'on' : 'off'" /></span>
                <b>{{ d.user }}</b>
                <span v-if="d.role !== 'owner'" class="badge">{{ d.role }}</span>
                <span v-if="d.name && d.name !== machine.host" class="muted">“{{ d.name }}”</span>
                <button class="icon-button" :aria-label="`Rename ${d.user}`" title="Rename" @click="rename(d)"><Pencil :size="14" /></button>
              </header>
              <dl class="facts">
                <dt>State</dt>
                <dd>{{ d.online ? "online" : `offline · last seen ${ago(d.lastSeen, now)}` }}</dd>
                <dt>herdr</dt>
                <dd>
                  {{ d.herdrServer || d.herdr || "?" }}
                  <span v-if="d.herdrServer && d.herdr && d.herdr !== d.herdrServer" class="muted"> ({{ d.herdr }} installed, used after a restart)</span>
                </dd>
                <dt>ember-fleet</dt>
                <dd>
                  {{ d.version || "?" }}
                  <span v-if="d.version && d.version !== app.me?.version" class="badge warn">behind {{ app.me?.version }}</span>
                </dd>
                <template v-if="resultOf(d)">
                  <dt>Last update</dt>
                  <dd :class="{ 'danger-text': resultOf(d)?.error }">
                    {{ ago(resultOf(d)!.at, now) }}<template v-if="resultOf(d)?.error">: {{ resultOf(d)?.error }}</template>
                    <details v-if="resultOf(d)?.error && resultOf(d)?.output" class="update-output">
                      <summary>herdr’s output</summary>
                      <pre>{{ resultOf(d)?.output }}</pre>
                    </details>
                  </dd>
                </template>
                <template v-for="s in d.sessions.filter((x) => x.error)" :key="s.name">
                  <dt>{{ s.name === "default" ? "Problem" : `Session ${s.name}` }}</dt>
                  <dd class="danger-text">{{ s.error }}</dd>
                </template>
              </dl>
            </article>
          </div>
        </section>

        <section class="page-section">
          <h2>
            Agents <span class="section-count">{{ agents.length }}</span>
            <button v-if="shells.length" class="shell-toggle" :aria-pressed="showShells" @click="showShells = !showShells">
              {{ showShells ? "Hide shells" : `Show ${plural(shells.length, "shell")}` }}
            </button>
          </h2>
          <AgentTable v-if="listed.length" :agents="listed" hide-host />
          <div v-else class="empty-agents">
            <b>No agents on {{ machine.host }} yet.</b>
            <p class="hint">
              <template v-if="online && herdrDown">
                herdr isn’t answering there (see above). Once it runs, its agents show up here by themselves.
              </template>
              <template v-else-if="online">
                Start one in herdr there (as {{ machine.docks.map((d) => d.user).join(" or ") }}) and it shows up here
                by itself.
              </template>
              <template v-else>It’s offline. Its agents show up here when it’s back.</template>
            </p>
          </div>
        </section>

        <section class="page-section danger-zone">
          <h2>Danger zone</h2>
          <div v-for="d in machine.docks" :key="d.id" class="danger-row">
            <div>
              <b><Trash2 :size="15" />Remove {{ d.user }} on {{ machine.host }}</b>
              <p class="hint">Revokes its key. Its agents keep running there, just outside the fleet.</p>
            </div>
            <button class="secondary danger-text" @click="remove(d)">Remove</button>
          </div>
        </section>
        <p v-if="error" class="message-error" role="alert">{{ error }}</p>
      </template>
      <div v-else class="panel-empty">
        <b>No machine called “{{ host }}”.</b>
        <p class="hint">It may have been removed or renamed.</p>
      </div>
    </div>
  </div>
</template>
