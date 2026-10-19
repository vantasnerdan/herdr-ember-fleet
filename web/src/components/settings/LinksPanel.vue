<script setup lang="ts">
// Settings › Join links: links waiting to be used, and what became of recent ones.
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { Plus, X } from "@lucide/vue";
import { api, errorText } from "../../lib/api";
import { app, confirmAsk, now } from "../../lib/store";
import type { JoinLink } from "../../lib/types";

const links = ref<JoinLink[]>([]);
const error = ref("");
let poll: ReturnType<typeof setInterval> | undefined;

async function load() {
  try {
    links.value = (await api.links()).links ?? [];
  } catch (e) {
    error.value = errorText(e);
  }
}
onMounted(() => {
  void load();
  poll = setInterval(() => void load(), 4000);
});
onBeforeUnmount(() => clearInterval(poll));

const pending = computed(() => links.value.filter((l) => l.state === "pending"));
const recentLinks = computed(() => links.value.filter((l) => l.state !== "pending").slice(0, 8));

const when = (t: number | string) => (typeof t === "number" ? (t < 1e12 ? t * 1000 : t) : Date.parse(t));
function expiresIn(l: JoinLink): string {
  const ms = when(l.expires) - now.value;
  if (!(ms > 0)) return "expired";
  const h = Math.floor(ms / 3_600_000);
  const m = Math.round((ms % 3_600_000) / 60_000);
  return h ? `expires in ${h} h ${m} min` : `expires in ${m} min`;
}

async function cancel(l: JoinLink) {
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
    await load();
  } catch (e) {
    error.value = errorText(e);
  }
}
</script>

<template>
  <section class="settings-panel">
    <p class="hint">
      A join link adds one user of one machine to the fleet. It works once and runs out after a while.
    </p>
    <ul v-if="pending.length" class="item-list">
      <li v-for="l in pending" :key="l.id">
        <div class="item-text">
          <span class="line"><b>{{ l.name }}</b><span class="badge warn">waiting</span></span>
          <small>{{ expiresIn(l) }}</small>
        </div>
        <button class="icon-button" :aria-label="`Cancel the link for ${l.name}`" title="Cancel link" @click="cancel(l)">
          <X :size="16" />
        </button>
      </li>
    </ul>
    <p v-else class="hint">No links waiting to be used.</p>
    <p v-if="recentLinks.length" class="hint recent">
      Lately:
      <template v-for="(l, i) in recentLinks" :key="l.id"
        >{{ i ? " · " : " " }}{{ l.name }} {{ l.state === "used" ? "joined" : l.state }}</template
      >
    </p>
    <button class="secondary" @click="app.adding = true"><Plus :size="15" />Add a machine</button>
    <p v-if="error" class="message-error" role="alert">{{ error }}</p>
  </section>
</template>
