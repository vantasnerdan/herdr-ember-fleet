<script setup lang="ts">
// The sidebar: "Needs you", then the fleet in one of two views, so each agent shows once:
//   Groups    the viewer's groups (Physics, Coding, Burst…), then what is in no group
//   Machines  every machine, its users, herdr sessions, workspaces, tabs and panes
// Plain shells hide unless the viewer shows them.
//
// Arrow keys walk the tree; Enter opens a pane or folds a row; M (or the context-menu key)
// opens "Move to group…" for the focused agent or machine user, F2 renames a group.
// With a mouse, agents and machine users drag into groups, members drag to reorder or out
// to the machines to leave their group, and group headers drag to reorder groups.
import { computed, nextTick, ref, watch } from "vue";
import { ArrowDown, ArrowUp, FolderPlus, LogOut, Network, Pencil, Plus, Search, SquareTerminal, Trash2, X } from "@lucide/vue";
import FleetRow from "./FleetRow.vue";
import GroupRow from "./GroupRow.vue";
import GroupDialog from "./GroupDialog.vue";
import NeedsYou from "./NeedsYou.vue";
import RowMenu, { type MenuItem } from "./RowMenu.vue";
import { filterTree, flatten, isShellRow, prune, type Row, type TreeNode } from "../lib/fleet";
import {
  colorHex,
  createGroup,
  deleteGroup,
  GROUP_COLORS,
  groupOf,
  groupOfNode,
  groupTree,
  memberKey,
  memberOf,
  moveMember,
  reorderGroups,
  updateGroup,
} from "../lib/groups";
import { app, confirmAsk, groups, isOpen, machines, openPane, selectedKey, setOpen, showShells, tree, treeView } from "../lib/store";
import type { Group, Member } from "../lib/types";

// ---------------------------------------------------------------- what is shown

const view = treeView;
const shells = showShells;
const byGroup = computed(() => view.value === "groups");

const filtering = computed(() => !!app.filter.trim());
const openRow = (n: TreeNode) => filtering.value || isOpen(n.key);
const hideShells = (list: TreeNode[]) => (shells.value ? list : prune(list, isShellRow));
const groupRows = computed(() =>
  byGroup.value ? flatten(filterTree(hideShells(groupTree.value), app.filter), openRow) : [],
);
/** In the Groups view, only what is in no group; in the Machines view, everything. */
const machineTree = computed(() => {
  const all = byGroup.value ? prune(tree.value, (n) => !!groupOfNode(n)) : tree.value;
  return hideShells(all);
});
const machineRows = computed(() => flatten(filterTree(machineTree.value, app.filter), openRow));
const shellCount = computed(() => tree.value.reduce((n, m) => n + m.counts.shells, 0));
const rows = computed(() => [...groupRows.value, ...machineRows.value]);
const rowByKey = computed(() => new Map(rows.value.map((r) => [r.node.key, r])));

const isSelected = (n: TreeNode) => n.kind === "pane" && !!n.pane && n.pane.key === selectedKey.value;
/** In the Machines view, a grouped agent or machine user wears its group's name. */
const tagOf = (n: TreeNode) => (n.inGroup || byGroup.value ? undefined : groupOfNode(n));
const movable = (n: TreeNode) => !!memberOf(n);

const focusKey = ref<string>();
const list = ref<HTMLElement>();

function toggle(node: TreeNode, open = !isOpen(node.key)) {
  if (filtering.value) return;
  setOpen(node.key, open);
}

function activate(node: TreeNode) {
  focusKey.value = node.key;
  if (node.kind === "pane" && node.pane) openPane(node.pane.ref);
  else if (node.children.length || node.kind === "group") toggle(node);
}

// ---------------------------------------------------------------- groups: make, rename, colour, delete

const dialog = ref<{ member?: Member; label?: string } | null>(null);
const editing = ref<string | null>(null);

function newGroup(member?: Member, label?: string) {
  dialog.value = { member, label };
}

async function made(value: { name: string; color: string }) {
  const pending = dialog.value;
  dialog.value = null;
  const group = await createGroup(value.name, value.color);
  if (!group) return;
  setOpen(`g:${group.id}`, true);
  if (pending?.member) await moveMember(pending.member, group.id);
}

