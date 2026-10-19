<script lang="ts">
import type { Component } from "vue";

export interface MenuItem {
  label: string;
  icon?: Component;
  /** A colour dot before the label; "" draws an empty ring (no colour). */
  swatch?: string;
  checked?: boolean;
  danger?: boolean;
  disabled?: boolean;
  /** A rule above this item. */
  rule?: boolean;
  run?: () => void;
}
</script>

<script setup lang="ts">
// A small menu at a point: a row's "Move to group…", a group's rename, colour and delete.
// Arrow keys move between items, Esc or a click elsewhere closes it, focus returns after.
import { nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { Check } from "@lucide/vue";

const props = defineProps<{ items: MenuItem[]; x: number; y: number; title?: string }>();
const emit = defineEmits<{ close: [] }>();

const box = ref<HTMLElement>();
const pos = ref({ left: props.x, top: props.y });
const before = document.activeElement as HTMLElement | null;

function outside(e: PointerEvent) {
  if (box.value && !box.value.contains(e.target as Node)) emit("close");
}

onMounted(async () => {
  await nextTick();
  const el = box.value;
  if (!el) return;
  // keep it on screen
  const r = el.getBoundingClientRect();
  pos.value = {
    left: Math.max(8, Math.min(props.x, innerWidth - r.width - 8)),
    top: props.y + r.height > innerHeight - 8 ? Math.max(8, props.y - r.height) : props.y,
  };
  el.querySelector<HTMLElement>("button:not(:disabled)")?.focus();
  document.addEventListener("pointerdown", outside, true);
});
onBeforeUnmount(() => {
  document.removeEventListener("pointerdown", outside, true);
  before?.focus?.({ preventScroll: true });
});

function keydown(e: KeyboardEvent) {
  const buttons = [...(box.value?.querySelectorAll<HTMLElement>("button:not(:disabled)") ?? [])];
  const i = buttons.indexOf(document.activeElement as HTMLElement);
  if (e.key === "Escape" || e.key === "Tab") emit("close");
  else if (e.key === "ArrowDown") buttons[(i + 1) % buttons.length]?.focus();
  else if (e.key === "ArrowUp") buttons[(i - 1 + buttons.length) % buttons.length]?.focus();
  else if (e.key === "Home") buttons[0]?.focus();
  else if (e.key === "End") buttons[buttons.length - 1]?.focus();
  else return;
  e.preventDefault();
  e.stopPropagation();
}

function choose(item: MenuItem) {
  emit("close");
  item.run?.();
}
</script>

<template>
  <Teleport to="body">
    <div
      ref="box"
      class="row-menu-pop"
      role="menu"
      :aria-label="title"
      :style="{ left: `${pos.left}px`, top: `${pos.top}px` }"
      @keydown="keydown"
    >
      <div v-if="title" class="row-menu-title">{{ title }}</div>
      <template v-for="(item, i) in items" :key="i">
        <hr v-if="item.rule" />
        <button
          role="menuitem"
          :class="{ danger: item.danger, checked: item.checked }"
          :disabled="item.disabled"
          @click="choose(item)"
        >
          <span v-if="item.swatch !== undefined" class="swatch" :class="{ none: !item.swatch }" :style="item.swatch ? { background: item.swatch } : undefined" />
          <component :is="item.icon" v-else-if="item.icon" :size="14" />
          <span class="label">{{ item.label }}</span>
          <Check v-if="item.checked" class="tick" :size="14" />
        </button>
      </template>
    </div>
  </Teleport>
</template>
