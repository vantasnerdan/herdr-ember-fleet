<script setup lang="ts">
// A group's header: its colour, name, how many agents it holds and which of them are
// working or waiting. Drag it to reorder groups; drop members on it to add them.
// Double-click the name (or "Rename" in its menu) to rename it in place.
import { computed, nextTick, ref, watch } from "vue";
import { ChevronDown, ChevronRight, Ellipsis } from "@lucide/vue";
import { colorHex, NAME_MAX, validName } from "../lib/groups";
import { plural, type TreeNode } from "../lib/fleet";

const props = defineProps<{
  node: TreeNode;
  open: boolean;
  focused: boolean;
  drop?: "into" | "before" | "after";
  editing: boolean;
  dragging?: boolean;
}>();
const emit = defineEmits<{
  toggle: [];
  menu: [anchor: DOMRect];
  rename: [name: string];
  stopEditing: [];
  edit: [];
}>();

const g = computed(() => props.node.group!);
const hex = computed(() => colorHex(g.value.color));
const folded = computed(() => !props.open && props.node.children.length > 0);
const meta = computed(() => {
  const c = props.node.counts;
  const n = props.node.children.length;
  if (!n) return "empty";
  const parts = [c.agents ? plural(c.agents, "agent") : "no agents"];
  const gone = props.node.children.filter((x) => x.kind === "missing").length;
  if (gone) parts.push(`${gone} not running`);
  return parts.join(" · ");
});

const draft = ref("");
const input = ref<HTMLInputElement>();
watch(
  () => props.editing,
  async (on) => {
    if (!on) return;
    draft.value = g.value.name;
    await nextTick();
    input.value?.focus();
    input.value?.select();
  },
  { immediate: true },
);

function commit() {
  const name = draft.value.trim();
  if (validName(name) && name !== g.value.name) emit("rename", name);
  else emit("stopEditing");
}

function openMenu(e: MouseEvent) {
  const el = (e.currentTarget as HTMLElement) ?? undefined;
  emit("menu", el.getBoundingClientRect());
}
</script>

<template>
  <div
    class="tree-row group-row"
    :class="[
      drop ? `drop-${drop}` : '',
      { focus: focused, dragging, 'has-color': !!hex },
    ]"
    :style="hex ? { '--group': hex } : undefined"
    role="treeitem"
    :aria-level="1"
    :aria-expanded="open"
    :aria-label="`Group ${g.name}, ${meta}`"
    :data-key="node.key"
    :draggable="!editing"
    @click="editing ? undefined : emit('toggle')"
  >
    <button
      class="chev"
      tabindex="-1"
      :aria-label="open ? `Collapse ${g.name}` : `Expand ${g.name}`"
      @click.stop="emit('toggle')"
    >
      <ChevronDown v-if="open" :size="16" /><ChevronRight v-else :size="16" />
    </button>
    <span class="swatch" :class="{ none: !hex }" />
    <span class="two">
      <input
        v-if="editing"
        ref="input"
        v-model="draft"
        class="group-name-input"
        :maxlength="NAME_MAX"
        aria-label="Group name"
        spellcheck="false"
        @click.stop
        @keydown.enter.prevent="commit"
        @keydown.esc.prevent.stop="emit('stopEditing')"
        @blur="commit"
      />
      <b v-else class="name" @dblclick.stop="emit('edit')">{{ g.name }}</b>
      <span class="meta">{{ meta }}</span>
    </span>
    <span class="right">
      <span v-if="node.counts.blocked" class="count blocked" :title="`${node.counts.blocked} blocked`"
        ><i class="sdot blocked" :class="{ beacon: folded }" />{{ node.counts.blocked }}</span
      >
      <span v-if="node.counts.working" class="count working" :title="`${node.counts.working} working`"
        ><i class="sdot working" :class="{ beacon: folded }" />{{ node.counts.working }}</span
      >
      <button
        class="icon-button row-more"
        tabindex="-1"
        :aria-label="`Group ${g.name}: rename, colour, delete`"
        title="Rename, colour, delete"
        @click.stop="openMenu"
      >
        <Ellipsis :size="16" />
      </button>
    </span>
  </div>
</template>