async function rename(g: Group, name: string) {
  editing.value = null;
  await updateGroup(g.id, { name });
}

async function removeGroup(g: Group) {
  const ok = await confirmAsk({
    eyebrow: "Groups",
    title: `Delete ${g.name}?`,
    body: "Only the group goes. Its agents and machine users stay in the fleet, untouched and running.",
    confirm: "Delete group",
    danger: true,
  });
  if (ok) await deleteGroup(g.id);
}

function shiftGroup(g: Group, by: -1 | 1) {
  const ids = groups.value.map((x) => x.id);
  const i = ids.indexOf(g.id);
  const j = i + by;
  if (i < 0 || j < 0 || j >= ids.length) return;
  [ids[i], ids[j]] = [ids[j], ids[i]];
  void reorderGroups(ids);
}

// ---------------------------------------------------------------- menus (also the keyboard and touch way to group)

const menu = ref<{ x: number; y: number; title: string; items: MenuItem[] } | null>(null);

function memberMenu(node: TreeNode, at: DOMRect) {
  const m = memberOf(node);
  if (!m) return;
  const current = groupOf.value.get(memberKey(m));
  const items: MenuItem[] = groups.value.map((g) => ({
    label: g.name,
    swatch: colorHex(g.color) ?? "",
    checked: g.id === current?.id,
    run: () => void (g.id !== current?.id && moveMember(m, g.id)),
  }));
  items.push({ label: "New group…", icon: FolderPlus, rule: items.length > 0, run: () => newGroup(m, node.label) });
  if (current) {
    const i = current.members.findIndex((x) => memberKey(x) === memberKey(m));
    items.push(
      { label: "Move up", icon: ArrowUp, rule: true, disabled: i <= 0, run: () => void moveMember(m, current.id, i - 1) },
      {
        label: "Move down",
        icon: ArrowDown,
        disabled: i < 0 || i >= current.members.length - 1,
        run: () => void moveMember(m, current.id, i + 1),
      },
      { label: `Remove from ${current.name}`, icon: LogOut, rule: true, run: () => void moveMember(m, null) },
    );
  }
  menu.value = { x: at.left, y: at.bottom + 4, title: `Move ${node.label} to`, items };
}

function groupMenu(node: TreeNode, at: DOMRect) {
  const g = node.group;
  if (!g) return;
  const i = groups.value.findIndex((x) => x.id === g.id);
  const items: MenuItem[] = [
    { label: "Rename", icon: Pencil, run: () => (editing.value = g.id) },
    { label: "No colour", swatch: "", checked: !colorHex(g.color), rule: true, run: () => void updateGroup(g.id, { color: "" }) },
    ...GROUP_COLORS.map((c) => ({
      label: c.label,
      swatch: c.hex,
      checked: g.color === c.id,
      run: () => void updateGroup(g.id, { color: c.id }),
    })),
    { label: "Move up", icon: ArrowUp, rule: true, disabled: i <= 0, run: () => shiftGroup(g, -1) },
    { label: "Move down", icon: ArrowDown, disabled: i >= groups.value.length - 1, run: () => shiftGroup(g, 1) },
    { label: "Delete group…", icon: Trash2, danger: true, rule: true, run: () => void removeGroup(g) },
  ];
  menu.value = { x: at.right - 220, y: at.bottom + 4, title: g.name, items };
}

function rowMenu(node: TreeNode, at: DOMRect) {
  if (node.kind === "group") groupMenu(node, at);
  else memberMenu(node, at);
}

// ---------------------------------------------------------------- drag and drop

type Drag =
  | { kind: "member"; member: Member; from?: string; fromIndex?: number; key: string }
  | { kind: "group"; id: string; key: string };
type DropAt =
  | { kind: "row"; key: string; where: "into" | "before" | "after" }
  | { kind: "ungroup" }
  | { kind: "empty"; group: string }
  | { kind: "new" };

const drag = ref<Drag | null>(null);
const dropAt = ref<DropAt | null>(null);

