<script setup lang="ts">
// Settings › General: the fleet's name.
import { computed, ref, watch } from "vue";
import { Check } from "@lucide/vue";
import { api, errorText } from "../../lib/api";
import { app } from "../../lib/store";

const fleetName = ref(app.me?.fleet ?? "");
const busy = ref(false);
const error = ref("");
const saved = ref("");
const changed = computed(() => fleetName.value.trim() !== (app.me?.fleet ?? ""));
watch(
  () => app.me?.fleet,
  (name, before) => {
    if (fleetName.value.trim() === before) fleetName.value = name ?? "";
  },
);

async function rename() {
  const name = fleetName.value.trim();
  if (!name || !changed.value || busy.value) return;
  busy.value = true;
  error.value = saved.value = "";
  try {
    await api.renameFleet(name);
    fleetName.value = app.me?.fleet ?? name;
    saved.value = "Fleet name saved.";
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <section class="settings-panel">
    <form class="fleet-name-form" @submit.prevent="rename">
      <label for="fleet-name">Fleet name</label>
      <p id="fleet-name-help" class="hint">The name at the top of the page. Everyone who uses this hub sees it.</p>
      <div class="inline-form">
        <input
          id="fleet-name"
          v-model="fleetName"
          name="fleet-name"
          maxlength="40"
          required
          :disabled="busy"
          autocomplete="off"
          aria-describedby="fleet-name-help"
          :aria-invalid="!!error"
          @input="saved = error = ''"
        />
        <button class="primary small" :disabled="busy || !fleetName.trim() || !changed">
          {{ busy ? "Saving…" : "Save name" }}
        </button>
      </div>
      <p v-if="error" class="message-error" role="alert">{{ error }}</p>
      <p v-else-if="saved" class="saved-note" role="status"><Check :size="14" />{{ saved }}</p>
    </form>
  </section>
</template>
