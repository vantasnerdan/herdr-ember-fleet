<script setup lang="ts">
// One row of the fleet tree: a machine, an OS user's dock, a herdr session, a
// workspace, a tab, or a pane with its agent; inside a group, also a member that is
// not running any more. Agents and machine users can be dragged into groups; the "⋯"
// button (or a right-click) offers the same as a menu.
import { computed } from "vue";
import {
  ChevronDown,
  ChevronRight,
  CircleOff,
  Ellipsis,
  Folder,
  FolderOpen,
  Layers,
  PanelTop,
  UserRound,
  X,
} from "@lucide/vue";
import DeviceIcon from "./DeviceIcon.vue";
import HarnessIcon from "./HarnessIcon.vue";
import StatePill from "./StatePill.vue";
import { ago, plural, shortPath, type TreeNode } from "../lib/fleet";
import { colorHex } from "../lib/groups";
import { now } from "../lib/store";
import type { Group } from "../lib/types";

const props = defineProps<{
  node: TreeNode;
  depth: number;
  open: boolean;
  selected: boolean;
  focused: boolean;
  /** It can be dragged into a group (and has a "Move to group…" menu). */
  movable?: boolean;
  /** The group it is in, shown as a tag in the machine tree. */
  tag?: Group;
  drop?: "into" | "before" | "after";
  dragging?: boolean;
}>();
const emit = defineEmits<{ toggle: []; activate: []; menu: [anchor: DOMRect]; remove: [] }>();

const n = computed(() => props.node);
const hasChildren = computed(() => n.value.children.length > 0);
const pane = computed(() => n.value.pane);
/** Rolled-up states glow only while their rows are folded away. */
const folded = computed(() => hasChildren.value && !props.open);
const tagHex = computed(() => colorHex(props.tag?.color));

const dockMeta = computed(() => {
  const d = n.value.dock;
  if (!d) return "";
  if (!d.online) return `offline · last seen ${ago(d.lastSeen, now.value)}`;
  if (n.value.error) return n.value.error;
  const c = n.value.counts;
  const parts = [c.agents ? plural(c.agents, "agent") : "no agents"];
  if (c.shells) parts.push(plural(c.shells, "shell"));
  // what's running matters most; note the installed version when it's newer
  const run = d.herdrServer || d.herdr;
  if (run) parts.push(d.herdrServer && d.herdr && d.herdr !== d.herdrServer ? `herdr ${run} (${d.herdr} installed)` : `herdr ${run}`);
  return parts.join(" · ");
});

const machineMeta = computed(() => {
  if (n.value.only) return dockMeta.value;
  const docks = n.value.children.map((c) => c.dock).filter((d) => !!d);
  const on = docks.filter((d) => d.online).length;
  const c = n.value.counts;
  if (!on) {
    const last = Math.max(...docks.map((d) => d.lastSeen || 0));
    return `offline · last seen ${ago(last, now.value)}`;
  }
  return [`${on} of ${plural(docks.length, "user")} online`, plural(c.agents, "agent")].join(" · ");
});

/** Under an agent's name: what its screen says, else its folder (already in the name when names repeat). */
const subText = computed(() => {
  const p = pane.value;
  if (!p) return "";
  const cwd = p.cwd ? shortPath(p.cwd) : "";
  if (p.label !== p.name) return n.value.context ? "" : cwd;
  if (n.value.context) return p.title || (p.harness ? "" : "shell");
  return [p.title || (p.harness ? "" : "shell"), cwd].filter(Boolean).join(" · ");
});
const dot = computed(() => (n.value.online ? (n.value.error ? "err" : "on") : "off"));

const title = computed(() => {
  const node = n.value;
  if (node.kind === "pane" && node.pane) {
    const p = node.pane;
    return [
      p.label + (p.harness ? ` (${p.harness}, ${p.status})` : " (shell)"),
      `${p.dock.user}@${p.host}`,
      p.title,
      p.cwd,
      p.ref.pane,
      props.tag ? `In group ${props.tag.name}` : "",
    ]
      .filter(Boolean)
      .join("\n");
  }
  if (node.kind === "machine" || node.kind === "dock") {
    const d = node.only ?? (node.kind === "dock" ? node.dock : undefined);
    if (d)
      return [
        `${node.host} · ${d.user}`,
        d.name !== node.host ? d.name : "",
        `${d.os}/${d.arch} · role ${d.role}`,
        node.error ?? "",
        props.tag ? `In group ${props.tag.name}` : "",
      ]
        .filter(Boolean)
        .join("\n");
  }
  if (node.kind === "missing") return `${node.label}\n${node.context}\nIt comes back here when it runs again.`;
  return node.error ? `${node.label}\n${node.error}` : node.label;
});

function openMenu(e: MouseEvent) {
  emit("menu", (e.currentTarget as HTMLElement).getBoundingClientRect());
}
</script>