const rowDrop = (key: string) =>
  dropAt.value?.kind === "row" && dropAt.value.key === key ? dropAt.value.where : undefined;

function rowOf(target: EventTarget | null): { row: Row; el: HTMLElement } | undefined {
  const el = (target as HTMLElement | null)?.closest?.<HTMLElement>("[data-key]");
  const row = el ? rowByKey.value.get(el.dataset.key ?? "") : undefined;
  return row && el ? { row, el } : undefined;
}

function onDragStart(e: DragEvent) {
  const hit = rowOf(e.target);
  if (!hit || !e.dataTransfer) return;
  const n = hit.row.node;
  if (n.kind === "group" && n.group) drag.value = { kind: "group", id: n.group.id, key: n.key };
  else {
    const m = memberOf(n);
    if (!m) return e.preventDefault();
    const g = groupOf.value.get(memberKey(m));
    const fromIndex = g?.members.findIndex((x) => memberKey(x) === memberKey(m));
    drag.value = { kind: "member", member: m, from: g?.id, fromIndex, key: n.key };
  }
  e.dataTransfer.effectAllowed = "move";
  e.dataTransfer.setData("application/x-ember-fleet", JSON.stringify(drag.value));
  e.dataTransfer.setData("text/plain", n.label);
  menu.value = null;
}

const half = (e: DragEvent, el: Element) => {
  const r = el.getBoundingClientRect();
  return e.clientY < r.top + r.height / 2 ? "before" : "after";
};

function targetOf(e: DragEvent): DropAt | null {
  const d = drag.value;
  const t = e.target as HTMLElement | null;
  if (!d || !t?.closest) return null;
  if (d.kind === "member") {
    if (t.closest("[data-drop='new']")) return { kind: "new" };
    if (t.closest("[data-drop='ungroup'], .machines-section")) return d.from ? { kind: "ungroup" } : null;
    const empty = t.closest<HTMLElement>("[data-drop-group]");
    if (empty?.dataset.dropGroup) return d.from === empty.dataset.dropGroup ? null : { kind: "empty", group: empty.dataset.dropGroup };
  }
  const hit = rowOf(t);
  if (!hit) return null;
  const n = hit.row.node;
  if (d.kind === "group") {
    if (n.kind !== "group" || n.group?.id === d.id) return null;
    return { kind: "row", key: n.key, where: half(e, hit.el) };
  }
  if (n.key === d.key) return null;
  if (n.kind === "group") return d.from === n.group?.id ? null : { kind: "row", key: n.key, where: "into" };
  if (n.inGroup && n.member) return { kind: "row", key: n.key, where: half(e, hit.el) };
  if (n.inGroup) return d.from === n.inGroup.id ? null : { kind: "row", key: `g:${n.inGroup.id}`, where: "into" };
  return null;
}

function onDragOver(e: DragEvent) {
  if (!drag.value) return; // files and other drags are not ours
  const at = targetOf(e);
  dropAt.value = at;
  if (at && e.dataTransfer) {
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
  }
}

function onDragLeave(e: DragEvent) {
  if (!list.value?.contains(e.relatedTarget as Node | null)) dropAt.value = null;
}

function endDrag() {
  drag.value = null;
  dropAt.value = null;
}

function onDrop(e: DragEvent) {
  const d = drag.value;
  const at = dropAt.value ?? targetOf(e);
  endDrag();
  if (!d || !at) return;
  e.preventDefault();
  if (d.kind === "member") {
    if (at.kind === "ungroup") return void moveMember(d.member, null);
    if (at.kind === "empty") return void moveMember(d.member, at.group);
    if (at.kind === "new") return newGroup(d.member);
  }
  if (at.kind !== "row") return;
  const n = rowByKey.value.get(at.key)?.node;
  if (!n) return;
  if (d.kind === "group") {
    const ids = groups.value.map((g) => g.id).filter((id) => id !== d.id);
    let i = ids.indexOf(n.group?.id ?? "");
    if (i < 0) return;
    if (at.where === "after") i++;
    ids.splice(i, 0, d.id);
    return void reorderGroups(ids);
  }
  if (at.where === "into" && n.group) return void moveMember(d.member, n.group.id);
  const g = n.inGroup;
  if (!g || n.memberIndex === undefined) return;
  let index = n.memberIndex + (at.where === "after" ? 1 : 0);
  // the index counts the group's members without the one that moves
  if (d.from === g.id && d.fromIndex !== undefined && d.fromIndex >= 0) {
    if (d.fromIndex < index) index--;
    if (d.fromIndex === index) return;
  }
  void moveMember(d.member, g.id, index);
}

