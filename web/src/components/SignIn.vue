<script setup lang="ts">
// Everything before the fleet: connecting, signing in from a link, signed out, hub unreachable.
import { computed } from "vue";
import { LoaderCircle } from "@lucide/vue";
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
      return { eyebrow: "Ember Fleet", title: "You are signed out", busy: false };
    default:
      return { eyebrow: "Ember Fleet", title: "Connecting to the hub…", busy: true };
  }
});

function retry() {
  app.phase = "loading";
  app.signinError = "";
  void boot();
}
</script>

<template>
  <main class="gate">
    <div class="gate-card">
      <img class="gate-mark" src="/mark.svg" alt="Ember Fleet" />
      <span class="eyebrow">{{ view.eyebrow }}</span>
      <h1>
        <LoaderCircle v-if="view.busy" :size="20" class="spin" />{{ view.title }}
      </h1>
      <template v-if="app.phase === 'signed-out'">
        <p>Run this on the hub to get a sign-in link, then open it in this browser:</p>
        <code class="gate-cmd">ember-fleet login-link</code>
        <p class="hint">Each link works once.</p>
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
