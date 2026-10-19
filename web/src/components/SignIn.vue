<script setup lang="ts">
// Everything before the fleet: connecting, signing in, signed out, hub unreachable.
// Signed out, there are two ways in: the fleet's PIN (it never expires, so it works
// from any computer), or approval: this browser shows a code and a browser that is
// already signed in approves it with one click (like signing in a TV).
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { Eye, EyeOff, LoaderCircle } from "@lucide/vue";
import { api, ApiError, errorText } from "../lib/api";
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

/** With a PIN set, the PIN comes first and approval is the fallback. */
const mode = ref<"pin" | "approve">("pin");

// ---------------------------------------------------------------- the PIN

const pin = ref("");
const showPin = ref(false);
const pinBusy = ref(false);
const pinError = ref("");
const pinInput = ref<HTMLInputElement | null>(null);

async function signInWithPin() {
  if (!pin.value.trim() || pinBusy.value) return;
  pinBusy.value = true;
  pinError.value = "";
  try {
    await api.pinSignin(pin.value);
    pin.value = "";
    await withdrawAsk();
    app.phase = "loading";
    await boot();
  } catch (e) {
    pinError.value =
      e instanceof ApiError && e.status === 401 ? "That PIN isn’t right. Check it and try again." : capital(errorText(e));
    await nextTick();
    pinInput.value?.select();
  } finally {
    pinBusy.value = false;
  }
}

const capital = (s: string) => s.charAt(0).toUpperCase() + s.slice(1) + (/[.!?]$/.test(s) ? "" : ".");

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
  mode.value = "approve";
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

/** Takes back a waiting request, so signed-in browsers stop showing it. */
async function withdrawAsk() {
  clearTimeout(timer);
  const a = ask.value ?? recall();
  ask.value = null;
  askState.value = "idle";
  remember(null);
  if (a) await api.cancelSignin(a.id, a.poll).catch(() => {});
}

async function usePin() {
  await withdrawAsk();
  mode.value = "pin";
  await nextTick();
  pinInput.value?.focus();
}

watch(
  () => app.phase,
  (p) => {
    if (p !== "signed-out") return clearTimeout(timer);
    if (askState.value !== "idle") return;
    // A request already waiting (this tab was reloaded), or no PIN to offer: ask.
    if (recall() || !app.pinSignin) void startAsk();
    else mode.value = "pin";
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
        <template v-if="app.pinSignin && mode === 'pin'">
          <form class="pin-form" @submit.prevent="signInWithPin">
            <input type="text" name="username" autocomplete="username" value="Herdr Ember Fleet" hidden />
            <label for="signin-pin">Your PIN</label>
            <div class="pin-row">
              <span class="pin-field">
                <input
                  id="signin-pin"
                  ref="pinInput"
                  v-model="pin"
                  name="pin"
                  :type="showPin ? 'text' : 'password'"
                  autocomplete="current-password"
                  autocapitalize="off"
                  spellcheck="false"
                  maxlength="80"
                  autofocus
                  :disabled="pinBusy"
                  :aria-invalid="!!pinError"
                  aria-describedby="signin-pin-help"
                  @input="pinError = ''"
                />
                <button
                  type="button"
                  class="icon-button"
                  :aria-label="showPin ? 'Hide the PIN' : 'Show the PIN'"
                  :title="showPin ? 'Hide' : 'Show'"
                  @click="showPin = !showPin"
                >
                  <EyeOff v-if="showPin" :size="16" /><Eye v-else :size="16" />
                </button>
              </span>
              <button class="primary" :disabled="pinBusy || !pin.trim()">
                <LoaderCircle v-if="pinBusy" :size="15" class="spin" />Sign in
              </button>
            </div>
            <p v-if="pinError" class="message-error" role="alert">{{ pinError }}</p>
            <p v-else id="signin-pin-help" class="hint">This browser stays signed in while you use it.</p>
          </form>
          <button class="text-button gate-switch" @click="startAsk">
            No PIN with you? Approve this browser from one that’s signed in
          </button>
        </template>
        <template v-else>
          <template v-if="askState === 'waiting' && ask">
            <p>Approve this browser from one where you’re already signed in. It shows this code:</p>
            <code class="gate-code" aria-label="Sign-in code">{{ ask.code }}</code>
            <p class="hint"><LoaderCircle :size="14" class="spin" /> Waiting for approval (10 minutes)…</p>
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
          <button v-if="app.pinSignin" class="text-button gate-switch" @click="usePin">Use your PIN instead</button>
        </template>
        <p class="hint gate-note">
          <template v-if="app.pinSignin">Forgot the PIN?</template><template v-else>No browser signed in yet?</template>
          On the hub, run <code>ember-fleet pin</code> for a new PIN.
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
