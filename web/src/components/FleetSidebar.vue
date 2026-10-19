<script setup lang="ts">
// The sidebar: "Needs you", then every machine, its users, herdr sessions, workspaces,
// tabs and panes. Arrow keys walk the tree; Enter opens a pane or folds a row.
import { computed, nextTick, ref, watch } from "vue";
import { Network, Plus, Search, X } from "@lucide/vue";
import FleetRow from "./FleetRow.vue";
import NeedsYou from "./NeedsYou.vue";
import { filterTree, flatten, type TreeNode } from "../lib/fleet";
import { app, isOpen, machines, openPane, selectedKey, setOpen, tree } from "../lib/store";

const shown = computed(() => filterTree(tree.value, app.filter));
const filtering = computed(() => !!app.filter.trim());
const rows = computed(() => flatten(shown.value, (n) => filtering.value || isOpen(n.key)));
const selectedRow = computed(() => (selectedKey.value ? `p:${selectedKey.value}` : undefined));

const focusKey = ref<string>();
const list = ref<HTMLElement>();

function toggle(node: TreeNode, open = !isOpen(node.key)) {
  if (filtering.value) return;
  setOpen(node.key, open);
}

function activate(node: TreeNode) {
  focusKey.value = node.key;
  if (node.kind === "pane" && node.pane) openPane(node.pane.ref);
  else if (node.children.length) toggle(node);
}

function move(delta: number) {
  const i = rows.value.findIndex((r) => r.node.key === focusKey.value);
  const next = rows.value[Math.max(0, Math.min(rows.value.length - 1, (i < 0 ? 0 : i) + delta))];
  if (!next) return;
  focusKey.value = next.node.key;
  void nextTick(() =>
    list.value?.querySelector(`[data-key="${CSS.escape(next.node.key)}"]`)?.scrollIntoView({ block: "nearest" }),
  );
}

function keydown(e: KeyboardEvent) {
  if (e.target !== e.currentTarget) return;
  const row = rows.value.find((r) => r.node.key === focusKey.value);
  if (e.key === "ArrowDown") move(1);
  else if (e.key === "ArrowUp") move(-1);
  else if (e.key === "Home") move(-rows.value.length);
  else if (e.key === "End") move(rows.value.length);
  else if (e.key === "ArrowRight" && row) {
    if (!row.node.children.length) return;
    if (isOpen(row.node.key) || filtering.value) move(1);
    else toggle(row.node, true);
  } else if (e.key === "ArrowLeft" && row) {
    if (row.node.children.length && isOpen(row.node.key) && !filtering.value) toggle(row.node, false);
    else if (row.parent) focusKey.value = row.parent;
  } else if ((e.key === "Enter" || e.key === " ") && row) activate(row.node);
  else return;
  e.preventDefault();
}

function focusFirst() {
  focusKey.value ??= selectedRow.value ?? rows.value[0]?.node.key;
}

/** A DOM id per row, for aria-activedescendant. */
const rowId = (key: string) => `row-${key.replace(/[^a-zA-Z0-9_-]/g, (c) => `_${c.charCodeAt(0).toString(16)}`)}`;

// Bring the open pane into view when it is chosen from elsewhere (Needs you, a notification).
watch(selectedRow, (key) => {
  if (!key) return;
  void nextTick(() =>
    list.value?.querySelector(`[data-key="${CSS.escape(key)}"]`)?.scrollIntoView({ block: "nearest" }),
  );
});
</script>

<template>
  <aside class="sidebar" aria-label="Fleet">
    <NeedsYou />
    <header class="tree-head">
      <span :title="app.me?.fleet"><Network :size="15" />{{ (app.me?.fleet || "Fleet").toUpperCase() }}</span>
      <button class="icon-button" title="Add a machine" aria-label="Add a machine" @click="app.settings = 'add'">
        <Plus :size="17" />
      </button>
    </header>
    <div v-if="machines.length" class="tree-filter">
      <Search :size="14" />
      <input
        v-model="app.filter"
        name="filter"
        type="search"
        placeholder="Filter machines, agents, folders"
        aria-label="Filter the fleet"
        spellcheck="false"
        autocomplete="off"
        @keydown.esc="app.filter = ''"
      />
      <button v-if="app.filter" class="icon-button" aria-label="Clear filter" @click="app.filter = ''">
        <X :size="14" />
      </button>
    </div>
    <div
      ref="list"
      class="tree-list"
      role="tree"
      tabindex="0"
      aria-label="Machines, users, workspaces and panes"
      :aria-activedescendant="focusKey ? rowId(focusKey) : undefined"
      @keydown="keydown"
      @focus="focusFirst"
    >
      <FleetRow
        v-for="row in rows"
        :id="rowId(row.node.key)"
        :key="row.node.key"
        :node="row.node"
        :depth="row.depth"
        :open="filtering || isOpen(row.node.key)"
        :selected="selectedRow === row.node.key"
        :focused="focusKey === row.node.key"
        @toggle="toggle(row.node)"
        @activate="activate(row.node)"
      />
      <p v-if="!machines.length" class="hint tree-empty">
        No machines yet.
        <button class="text-button" @click="app.settings = 'add'">Add your first machine</button>
      </p>
      <p v-else-if="!rows.length" class="hint tree-empty">Nothing matches “{{ app.filter }}”.</p>
    </div>
  </aside>
</template>