// ---------------------------------------------------------------- keyboard

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
  const el = row ? list.value?.querySelector<HTMLElement>(`[data-key="${CSS.escape(row.node.key)}"]`) : null;
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
  else if (row && el && (e.key === "m" || e.key === "M" || e.key === "ContextMenu" || (e.key === "F10" && e.shiftKey))) {
    if (row.node.kind === "group" || movable(row.node)) rowMenu(row.node, el.getBoundingClientRect());
  } else if (e.key === "F2" && row?.node.group) editing.value = row.node.group.id;
  else return;
  e.preventDefault();
}

function focusFirst() {
  focusKey.value ??= rows.value.find((r) => isSelected(r.node))?.node.key ?? rows.value[0]?.node.key;
}

/** A DOM id per row, for aria-activedescendant. */
const rowId = (key: string) => `row-${key.replace(/[^a-zA-Z0-9_-]/g, (c) => `_${c.charCodeAt(0).toString(16)}`)}`;

// Bring the open pane into view when it is chosen from elsewhere (Needs you, a notification).
watch(selectedKey, (key) => {
  if (!key) return;
  void nextTick(() => {
    const row = rows.value.find((r) => isSelected(r.node));
    if (!row) return;
    focusKey.value = row.node.key; // the keyboard carries on from the open pane
    list.value?.querySelector(`[data-key="${CSS.escape(row.node.key)}"]`)?.scrollIntoView({ block: "nearest" });
  });
});
</script>

