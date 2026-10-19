<script setup lang="ts">
// Settings › Signing in: the fleet's PIN (types into the sign-in page on any computer,
// never expires) and the browsers signed in now, each of which can be signed out.
import { computed, onMounted, ref } from "vue";
import { Check, Copy, KeyRound, LogOut, MonitorSmartphone, RefreshCw, Trash2 } from "@lucide/vue";
import { api, errorText } from "../lib/api";
import { ago } from "../lib/fleet";
import { app, confirmAsk, notify, now, promptAsk } from "../lib/store";
import type { BrowserSession, PinStatus } from "../lib/types";

const status = ref<PinStatus | null>(null);
const fresh = ref(""); // a PIN the hub just made, shown once
const sessions = ref<BrowserSession[]>([]);
const error = ref("");
const busy = ref(false);
const copied = ref(false);

async function load() {
  try {
    const [p, s] = await Promise.all([api.pin(), api.sessions()]);
    status.value = p;
    sessions.value = s.sessions ?? [];
  } catch (e) {
    error.value = errorText(e);
  }
}
onMounted(load);

async function run(f: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    await f();
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}

async function makePin() {
  if (status.value?.set) {
    const ok = await confirmAsk({
      title: "Make a new PIN?",
      body: "The current PIN stops working. Browsers already signed in stay signed in.",
      confirm: "New PIN",
    });
    if (!ok) return;
  }
  await run(async () => {
    const r = await api.setPin();
    fresh.value = r.pin ?? "";
    status.value = r;
    app.pinSignin = true;
  });
}

async function choosePin() {
  const typed = await promptAsk({
    eyebrow: "Signing in",
    title: status.value?.set ? "Change your PIN" : "Choose a PIN",
    body: "6 or more letters or digits. Spaces, dashes and capitals don’t matter. Browsers already signed in stay signed in.",
    confirm: "Save PIN",
    input: { label: "New PIN", value: "", maxlength: 64 },
  });
  if (!typed) return;
  await run(async () => {
    status.value = await api.setPin(typed);
    fresh.value = "";
    app.pinSignin = true;
    notify("PIN saved.", "ok");
  });
}

async function removePin() {
  const ok = await confirmAsk({
    title: "Turn off PIN sign-in?",
    body: "New browsers will need approval from a signed-in one instead. Browsers already signed in stay signed in.",
    confirm: "Turn off",
    danger: true,
  });
  if (!ok) return;
  await run(async () => {
    status.value = await api.removePin();
    fresh.value = "";
    app.pinSignin = false;
  });
}

async function copyPin() {
  try {
    await navigator.clipboard.writeText(fresh.value);
    copied.value = true;
    setTimeout(() => (copied.value = false), 1800);
  } catch {
    error.value = "Copying failed: select the PIN and copy it by hand.";
  }
}

const others = computed(() => sessions.value.filter((s) => !s.current));

function how(s: BrowserSession): string {
  const via = s.via === "pin" ? "with the PIN" : s.via === "approval" ? "by approval" : s.via === "link" ? "with a sign-in link" : "";
  const parts = [`signed in ${via ? via + " " : ""}${ago(s.created, now.value)}`];
  if (s.seen && s.seen - s.created > 3_600_000) parts.push(`used ${ago(s.seen, now.value)}`);
  return parts.join(" · ");
}

async function signOutBrowser(s: BrowserSession) {
  const ok = await confirmAsk({
    title: `Sign out ${s.agent || "that browser"}?`,
    body: "Its open pages close now. It can sign in again with the PIN or by approval.",
    confirm: "Sign out",
    danger: true,
  });
  if (!ok) return;
  await run(async () => {
    await api.endSession(s.id);
    sessions.value = sessions.value.filter((x) => x.id !== s.id);
    notify("Signed out.", "ok");
  });
}
</script>

<template>
  <section class="fleet-section signin-panel">
    <h3><KeyRound :size="16" />Signing in</h3>
    <p class="hint">
      To use this page on another computer, open this address there and type the PIN. It doesn’t expire, and that
      browser stays signed in while you use it. Five wrong tries pause PIN sign-in for 15 minutes.
    </p>
    <template v-if="status">
      <div v-if="fresh" class="token-panel">
        <p class="secret-warn">
          <KeyRound :size="15" /><span>Your new PIN. Shown only this once, so note it down.</span>
        </p>
        <div class="copy-row pin-show">
          <code>{{ fresh }}</code>
          <button class="icon-button" aria-label="Copy the PIN" title="Copy" @click="copyPin">
            <Check v-if="copied" :size="16" /><Copy v-else :size="16" />
          </button>
        </div>
      </div>
      <p v-else class="pin-state">
        <span class="badge" :class="status.set ? 'ok' : ''">{{ status.set ? "PIN on" : "No PIN" }}</span>
        <small class="muted" v-if="status.set">set {{ ago(status.at, now) }}</small>
      </p>
      <div class="pin-actions">
        <button class="secondary" :disabled="busy" @click="makePin">
          <RefreshCw :size="15" />{{ status.set ? "New PIN" : "Make a PIN" }}
        </button>
        <button class="secondary" :disabled="busy" @click="choosePin">Choose my own</button>
        <button v-if="status.set" class="secondary danger-text" :disabled="busy" @click="removePin">
          <Trash2 :size="15" />Turn off
        </button>
      </div>
    </template>
    <p v-else-if="!error" class="hint">Loading…</p>

    <h4 class="subhead">Signed-in browsers</h4>
    <ul class="item-list">
      <li v-for="s in sessions" :key="s.id">
        <span class="mark"><MonitorSmartphone :size="18" /></span>
        <div class="item-text">
          <span class="line"
            ><b>{{ s.agent || "A browser" }}</b><span v-if="s.current" class="badge ok">this browser</span></span
          >
          <small>{{ how(s) }}</small>
        </div>
        <button
          v-if="!s.current"
          class="icon-button danger-text"
          :aria-label="`Sign out ${s.agent || 'that browser'}`"
          title="Sign out this browser"
          :disabled="busy"
          @click="signOutBrowser(s)"
        >
          <LogOut :size="15" />
        </button>
      </li>
    </ul>
    <p v-if="status && !others.length" class="hint">Only this browser is signed in.</p>
    <p v-if="error" class="message-error" role="alert">{{ error }}</p>
  </section>
</template>
