<script setup lang="ts">
// Settings › About: versions, the hub's key, its address.
import { ref } from "vue";
import { Check, Copy } from "@lucide/vue";
import { copyText } from "../../lib/clipboard";
import { app } from "../../lib/store";

const origin = location.origin;
const copied = ref("");
async function copy(text: string, what: string) {
  if (await copyText(text)) {
    copied.value = what;
    setTimeout(() => copied.value === what && (copied.value = ""), 1800);
  }
}
</script>

<template>
  <section class="settings-panel">
    <p class="about-product">Herdr Ember Fleet <span class="badge">{{ app.me?.version }}</span></p>
    <dl class="facts">
      <dt>Hub key</dt>
      <dd class="copy-fact">
        <code :title="'The fingerprint of the hub’s public key. Not a secret: machines check it when they join.'">{{ app.me?.hub }}</code>
        <button class="icon-button" aria-label="Copy the hub key" title="Copy" @click="copy(app.me?.hub ?? '', 'hub')">
          <Check v-if="copied === 'hub'" :size="15" /><Copy v-else :size="15" />
        </button>
      </dd>
      <dt>Address</dt>
      <dd class="copy-fact">
        <span>{{ origin }}</span>
        <button class="icon-button" aria-label="Copy the address" title="Copy" @click="copy(origin, 'addr')">
          <Check v-if="copied === 'addr'" :size="15" /><Copy v-else :size="15" />
        </button>
      </dd>
    </dl>
  </section>
</template>
