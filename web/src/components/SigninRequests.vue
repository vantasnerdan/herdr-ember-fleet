<script setup lang="ts">
// Browsers asking to sign in, shown on every signed-in page. The code matches
// the one on the asking browser's screen; approving signs that browser in.
import { ref } from "vue";
import { Check, MonitorSmartphone, X } from "@lucide/vue";
import { api, errorText } from "../lib/api";
import { ago } from "../lib/fleet";
import { notify, now, signinRequests } from "../lib/store";

const busy = ref("");

async function decide(id: string, approve: boolean) {
  busy.value = id;
  try {
    await api.decideSignin(id, approve);
    signinRequests.value = signinRequests.value.filter((r) => r.id !== id);
    notify(approve ? "Approved: that browser is signing in." : "Denied.", approve ? "ok" : "info");
  } catch (e) {
    notify(errorText(e), "error");
  } finally {
    busy.value = "";
  }
}
</script>

<template>
  <div v-if="signinRequests.length" class="signin-requests" role="region" aria-label="Browsers asking to sign in">
    <div v-for="r in signinRequests" :key="r.id" class="signin-request" role="alert">
      <MonitorSmartphone :size="18" />
      <div class="signin-request-text">
        <b>{{ r.agent }} wants to sign in</b>
        <small>Approve only if this code is on your other screen · {{ ago(r.at, now) }}</small>
      </div>
      <code class="signin-request-code">{{ r.code }}</code>
      <button class="primary" :disabled="busy === r.id" @click="decide(r.id, true)"><Check :size="15" />Approve</button>
      <button class="secondary" :disabled="busy === r.id" @click="decide(r.id, false)"><X :size="15" />Deny</button>
    </div>
  </div>
</template>
