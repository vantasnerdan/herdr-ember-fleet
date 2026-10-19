<script setup lang="ts">
// Settings › Command line: access tokens for `ember-fleet attach` and `term` elsewhere.
import { computed, ref } from "vue";
import { Check, Copy, KeyRound } from "@lucide/vue";
import { api, errorText } from "../../lib/api";
import { copyText } from "../../lib/clipboard";

const name = ref("");
const token = ref("");
const busy = ref(false);
const error = ref("");
const copied = ref("");
const loginCommand = computed(() => `ember-fleet login ${location.origin}`);

async function make() {
  const n = name.value.trim();
  if (!n || busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    token.value = (await api.createToken(n)).token;
    name.value = "";
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}

async function copy(text: string, what: string) {
  if (await copyText(text)) {
    copied.value = what;
    setTimeout(() => copied.value === what && (copied.value = ""), 1800);
  } else error.value = "Copying failed: select the text and copy it by hand.";
}
</script>

<template>
  <section class="settings-panel">
    <p class="hint">
      Only for the <code>ember-fleet attach</code> and <code>ember-fleet term</code> commands on another computer.
      To use this page there, you only need the PIN.
    </p>
    <form class="inline-form" @submit.prevent="make">
      <input v-model="name" name="token-name" placeholder="Name it, e.g. laptop" maxlength="60" aria-label="Token name" />
      <button class="secondary" :disabled="busy || !name.trim()">Create token</button>
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
    <p v-if="error" class="message-error" role="alert">{{ error }}</p>
  </section>
</template>