<template>
  <div
    class="tree-row"
    :class="[
      n.kind,
      drop ? `drop-${drop}` : '',
      {
        sel: selected,
        focus: focused,
        offline: !n.online,
        shell: n.kind === 'pane' && !pane?.harness,
        movable,
        dragging,
        grouped: !!n.inGroup,
      },
    ]"
    :style="{ '--depth': depth }"
    role="treeitem"
    :aria-level="depth + 1"
    :aria-expanded="hasChildren ? open : undefined"
    :aria-selected="selected"
    :data-key="n.key"
    :title="title"
    :draggable="movable ? 'true' : undefined"
    @click="emit('activate')"
    @contextmenu="movable ? ($event.preventDefault(), emit('menu', ($event.currentTarget as HTMLElement).getBoundingClientRect())) : undefined"
  >
    <button
      v-if="hasChildren"
      class="chev"
      tabindex="-1"
      :aria-label="open ? `Collapse ${n.label}` : `Expand ${n.label}`"
      @click.stop="emit('toggle')"
    >
      <ChevronDown v-if="open" :size="16" /><ChevronRight v-else :size="16" />
    </button>
    <span v-else class="chev" />

    <!-- machine: the computer, and its user when it has only one -->
    <template v-if="n.kind === 'machine'">
      <span class="mark"><DeviceIcon :os="(n.only ?? n.children[0]?.dock)?.os" :size="18" /><i class="dot" :class="dot" /></span>
      <span class="two">
        <span
          ><b class="name">{{ n.label }}</b
          ><span v-if="n.only" class="badge" :title="`OS user ${n.only.user}`">{{ n.only.user }}</span
          ><span v-if="n.only && n.only.role !== 'owner'" class="badge">{{ n.only.role }}</span
          ><span v-if="tag" class="group-tag" :style="tagHex ? { '--group': tagHex } : undefined">{{ tag.name }}</span></span
        >
        <span class="meta" :class="{ err: n.online && !!n.error }">{{ machineMeta }}</span>
      </span>
    </template>

    <!-- dock: one OS user's herdr on a machine with several (or, in a group, user@host) -->
    <template v-else-if="n.kind === 'dock' && n.dock">
      <span class="mark"><UserRound :size="17" /><i class="dot" :class="dot" /></span>
      <span class="two">
        <span
          ><b class="name">{{ n.label }}</b
          ><span v-if="n.dock.role !== 'owner'" class="badge">{{ n.dock.role }}</span
          ><span v-if="tag" class="group-tag" :style="tagHex ? { '--group': tagHex } : undefined">{{ tag.name }}</span></span
        >
        <span class="meta" :class="{ err: n.online && !!n.error }">{{ dockMeta }}</span>
      </span>
    </template>

    <template v-else-if="n.kind === 'session'">
      <Layers class="ico" :size="16" />
      <span class="name">{{ n.label }}</span>
      <span v-if="n.error" class="meta err">{{ n.error }}</span>
      <span v-else-if="!n.online" class="meta">offline</span>
    </template>

    <template v-else-if="n.kind === 'workspace'">
      <FolderOpen v-if="open && hasChildren" class="ico" :size="16" /><Folder v-else class="ico" :size="16" />
      <span class="name">{{ n.label }}</span>
      <span v-if="!hasChildren" class="meta">empty</span>
    </template>

    <template v-else-if="n.kind === 'tab'">
      <PanelTop class="ico" :size="15" />
      <span class="name">{{ n.label }}</span>
    </template>

    <!-- a member whose pane or dock is gone; it stays, since herdr restores pane IDs -->
    <template v-else-if="n.kind === 'missing'">
      <CircleOff class="ico" :size="16" />
      <span class="two">
        <span><span class="name">{{ n.label }}</span></span>
        <span class="sub">{{ n.context }}</span>
      </span>
    </template>

    <template v-else-if="pane">
      <HarnessIcon :harness="pane.harness" />
      <span class="two">
        <span
          ><span class="name">{{ pane.label }}</span
          ><span v-if="tag" class="group-tag" :style="tagHex ? { '--group': tagHex } : undefined">{{ tag.name }}</span></span
        >
        <span class="sub"
          ><span v-if="n.context" class="where">{{ n.context }}</span
          ><span v-if="subText" class="task">{{ subText }}</span></span
        >
      </span>
    </template>

    <span class="right">
      <template v-if="n.kind === 'pane'">
        <StatePill v-if="pane?.harness" :status="pane.status" :offline="!n.online" />
      </template>
      <template v-else-if="n.kind === 'machine' || n.kind === 'dock' || n.kind === 'session'">
        <span v-if="n.online && n.counts.blocked" class="count blocked" :title="`${n.counts.blocked} blocked`"
          ><i class="sdot blocked" :class="{ beacon: folded }" />{{ n.counts.blocked }}</span
        >
        <span v-if="n.online && n.counts.working" class="count working" :title="`${n.counts.working} working`"
          ><i class="sdot working" :class="{ beacon: folded }" />{{ n.counts.working }}</span
        >
      </template>
      <button
        v-else-if="n.kind === 'missing'"
        class="icon-button row-remove"
        :aria-label="`Remove ${n.label} from ${n.inGroup?.name ?? 'the group'}`"
        title="Remove from the group"
        @click.stop="emit('remove')"
      >
        <X :size="15" />
      </button>
      <i
        v-else-if="n.status && n.online"
        class="sdot"
        :class="[n.status, { beacon: folded && (n.status === 'working' || n.status === 'blocked') }]"
        :title="n.status"
      />
      <button
        v-if="movable"
        class="icon-button row-more"
        tabindex="-1"
        :aria-label="`Move ${n.label} to a group`"
        title="Move to group…"
        @click.stop="openMenu"
      >
        <Ellipsis :size="16" />
      </button>
    </span>
  </div>
</template>