<template>
  <aside class="sidebar" aria-label="Fleet">
    <NeedsYou />
    <header class="tree-head">
      <span><Network :size="15" />AGENTS &amp; MACHINES</span>
      <button class="icon-button" title="Add a machine" aria-label="Add a machine" @click="app.adding = true">
        <Plus :size="17" />
      </button>
    </header>
    <div v-if="machines.length || groups.length" class="tree-tools">
      <div class="tree-filter">
        <Search :size="14" />
        <input
          v-model="app.filter"
          name="filter"
          type="search"
          placeholder="Filter groups, machines, agents"
          aria-label="Filter the fleet"
          spellcheck="false"
          autocomplete="off"
          @keydown.esc="app.filter = ''"
        />
        <button v-if="app.filter" class="icon-button" aria-label="Clear filter" @click="app.filter = ''">
          <X :size="14" />
        </button>
      </div>
      <div class="tree-switches">
        <div class="view-toggle" role="group" aria-label="Arrange by">
          <button :aria-pressed="view === 'groups'" @click="view = 'groups'">Groups</button>
          <button :aria-pressed="view === 'machines'" @click="view = 'machines'">Machines</button>
        </div>
        <button
          v-if="shellCount"
          class="shell-toggle"
          :aria-pressed="shells"
          :title="shells ? 'Hide plain shells' : 'Show plain shells too'"
          @click="shells = !shells"
        >
          <SquareTerminal :size="14" />{{ shells ? "Shells shown" : `${shellCount} shell${shellCount === 1 ? "" : "s"} hidden` }}
        </button>
      </div>
    </div>
    <div
      ref="list"
      class="tree-list"
      :class="{ 'drag-on': !!drag }"
      role="tree"
      tabindex="0"
      aria-label="Groups, machines, users, workspaces and panes"
      :aria-activedescendant="focusKey ? rowId(focusKey) : undefined"
      @keydown="keydown"
      @focus="focusFirst"
      @dragstart="onDragStart"
      @dragover="onDragOver"
      @dragleave="onDragLeave"
      @drop="onDrop"
      @dragend="endDrag"
    >
      <section v-if="byGroup" class="groups-section" role="group" aria-label="Groups">
        <div class="section-head">
          <span>Groups</span>
          <button
            class="section-add"
            :class="{ drop: dropAt?.kind === 'new' }"
            data-drop="new"
            title="New group (or drop an agent here to start one with it)"
            @click="newGroup()"
          >
            <Plus :size="14" />New group
          </button>
        </div>
        <template v-for="row in groupRows" :key="row.node.key">
          <GroupRow
            v-if="row.node.kind === 'group'"
            :id="rowId(row.node.key)"
            :node="row.node"
            :open="openRow(row.node)"
            :focused="focusKey === row.node.key"
            :drop="rowDrop(row.node.key)"
            :editing="editing === row.node.group?.id"
            :dragging="drag?.key === row.node.key"
            @toggle="activate(row.node)"
            @menu="groupMenu(row.node, $event)"
            @edit="editing = row.node.group!.id"
            @rename="rename(row.node.group!, $event)"
            @stop-editing="editing = null"
          />
          <FleetRow
            v-else
            :id="rowId(row.node.key)"
            :node="row.node"
            :depth="row.depth"
            :open="openRow(row.node)"
            :selected="isSelected(row.node)"
            :focused="focusKey === row.node.key"
            :movable="movable(row.node)"
            :drop="rowDrop(row.node.key)"
            :dragging="drag?.key === row.node.key"
            @toggle="toggle(row.node)"
            @activate="activate(row.node)"
            @menu="memberMenu(row.node, $event)"
            @remove="row.node.member && moveMember(row.node.member, null)"
          />
          <div
            v-if="row.node.kind === 'group' && !row.node.children.length && openRow(row.node)"
            class="group-empty"
            :class="{ drop: dropAt?.kind === 'empty' && dropAt.group === row.node.group?.id }"
            :data-drop-group="row.node.group?.id"
          >
            Drag agents here, or use their ⋯ menu.
          </div>
        </template>
        <p v-if="!groups.length" class="hint groups-hint">
          Cluster agents and machine users into groups, like Physics, Coding or Burst: drag their rows here, or
          use “Move to group…” in a row’s ⋯ menu.
        </p>
        <p v-else-if="filtering && !groupRows.length" class="hint groups-hint">No group matches.</p>
        <div
          v-if="drag?.kind === 'member' && drag.from"
          class="ungroup-zone"
          :class="{ drop: dropAt?.kind === 'ungroup' }"
          data-drop="ungroup"
        >
          <LogOut :size="14" />Drop here to take it out of its group
        </div>
      </section>

      <section
        class="machines-section"
        :class="{ 'drop-ungroup': dropAt?.kind === 'ungroup' }"
        role="group"
        :aria-label="byGroup && groups.length ? 'Not in a group' : 'Machines'"
      >
        <div class="section-head"><span>{{ byGroup && groups.length ? "Not in a group" : "Machines" }}</span></div>
        <FleetRow
          v-for="row in machineRows"
          :id="rowId(row.node.key)"
          :key="row.node.key"
          :node="row.node"
          :depth="row.depth"
          :open="openRow(row.node)"
          :selected="isSelected(row.node)"
          :focused="focusKey === row.node.key"
          :movable="movable(row.node)"
          :tag="tagOf(row.node)"
          :dragging="drag?.key === row.node.key"
          @toggle="toggle(row.node)"
          @activate="activate(row.node)"
          @menu="memberMenu(row.node, $event)"
        />
        <p v-if="!machines.length" class="hint tree-empty">
          No machines yet.
          <button class="text-button" @click="app.adding = true">Add your first machine</button>
        </p>
        <p v-else-if="!machineRows.length && filtering" class="hint tree-empty">Nothing matches “{{ app.filter }}”.</p>
        <p v-else-if="!machineRows.length" class="hint tree-empty">Every agent is in a group.</p>
      </section>
    </div>

    <RowMenu v-if="menu" :items="menu.items" :x="menu.x" :y="menu.y" :title="menu.title" @close="menu = null" />
    <GroupDialog
      v-if="dialog"
      :title="dialog.label ? `New group with ${dialog.label}` : 'New group'"
      confirm="Create group"
      @done="made"
      @close="dialog = null"
    />
  </aside>
</template>
