<script setup lang="ts">
// Fleet settings: machines and their users, join links waiting to be used, terminal
// access tokens, this hub, and signing out. "Add a machine" is a view of its own.
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { Check, Copy, KeyRound, Link2, LogOut, Pencil, Plus, Server, Trash2, X } from "@lucide/vue";
import Modal from "./Modal.vue";
import AddMachine from "./AddMachine.vue";
import DeviceIcon from "./DeviceIcon.vue";
import UpdatesPanel from "./UpdatesPanel.vue";
import { api, errorText } from "../lib/api";
import { ago } from "../lib/fleet";
import { app, confirmAsk, machines, notify, now, promptAsk, signOut } from "../lib/store";
import type { Dock, JoinLink } from "../lib/types";

const links = ref<JoinLink[]>([]);
const error = ref("");
const tokenName = ref("");
const token = ref("");
const copied = ref("");
const busy = ref(false);
const fleetName = ref(app.me?.fleet ?? "");
const nameBusy = ref(false);
const nameError = ref("");
const savedName = ref("");
const nameChanged = computed(() => fleetName.value.trim() !== (app.me?.fleet ?? ""));
watch(() => app.me?.fleet, (name, before) => {
  if (fleetName.value.trim() === before) fleetName.value = name ?? "";
});
async function renameFleet() {
  const name = fleetName.value.trim();
  if (!name || !nameChanged.value || nameBusy.value) return;
  nameBusy.value = true;
  nameError.value = savedName.value = "";
  try {
    await api.renameFleet(name);
    fleetName.value = app.me?.fleet ?? name;
    savedName.value = "Fleet name saved.";
  } catch (e) {
    nameError.value = errorText(e);
  } finally {
    nameBusy.value = false;
  }
}
let poll: ReturnType<typeof setInterval> | undefined;

const adding = computed(() => app.settings === "add");
const close = () => (app.settings = null);

const rows = computed(() =>
  [...machines.value]
    .sort((a, b) => a.host.localeCompare(b.host))
    .flatMap((m) => [...m.docks].sort((a, b) => a.user.localeCompare(b.user)).map((d) => ({ host: m.host, d }))),
);
const pending = computed(() => links.value.filter((l) => l.state === "pending"));
const recent = computed(() => links.value.filter((l) => l.state !== "pending").slice(0, 6));

async function loadLinks() {
  try {
    links.value = (await api.links()).links ?? [];
  } catch (e) {
    error.value = errorText(e);
  }
}
onMounted(() => {
  void loadLinks();
  poll = setInterval(() => void loadLinks(), 4000);
});
onBeforeUnmount(() => clearInterval(poll));

function when(t: number | string): number {
  return typeof t === "number" ? (t < 1e12 ? t * 1000 : t) : Date.parse(t);
}
function expiresIn(l: JoinLink): string {
  const ms = when(l.expires) - now.value;
  if (!(ms > 0)) return "expired";
  const h = Math.floor(ms / 3_600_000);
  const m = Math.round((ms % 3_600_000) / 60_000);
  return h ? `expires in ${h} h ${m} min` : `expires in ${m} min`;
}

async function cancelLink(l: JoinLink) {
  const ok = await confirmAsk({
    title: `Cancel the link for ${l.name}?`,
    body: "Nobody can join with it after this.",
    confirm: "Cancel link",
    cancel: "Keep it",
    danger: true,
  });
  if (!ok) return;
  try {
    await api.cancelLink(l.id);
    await loadLinks();
  } catch (e) {
    error.value = errorText(e);
  }
}

