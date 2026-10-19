<script setup lang="ts">
// Name a group and pick its colour: for a new group, or to change one.
import { ref } from "vue";
import Modal from "./Modal.vue";
import { GROUP_COLORS, NAME_MAX, validName } from "../lib/groups";

const props = defineProps<{ title: string; confirm: string; name?: string; color?: string; eyebrow?: string }>();
const emit = defineEmits<{ done: [value: { name: string; color: string }]; close: [] }>();

const name = ref(props.name ?? "");
const color = ref(props.color ?? "");

function submit() {
  if (validName(name.value)) emit("done", { name: name.value.trim(), color: color.value });
}
</script>

<template>
  <Modal :eyebrow="eyebrow ?? 'Groups'" :title="title" @close="emit('close')">
    <form class="group-form" @submit.prevent="submit">
      <label
        >Name<input
          v-model="name"
          autofocus
          :maxlength="NAME_MAX"
          placeholder="e.g. Physics, Coding, Burst"
          spellcheck="false"
          autocomplete="off"
      /></label>
      <fieldset class="swatches">
        <legend>Colour <span class="hint">optional</span></legend>
        <label class="swatch-pick" :class="{ on: color === '' }" title="No colour">
          <input v-model="color" type="radio" name="group-color" value="" />
          <span class="swatch none" />
          <span class="sr">None</span>
        </label>
        <label
          v-for="c in GROUP_COLORS"
          :key="c.id"
          class="swatch-pick"
          :class="{ on: color === c.id }"
          :title="c.label"
        >
          <input v-model="color" type="radio" name="group-color" :value="c.id" />
          <span class="swatch" :style="{ background: c.hex }" />
          <span class="sr">{{ c.label }}</span>
        </label>
      </fieldset>
      <footer>
        <button type="button" class="secondary" @click="emit('close')">Cancel</button>
        <button type="submit" class="primary" :disabled="!validName(name)">{{ confirm }}</button>
      </footer>
    </form>
  </Modal>
</template>
