<script setup lang="ts">
// A dialog over the page: Esc or the backdrop closes it, focus starts inside and returns after.
import { nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { X } from "@lucide/vue";

const props = defineProps<{ eyebrow?: string; title: string; wide?: boolean }>();
const emit = defineEmits<{ close: [] }>();
const box = ref<HTMLElement>();
const before = document.activeElement as HTMLElement | null;

function keydown(e: KeyboardEvent) {
  if (e.key === "Escape") {
    e.stopPropagation();
    emit("close");
  }
}
onMounted(async () => {
  await nextTick();
  // A field that asks for focus, else (in a small dialog) its first answer, else the dialog.
  const first =
    box.value?.querySelector<HTMLElement>("[autofocus]") ??
    (props.wide ? undefined : box.value?.querySelector<HTMLElement>("footer button, footer a"));
  (first ?? box.value)?.focus({ preventScroll: !first });
});
onBeforeUnmount(() => before?.focus?.());
</script>

<template>
  <Teleport to="body">
    <div class="modal-backdrop" @mousedown.self="emit('close')" @keydown="keydown">
      <div ref="box" class="modal" :class="{ wide }" role="dialog" aria-modal="true" :aria-label="title" tabindex="-1">
        <header>
          <div>
            <span v-if="eyebrow" class="eyebrow">{{ eyebrow }}</span>
            <h2>{{ title }}</h2>
          </div>
          <button type="button" class="icon-button" aria-label="Close" @click="emit('close')"><X :size="18" /></button>
        </header>
        <slot />
      </div>
    </div>
  </Teleport>
</template>
