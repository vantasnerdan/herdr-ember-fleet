<script setup lang="ts">
// Everything before the fleet: connecting, signing in, signed out, hub unreachable.
// Signed out, this browser asks to be approved: it shows a code, and any browser
// that is already signed in approves it with one click (like signing in a TV).
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { LoaderCircle } from "@lucide/vue";
import { api, errorText } from "../lib/api";
import { app, boot } from "../lib/store";

const view = computed(() => {
  switch (app.phase) {
    case "signing-in":
      return { eyebrow: "Signing in", title: "Signing you in…", busy: true };
    case "signin-failed":
      return { eyebrow: "Sign-in link", title: "That link didn’t work", busy: false };
    case "unreachable":
      return { eyebrow: "Hub", title: "Can’t reach the hub", busy: false };
    case "signed-out":
      return { eyebrow: "Herdr Ember Fleet", title: "Your workspace awaits.", busy: false };
    default:
      return { eyebrow: "Herdr Ember Fleet", title: "Opening your workspace…", busy: true };
  }
});

// ---------------------------------------------------------------- asking for approval

const KEY = "ember.signin-ask";
const ask = ref<{ id: string; code: string; poll: string } | null>(null);
const askState = ref<"idle" | "asking" | "waiting" | "denied" | "expired" | "error">("idle");
const askError = ref("");
let timer: ReturnType<typeof setTimeout> | undefined;

function remember(v: typeof ask.value) {
  try {
    if (v) sessionStorage.setItem(KEY, JSON.stringify(v));
    else sessionStorage.removeItem(KEY);
  } catch {
    // private window: the request just won't survive a reload
  }
}

function recall(): typeof ask.value {
  try {
    const raw = sessionStorage.getItem(KEY);
    return raw ? JSON.parse(raw) : null;
  } catch {
    return null;
  }
}

async function startAsk() {
  clearTimeout(timer);
  askError.value = "";
  askState.value = "asking";
  try {
    ask.value = recall() ?? (await api.askSignin());
    remember(ask.value);
    askState.value = "waiting";
    void poll();
  } catch (e) {
    askState.value = "error";
    askError.value = errorText(e);
  }
}

async function poll() {
  if (!ask.value) return;
  try {
    const r = await api.pollSignin(ask.value.id, ask.value.poll);
    if (r.state === "approved") {
      remember(null);
      app.phase = "loading";
      await boot();
      return;
    }
    if (r.state === "denied" || r.state === "expired") {
      remember(null);
      ask.value = null;
      askState.value = r.state;
      return;
    }
  } catch (e) {
    // a stale request from before a hub restart: ask afresh
    remember(null);
    ask.value = null;
    askState.value = "error";
    askError.value = errorText(e);
    return;
  }
  timer = setTimeout(() => void poll(), 2000);
}

watch(
  () => app.phase,
  (p) => {
    if (p === "signed-out" && askState.value === "idle") void startAsk();
    if (p !== "signed-out") clearTimeout(timer);
  },
  { immediate: true },
);
onBeforeUnmount(() => clearTimeout(timer));

function retry() {
  app.phase = "loading";
  app.signinError = "";
  void boot();
}
</script>

<template>
  <main class="gate">
    <div class="gate-card">
      <img class="gate-mark" src="/mark.svg" alt="" />
      <span class="eyebrow">{{ view.eyebrow }}</span>
      <h1>
        <LoaderCircle v-if="view.busy" :size="20" class="spin" />{{ view.title }}
      </h1>
      <template v-if="app.phase === 'signed-out'">
        <template v-if="askState === 'waiting' && ask">
          <p>Approve this browser from one where you’re already signed in. It shows this code:</p>
          <code class="gate-code" aria-label="Sign-in code">{{ ask.code }}</code>
          <p class="hint"><LoaderCircle :size="14" class="spin" /> Waiting for approval…</p>
        </template>
        <template v-else-if="askState === 'asking'">
          <p class="hint"><LoaderCircle :size="14" class="spin" /> Asking to sign in…</p>
        </template>
        <template v-else>
          <p v-if="askState === 'denied'" class="message-error" role="alert">That request was denied.</p>
          <p v-else-if="askState === 'expired'" class="hint">That request expired.</p>
          <p v-else-if="askState === 'error'" class="message-error" role="alert">{{ askError }}</p>
          <button class="secondary" @click="startAsk">Ask to sign in</button>
        </template>
        <p class="hint gate-note">
          No browser signed in yet? On the hub, run <code>ember-fleet login-link</code> and open the link here.
        </p>
      </template>
      <template v-else-if="app.phase === 'signin-failed'">
        <p class="message-error" role="alert">{{ app.signinError }}</p>
        <p>Run this on the hub for a fresh one:</p>
        <code class="gate-cmd">ember-fleet login-link</code>
        <button class="secondary" @click="retry">I’m signed in already</button>
      </template>
      <template v-else-if="app.phase === 'unreachable'">
        <p class="hint">{{ app.signinError || "No answer." }} Trying again…</p>
        <button class="secondary" @click="retry">Try now</button>
      </template>
    </div>
  </main>
</template>
