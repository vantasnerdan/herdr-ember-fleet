<script setup lang="ts">
// Settings › Signing in: the fleet's PIN. Typed on the sign-in page of any computer,
// it never expires. Turning it off is in the danger zone.
import { onMounted, ref } from "vue";
import { Check, Copy, KeyRound, RefreshCw } from "@lucide/vue";
import { api, errorText } from "../../lib/api";
import { copyText } from "../../lib/clipboard";
import { ago } from "../../lib/fleet";
import { app, confirmAsk, notify, now, pinStatus, promptAsk } from "../../lib/store";

const fresh = ref(""); // a PIN the hub just made, shown once
const error = ref("");
const busy = ref(false);
const copied = ref(false);

onMounted(async () => {
  try {
    pinStatus.value = await api.pin();
  } catch (e) {
    error.value = errorText(e);
  }
});

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
  if (pinStatus.value?.set) {
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
    pinStatus.value = r;
    app.pinSignin = true;
  });
}

async function choosePin() {
  const typed = await promptAsk({
    eyebrow: "Signing in",
    title: pinStatus.value?.set ? "Change your PIN" : "Choose a PIN",
    body: "6 or more letters or digits. Spaces, dashes and capitals don’t matter. Browsers already signed in stay signed in.",
    confirm: "Save PIN",
    input: { label: "New PIN", value: "", maxlength: 64 },
  });
  if (!typed) return;
  await run(async () => {
    pinStatus.value = await api.setPin(typed);
    fresh.value = "";
    app.pinSignin = true;
    notify("PIN saved.", "ok");
  });
}

async function copyPin() {
  if (await copyText(fresh.value)) {
    copied.value = true;
    setTimeout(() => (copied.value = false), 1800);
  } else error.value = "Copying failed: select the PIN and copy it by hand.";
}
</script>

<template>
  <section class="settings-panel">
    <p class="hint">
      To use this page on another computer, open this address there and type the PIN. It doesn’t expire, and that
      browser stays signed in while you use it. Five wrong tries pause PIN sign-in for 15 minutes.
    </p>
    <template v-if="pinStatus">
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
        <span class="badge" :class="pinStatus.set ? 'ok' : ''">{{ pinStatus.set ? "PIN on" : "No PIN" }}</span>
        <small class="muted" v-if="pinStatus.set">set {{ ago(pinStatus.at, now) }}</small>
        <small class="muted" v-else>New browsers need approval from a signed-in one.</small>
      </p>
      <div class="pin-actions">
        <button class="secondary" :disabled="busy" @click="makePin">
          <RefreshCw :size="15" />{{ pinStatus.set ? "New PIN" : "Make a PIN" }}
        </button>
        <button class="secondary" :disabled="busy" @click="choosePin">Choose my own</button>
      </div>
    </template>
    <p v-else-if="!error" class="hint">Loading…</p>
    <p v-if="error" class="message-error" role="alert">{{ error }}</p>
  </section>
</template>
