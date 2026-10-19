<script setup lang="ts">
// The one question the page is asking right now: confirm, name something, or open a link.
import { ref, watch } from "vue";
import { ExternalLink } from "@lucide/vue";
import Modal from "./Modal.vue";
import { asking } from "../lib/store";

const value = ref("");
watch(asking, (a) => (value.value = a?.input?.value ?? ""), { immediate: true });

function submit() {
  const a = asking.value;
  if (!a) return;
  if (a.input) {
    const v = value.value.trim();
    if (v) a.resolve(v);
  } else a.resolve(true);
}
</script>

<template>
  <Modal
    v-if="asking"
    :eyebrow="asking.eyebrow"
    :title="asking.title"
    @close="asking.resolve(null)"
  >
    <form @submit.prevent="submit">
      <p v-if="asking.body" class="body-text">{{ asking.body }}</p>
      <code v-if="asking.link" class="link-preview">{{ asking.link }}</code>
      <label v-if="asking.input"
        >{{ asking.input.label
        }}<input
          v-model="value"
          name="answer"
          autofocus
          :placeholder="asking.input.placeholder"
          :maxlength="asking.input.maxlength ?? 80"
          spellcheck="false"
          autocomplete="off"
      /></label>
      <footer>
        <button type="button" class="secondary" @click="asking.resolve(null)">{{ asking.cancel ?? "Cancel" }}</button>
        <a
          v-if="asking.link"
          class="primary"
          :href="asking.link"
          target="_blank"
          rel="noopener noreferrer"
          referrerpolicy="no-referrer"
          @click="asking.resolve(true)"
          ><ExternalLink :size="15" />{{ asking.confirm }}</a
        >
        <button
          v-else
          type="submit"
          class="primary"
          :class="{ danger: asking.danger }"
          :disabled="!!asking.input && !value.trim()"
        >
          {{ asking.confirm }}
        </button>
      </footer>
    </form>
  </Modal>
</template>
