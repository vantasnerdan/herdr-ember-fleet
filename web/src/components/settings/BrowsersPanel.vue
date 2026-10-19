<script setup lang="ts">
// Settings › Browsers: every signed-in browser, with how and when it signed in.
// Signing one out closes its open pages at once.
import { computed, onMounted, ref } from "vue";
import { LogOut, MonitorSmartphone } from "@lucide/vue";
import { api, errorText } from "../../lib/api";
import { ago } from "../../lib/fleet";
import { browserSessions, confirmAsk, notify, now } from "../../lib/store";
import type { BrowserSession } from "../../lib/types";

const error = ref("");
const busy = ref(false);

async function load() {
  try {
    browserSessions.value = (await api.sessions()).sessions ?? [];
  } catch (e) {
    error.value = errorText(e);
  }
}
onMounted(load);

const others = computed(() => browserSessions.value.filter((s) => !s.current));

function how(s: BrowserSession): string {
  const via = s.via === "pin" ? "with the PIN" : s.via === "approval" ? "by approval" : s.via === "link" ? "with a sign-in link" : "";
  return `signed in ${via ? via + " " : ""}${ago(s.created, now.value)}`;
}
function used(s: BrowserSession): string {
  if (s.current) return "in use now";
  if (!s.seen) return "";
  return now.value - s.seen < 86_400_000 ? "used today" : `last used ${ago(s.seen, now.value)}`;
}

async function signOut(s: BrowserSession) {
  const ok = await confirmAsk({
    title: `Sign out ${s.agent || "that browser"}?`,
    body: "Its open pages close now. It can sign in again with the PIN or by approval.",
    confirm: "Sign out",
    danger: true,
  });
  if (!ok) return;
  busy.value = true;
  try {
    await api.endSession(s.id);
    browserSessions.value = browserSessions.value.filter((x) => x.id !== s.id);
    notify("Signed out.", "ok");
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <section class="settings-panel">
    <p class="hint">Browsers signed in to this fleet. A lost or borrowed computer can be signed out from here.</p>
    <ul class="item-list">
      <li v-for="s in browserSessions" :key="s.id">
        <span class="mark"><MonitorSmartphone :size="18" /></span>
        <div class="item-text">
          <span class="line"
            ><b>{{ s.agent || "Unknown browser" }}</b><span v-if="s.current" class="badge ok">this browser</span></span
          >
          <small>{{ how(s) }}<template v-if="used(s)"> · {{ used(s) }}</template></small>
        </div>
        <button
          v-if="!s.current"
          class="secondary small danger-text"
          :aria-label="`Sign out ${s.agent || 'that browser'}`"
          :disabled="busy"
          @click="signOut(s)"
        >
          <LogOut :size="14" />Sign out
        </button>
      </li>
    </ul>
    <p v-if="browserSessions.length && !others.length" class="hint">Only this browser is signed in.</p>
    <p v-if="error" class="message-error" role="alert">{{ error }}</p>
  </section>
</template>
