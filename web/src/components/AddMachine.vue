<script setup lang="ts">
// Add a machine: name it, then run one command on it. The link inside is single use and
// expires in 24 hours; until it is used it is a secret.
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { ArrowLeft, Check, CircleCheck, Copy, KeyRound, LoaderCircle, Plus, X } from "@lucide/vue";
import { api, errorText } from "../lib/api";
import { confirmAsk, notify } from "../lib/store";
import type { LinkState, NewLink } from "../lib/types";

defineProps<{ inline?: boolean }>();
const emit = defineEmits<{ done: []; back: [] }>();

const name = ref("");
const busy = ref(false);
const error = ref("");
const made = ref<NewLink & { name: string }>();
const state = ref<LinkState>("pending");
const copied = ref("");
const field = ref<HTMLInputElement>();
let poll: ReturnType<typeof setInterval> | undefined;


function when(t: number | string): Date {
  if (typeof t === "number") return new Date(t < 1e12 ? t * 1000 : t);
  return new Date(t);
}
const expiresAt = computed(() => {
  if (!made.value) return "";
  const d = when(made.value.expires);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit" });
});

async function create() {
  const n = name.value.trim();
  if (!n || busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    made.value = { ...(await api.createLink(n)), name: n };
    state.value = "pending";
    poll = setInterval(() => void check(), 3000);
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}

async function check() {
  if (!made.value) return;
  try {
    const { links } = await api.links();
    const l = links.find((x) => x.id === made.value?.id);
    if (l) state.value = l.state;
    if (state.value !== "pending") clearInterval(poll);
    if (state.value === "used") notify(`${made.value.name} joined the fleet.`, "ok");
  } catch {
    // keep waiting
  }
}

async function cancel() {
  if (!made.value) return;
  const ok = await confirmAsk({
    title: "Cancel this link?",
    body: "Nobody can join with it after this. You can make a new one any time.",
    confirm: "Cancel link",
    cancel: "Keep it",
    danger: true,
  });
  if (!ok) return;
  try {
    await api.cancelLink(made.value.id);
    state.value = "cancelled";
    clearInterval(poll);
  } catch (e) {
    error.value = errorText(e);
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

function again() {
  clearInterval(poll);
  made.value = undefined;
  name.value = "";
}

onMounted(() => field.value?.focus({ preventScroll: true }));
onBeforeUnmount(() => clearInterval(poll));
</script>

<template>
  <div class="add-machine">
    <form v-if="!made" @submit.prevent="create">
      <label
        >Name for the machine<input
          ref="field"
          v-model="name"
          name="machine-name"
          placeholder="e.g. studio"
          maxlength="60"
          spellcheck="false"
          autocomplete="off"
      /></label>
      <p class="hint">
        Each OS user that runs herdr joins with its own link: one link for <code>agent-one</code>, another for
        <code>agent-two</code>, even on the same computer.
      </p>
      <p v-if="error" class="message-error" role="alert">{{ error }}</p>
      <footer>
        <button v-if="!inline" type="button" class="secondary" @click="emit('back')">
          <ArrowLeft :size="15" />Back
        </button>
        <button class="primary" :disabled="busy || !name.trim()"><Plus :size="15" />Create join link</button>
      </footer>
    </form>

    <section v-else class="join-panel" aria-live="polite">
      <h3>Run this on {{ made.name }}</h3>
      <p class="hint">
        In a terminal on that computer, as the user whose herdr you want in the fleet. It dials out to the
        relay: no SSH, no open ports.
      </p>
      <div class="copy-row">
        <code>{{ made.command }}</code>
        <button class="icon-button" aria-label="Copy the command" title="Copy" @click="copy(made.command, 'command')">
          <Check v-if="copied === 'command'" :size="16" /><Copy v-else :size="16" />
        </button>
      </div>
      <p class="hint">The join link on its own, if ember-fleet asks for it:</p>
      <div class="copy-row">
        <code>{{ made.link }}</code>
        <button class="icon-button" aria-label="Copy the link" title="Copy" @click="copy(made.link, 'link')">
          <Check v-if="copied === 'link'" :size="16" /><Copy v-else :size="16" />
        </button>
      </div>
      <p class="secret-warn">
        <KeyRound :size="15" />
        <span
          >This link is a secret until it is used: anyone who has it can join a computer to your fleet. It
          works once, and expires in 24 hours<template v-if="expiresAt"> ({{ expiresAt }})</template>.</span
        >
      </p>
      <p class="join-state" :class="state">
        <template v-if="state === 'pending'"
          ><LoaderCircle :size="15" class="spin" />Waiting for {{ made.name }} to join…</template
        >
        <template v-else-if="state === 'used'"><CircleCheck :size="15" />{{ made.name }} joined the fleet.</template>
        <template v-else-if="state === 'expired'"><X :size="15" />This link expired. Make a new one.</template>
        <template v-else><X :size="15" />This link was cancelled.</template>
      </p>
      <p v-if="error" class="message-error" role="alert">{{ error }}</p>
      <footer>
        <button v-if="state === 'pending'" class="secondary danger-text" @click="cancel">Cancel link</button>
        <button v-else class="secondary" @click="again"><Plus :size="15" />Add another</button>
        <button class="primary" @click="emit('done')">Done</button>
      </footer>
    </section>
  </div>
</template>
