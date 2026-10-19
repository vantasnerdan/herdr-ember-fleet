<script setup lang="ts">
// Settings › Danger zone: things that lock people out. Each asks first.
// (Removing a machine is on that machine's page.)
import { computed, onMounted, ref } from "vue";
import { KeyRound, LogOut, MonitorX } from "@lucide/vue";
import { api, errorText } from "../../lib/api";
import { app, browserSessions, confirmAsk, notify, pinStatus, signOut } from "../../lib/store";

const error = ref("");
const busy = ref(false);

onMounted(async () => {
  try {
    const [p, s] = await Promise.all([api.pin(), api.sessions()]);
    pinStatus.value = p;
    browserSessions.value = s.sessions ?? [];
  } catch (e) {
    error.value = errorText(e);
  }
});

const others = computed(() => browserSessions.value.filter((s) => !s.current));

async function run(f: () => Promise<void>) {
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

async function pinOff() {
  const ok = await confirmAsk({
    title: "Turn off PIN sign-in?",
    body: "New browsers will need approval from a signed-in one instead. Browsers already signed in stay signed in.",
    confirm: "Turn off",
    danger: true,
  });
  if (ok)
    await run(async () => {
      pinStatus.value = await api.removePin();
      app.pinSignin = false;
      notify("PIN sign-in is off.", "ok");
    });
}

async function signOutOthers() {
  const n = others.value.length;
  const ok = await confirmAsk({
    title: `Sign out ${n} other browser${n === 1 ? "" : "s"}?`,
    body: "Their open pages close now. Only this browser stays signed in.",
    confirm: "Sign them out",
    danger: true,
  });
  if (ok)
    await run(async () => {
      for (const s of others.value) await api.endSession(s.id);
      browserSessions.value = browserSessions.value.filter((s) => s.current);
      notify("Every other browser is signed out.", "ok");
    });
}

async function signOutHere() {
  const ok = await confirmAsk({
    title: "Sign out of this browser?",
    body: app.pinSignin ? "You can sign in again with the PIN." : "You’ll need approval from another signed-in browser, or a sign-in link.",
    confirm: "Sign out",
    danger: true,
  });
  if (ok) await signOut();
}
</script>

<template>
  <section class="settings-panel danger-zone">
    <div class="danger-row">
      <div>
        <b><KeyRound :size="15" />Turn off PIN sign-in</b>
        <p class="hint">The PIN stops working. New browsers then need approval.</p>
      </div>
      <button class="secondary danger-text" :disabled="busy || !pinStatus?.set" @click="pinOff">
        {{ pinStatus?.set ? "Turn off" : "Already off" }}
      </button>
    </div>
    <div class="danger-row">
      <div>
        <b><MonitorX :size="15" />Sign out every other browser</b>
        <p class="hint">{{ others.length ? `${others.length} other browser${others.length === 1 ? " is" : "s are"} signed in.` : "No other browser is signed in." }}</p>
      </div>
      <button class="secondary danger-text" :disabled="busy || !others.length" @click="signOutOthers">Sign them out</button>
    </div>
    <div class="danger-row">
      <div>
        <b><LogOut :size="15" />Sign out of this browser</b>
        <p class="hint">To remove a machine from the fleet, open it from Machines.</p>
      </div>
      <button class="secondary danger-text" :disabled="busy" @click="signOutHere">Sign out</button>
    </div>
    <p v-if="error" class="message-error" role="alert">{{ error }}</p>
  </section>
</template>