async function rename(host: string, d: Dock) {
  const name = await promptAsk({
    eyebrow: `${host} · ${d.user}`,
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

async function remove(host: string, d: Dock) {
  const ok = await confirmAsk({
    eyebrow: `${host} · ${d.user}`,
    title: `Remove ${d.user} on ${host}?`,
    body:
      "This revokes its key and cuts its live channels now. Its agents keep running on that computer; " +
      "to bring it back, join it again with a new link.",
    confirm: "Remove",
    danger: true,
  });
  if (!ok) return;
  try {
    await api.removeDock(d.id);
    notify(`Removed ${d.user} on ${host}.`, "ok");
  } catch (e) {
    error.value = errorText(e);
  }
}

async function makeToken() {
  const n = tokenName.value.trim();
  if (!n || busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    token.value = (await api.createToken(n)).token;
    tokenName.value = "";
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}

async function copy(text: string, what: string) {
  try {
    await navigator.clipboard.writeText(text);
    copied.value = what;
    setTimeout(() => copied.value === what && (copied.value = ""), 1800);
  } catch {
    error.value = "Copying failed: select the text and copy it by hand.";
  }
}

const loginCommand = computed(() => `ember-fleet login ${location.origin}`);
</script>

<template>
  <Modal
    :eyebrow="adding ? 'Expand your workspace' : 'Your workspace'"
    :title="adding ? 'Add a machine' : 'Settings'"
    wide
    @close="close"
  >
    <AddMachine v-if="adding" @done="app.settings = 'fleet'" @back="app.settings = 'fleet'" />

    <template v-else>
      <section class="fleet-section first">
        <form class="fleet-name-form" @submit.prevent="renameFleet">
          <label for="fleet-name">Fleet name</label>
          <p id="fleet-name-help" class="hint">The name at the top of your workspace. Shared with everyone connected to this hub.</p>
          <div class="inline-form">
            <input id="fleet-name" v-model="fleetName" name="fleet-name" maxlength="40" required :disabled="nameBusy" autocomplete="off" aria-describedby="fleet-name-help" :aria-invalid="!!nameError" @input="savedName = ''; nameError = ''" />
            <button class="primary small" :disabled="nameBusy || !fleetName.trim() || !nameChanged">{{ nameBusy ? 'Saving…' : 'Save name' }}</button>
          </div>
          <p v-if="nameError" class="message-error" role="alert">{{ nameError }}</p>
          <p v-else-if="savedName" class="saved-note" role="status"><Check :size="14" />{{ savedName }}</p>
        </form>
      </section>

      <section class="fleet-section">
        <h3><Server :size="16" />Machines</h3>
        <ul v-if="rows.length" class="item-list">
          <li v-for="{ host, d } in rows" :key="d.id" :class="{ offline: !d.online }">
            <span class="mark"><DeviceIcon :os="d.os" :size="18" /><i class="dot" :class="d.online ? 'on' : 'off'" /></span>
            <div class="item-text">
              <span class="line"
                ><b>{{ host }}</b><span class="muted">·</span><span>{{ d.user }}</span
                ><span class="badge">{{ d.role }}</span></span
              >
              <small
                >{{ d.online ? "online" : `offline · last seen ${ago(d.lastSeen, now)}` }}
                · herdr {{ d.herdr || "?" }} · {{ d.os }}/{{ d.arch
                }}<template v-if="d.name && d.name !== host"> · “{{ d.name }}”</template></small
              >
            </div>
            <button class="icon-button" :aria-label="`Rename ${d.user} on ${host}`" title="Rename" @click="rename(host, d)">
              <Pencil :size="15" />
            </button>
            <button
              class="icon-button danger-text"
              :aria-label="`Remove ${d.user} on ${host}`"
              title="Remove: revokes its key and cuts its channels"
              @click="remove(host, d)"
            >
              <Trash2 :size="15" />
            </button>
          </li>
        </ul>
        <p v-else class="hint">No machines yet.</p>
        <button class="secondary" @click="app.settings = 'add'"><Plus :size="15" />Add a machine</button>
      </section>

      <UpdatesPanel />

      <section class="fleet-section">
        <h3><Link2 :size="16" />Join links</h3>
        <ul v-if="pending.length" class="item-list">
          <li v-for="l in pending" :key="l.id">
            <div class="item-text">
              <span class="line"><b>{{ l.name }}</b><span class="badge warn">waiting</span></span>
              <small>{{ expiresIn(l) }}</small>
            </div>
            <button class="icon-button" :aria-label="`Cancel the link for ${l.name}`" title="Cancel link" @click="cancelLink(l)">
              <X :size="16" />
            </button>
          </li>
        </ul>
        <p v-else class="hint">No links waiting to be used.</p>
        <p v-if="recent.length" class="hint recent">
          <template v-for="(l, i) in recent" :key="l.id"
            >{{ i ? " · " : "" }}{{ l.name }}: {{ l.state === "used" ? "joined" : l.state }}</template
          >
        </p>
      </section>

      <section class="fleet-section">
        <h3><KeyRound :size="16" />Terminal access</h3>
        <p class="hint">
          A token lets <code>ember-fleet attach</code> and <code>ember-fleet term</code> reach this fleet from
          another computer, through the hub.
        </p>
        <form class="inline-form" @submit.prevent="makeToken">
          <input v-model="tokenName" name="token-name" placeholder="Name it, e.g. laptop" maxlength="60" aria-label="Token name" />
          <button class="secondary" :disabled="busy || !tokenName.trim()">Create token</button>
        </form>
        <div v-if="token" class="token-panel">
          <p class="secret-warn">
            <KeyRound :size="15" /><span>Shown only this once. Anyone who has it can open your terminals.</span>
          </p>
          <div class="copy-row">
            <code>{{ token }}</code>
            <button class="icon-button" aria-label="Copy the token" title="Copy" @click="copy(token, 'token')">
              <Check v-if="copied === 'token'" :size="16" /><Copy v-else :size="16" />
            </button>
          </div>
          <p class="hint">On the other computer, run this and paste the token when it asks:</p>
          <div class="copy-row">
            <code>{{ loginCommand }}</code>
            <button class="icon-button" aria-label="Copy the command" title="Copy" @click="copy(loginCommand, 'login')">
              <Check v-if="copied === 'login'" :size="16" /><Copy v-else :size="16" />
            </button>
          </div>
        </div>
      </section>

      <section class="fleet-section">
        <h3>About</h3>
        <p class="about-product">Herdr Ember Fleet <span class="badge">{{ app.me?.version }}</span></p>
        <dl class="facts">
          <dt>Hub key</dt>
          <dd><code>{{ app.me?.hub }}</code></dd>
          <dt>Address</dt>
          <dd>{{ loginCommand.split(" ").at(-1) }}</dd>
        </dl>
      </section>

      <p v-if="error" class="message-error" role="alert">{{ error }}</p>

      <footer class="split">
        <button class="secondary danger-text" @click="signOut"><LogOut :size="15" />Sign out</button>
        <button class="primary" @click="close">Done</button>
      </footer>
    </template>
  </Modal>
</template>
