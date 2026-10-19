<script setup lang="ts">
// One row of the fleet tree: a machine, an OS user's dock, a herdr session, a
// workspace, a tab, or a pane with its agent.
import { computed } from "vue";
import { ChevronDown, ChevronRight, Folder, FolderOpen, Layers, PanelTop, UserRound } from "@lucide/vue";
import DeviceIcon from "./DeviceIcon.vue";
import HarnessIcon from "./HarnessIcon.vue";
import StatePill from "./StatePill.vue";
import { ago, plural, shortPath, type TreeNode } from "../lib/fleet";
import { now } from "../lib/store";

const props = defineProps<{
  node: TreeNode;
  depth: number;
  open: boolean;
  selected: boolean;
  focused: boolean;
}>();
const emit = defineEmits<{ toggle: []; activate: [] }>();

const n = computed(() => props.node);
const hasChildren = computed(() => n.value.children.length > 0);
const pane = computed(() => n.value.pane);
/** Rolled-up states glow only while their rows are folded away. */
const folded = computed(() => hasChildren.value && !props.open);

const dockMeta = computed(() => {
  const d = n.value.dock;
  if (!d) return "";
  if (!d.online) return `offline · last seen ${ago(d.lastSeen, now.value)}`;
  if (n.value.error) return n.value.error;
  const c = n.value.counts;
  const parts = [c.agents ? plural(c.agents, "agent") : "no agents"];
  if (c.shells) parts.push(plural(c.shells, "shell"));
  if (d.herdr) parts.push(`herdr ${d.herdr}`);
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

const dot = computed(() => (n.value.online ? (n.value.error ? "err" : "on") : "off"));

const title = computed(() => {
  const node = n.value;
  if (node.kind === "pane" && node.pane) {
    const p = node.pane;
    return [p.name + (p.harness ? ` (${p.harness}, ${p.status})` : " (shell)"), p.title, p.cwd, p.ref.pane]
      .filter(Boolean)
      .join("\n");
  }
  if (node.kind === "machine" || node.kind === "dock") {
    const d = node.only ?? (node.kind === "dock" ? node.dock : undefined);
    if (d) return [`${node.host} · ${d.user}`, d.name !== node.host ? d.name : "", `${d.os}/${d.arch} · role ${d.role}`, node.error ?? ""].filter(Boolean).join("\n");
  }
  return node.error ? `${node.label}\n${node.error}` : node.label;
});
</script>

<template>
  <div
    class="tree-row"
    :class="[
      n.kind,
      { sel: selected, focus: focused, offline: !n.online, shell: n.kind === 'pane' && !pane?.harness },
    ]"
    :style="{ '--depth': depth }"
    role="treeitem"
    :aria-level="depth + 1"
    :aria-expanded="hasChildren ? open : undefined"
    :aria-selected="selected"
    :data-key="n.key"
    :title="title"
    @click="emit('activate')"
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
          ><span v-if="n.only && n.only.role !== 'owner'" class="badge">{{ n.only.role }}</span></span
        >
        <span class="meta" :class="{ err: n.online && !!n.error }">{{ machineMeta }}</span>
      </span>
    </template>

    <!-- dock: one OS user's herdr on a machine with several -->
    <template v-else-if="n.kind === 'dock' && n.dock">
      <span class="mark"><UserRound :size="17" /><i class="dot" :class="dot" /></span>
      <span class="two">
        <span
          ><b class="name">{{ n.label }}</b
          ><span v-if="n.dock.role !== 'owner'" class="badge">{{ n.dock.role }}</span></span
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

    <template v-else-if="pane">
      <HarnessIcon :harness="pane.harness" />
      <span class="two">
        <span><span class="name">{{ pane.name }}</span></span>
        <span class="sub"
          >{{ pane.title || (pane.harness ? "" : "shell")
          }}<template v-if="pane.title && pane.cwd"> · </template
          ><span v-if="pane.cwd" class="cwd">{{ shortPath(pane.cwd) }}</span></span
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
      <i
        v-else-if="n.status && n.online"
        class="sdot"
        :class="[n.status, { beacon: folded && (n.status === 'working' || n.status === 'blocked') }]"
        :title="n.status"
      />
    </span>
  </div>
</template>
