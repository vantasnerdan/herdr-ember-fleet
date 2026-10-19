// Groups: the viewer's own clusters of machine users and agents ("Physics", "Coding",
// "Burst"). The hub keeps them. This file derives their part of the sidebar and changes
// them optimistically: the page moves at once, and goes back if the hub says no.
//
// A member that is gone (its pane closed, its dock removed) stays in its group, shown as
// "not running": herdr gives pane IDs back when it restores a session.

import { computed } from "vue";
import { api, errorText } from "./api";
import { container, dockNode, paneNode, prune, refKey, type Counts, type PaneInfo, type TreeNode } from "./fleet";
import { groups, machines, notify, paneIndex, tree, treeView } from "./store";
import type { Dock, Group, GroupColor, Member } from "./types";

// ---------------------------------------------------------------- colours

export const GROUP_COLORS: { id: Exclude<GroupColor, "">; label: string; hex: string }[] = [
  { id: "ember", label: "Ember", hex: "#f47853" },
  { id: "amber", label: "Amber", hex: "#f2b866" },
  { id: "rose", label: "Rose", hex: "#e48aa8" },
  { id: "teal", label: "Teal", hex: "#4fd1c5" },
  { id: "sky", label: "Sky", hex: "#5cc8f0" },
  { id: "violet", label: "Violet", hex: "#9db8ff" },
  { id: "slate", label: "Slate", hex: "#7c8baa" },
];

export const colorHex = (c?: string) => GROUP_COLORS.find((x) => x.id === c)?.hex;

export const NAME_MAX = 40;
export const validName = (name: string) => name.trim().length >= 1 && name.trim().length <= NAME_MAX;

// ---------------------------------------------------------------- members

export const memberKey = (m: Member) =>
  m.kind === "dock" ? `d:${m.dock}` : `p:${refKey({ dock: m.dock, session: m.session, pane: m.pane })}`;

/** The member a row stands for, if it can be grouped: an agent's pane, or a whole machine user. */
export function memberOf(node: TreeNode): Member | undefined {
  if (node.member) return node.member;
  if (node.kind === "pane" && node.pane) {
    const { dock, session, pane } = node.pane.ref;
    return { kind: "pane", dock, session, pane };
  }
  if (node.kind === "dock" && node.dock) return { kind: "dock", dock: node.dock.id };
  if (node.kind === "machine" && node.only) return { kind: "dock", dock: node.only.id };
  return undefined;
}

/** memberKey → the group it is in. */
export const groupOf = computed(() => {
  const out = new Map<string, Group>();
  for (const g of groups.value) for (const m of g.members) out.set(memberKey(m), g);
  return out;
});

export function groupOfNode(node: TreeNode): Group | undefined {
  const m = memberOf(node);
  return m ? groupOf.value.get(memberKey(m)) : undefined;
}

const docksById = computed(() => {
  const out = new Map<string, { host: string; dock: Dock }>();
  for (const m of machines.value) for (const d of m.docks) out.set(d.id, { host: m.host, dock: d });
  return out;
});

// ---------------------------------------------------------------- the sidebar's groups

const none = (): Counts => ({ agents: 0, working: 0, blocked: 0, done: 0, idle: 0, shells: 0 });

/** A member's rows, keyed apart from the same rows in the machine tree, and told where they live. */
function within(node: TreeNode, g: Group, index: number, prefix: string): TreeNode {
  return {
    ...node,
    key: prefix + node.key,
    inGroup: g,
    memberIndex: index,
    children: node.children.map((c) => within(c, g, index, prefix)),
  };
}

function gone(g: Group, m: Member, index: number): TreeNode {
  const d = docksById.value.get(m.dock);
  const where = d ? `${d.dock.user}@${d.host}` : m.dock;
  const label = m.kind === "dock" ? where : `pane ${m.pane}`;
  const context =
    m.kind === "dock" ? "not running" : `${where}${m.session !== "default" ? ` · ${m.session}` : ""} · not running`;
  return {
    key: `g:${g.id}|x:${memberKey(m)}`,
    kind: "missing",
    label,
    children: [],
    online: false,
    counts: none(),
    text: `${label} ${context}`,
    host: d?.host ?? "",
    member: m,
    inGroup: g,
    memberIndex: index,
    context,
  };
}

/** A pane that is a group member in its own right (not just through its machine user). */
const placedOnItsOwn = (n: TreeNode) =>
  n.kind === "pane" && !!n.pane && groupOf.value.has(memberKey({ kind: "pane", ...n.pane.ref }));

export const groupTree = computed<TreeNode[]>(() =>
  groups.value.map((g) => {
    const prefix = `g:${g.id}|`;
    const children = g.members.map((m, i): TreeNode => {
      let node: TreeNode | undefined;
      if (m.kind === "dock") {
        const d = docksById.value.get(m.dock);
        // A pane placed in a group of its own shows there, not again under its machine user.
        if (d) node = prune([dockNode(d.host, d.dock)], placedOnItsOwn)[0];
      } else {
        const p = paneIndex.value.get(refKey(m));
        if (p) node = { ...paneNode(p), context: `${p.dock.user}@${p.host}` };
      }
      if (!node) return gone(g, m, i);
      return { ...within(node, g, i, prefix), member: m };
    });
    return container({
      key: `g:${g.id}`,
      kind: "group",
      label: g.name,
      children,
      online: true,
      text: g.name,
      host: "",
      group: g,
    });
  }),
);

// ---------------------------------------------------------------- changes

/**
 * Agents in the order the sidebar shows them (grouped first in the Groups view), for
 * stepping to the previous or next agent.
 */
export const agentOrder = computed<PaneInfo[]>(() => {
  const out: PaneInfo[] = [];
  const seen = new Set<string>();
  const walk = (nodes: TreeNode[]) => {
    for (const n of nodes) {
      const p = n.pane;
      if (n.kind === "pane" && p?.harness && !seen.has(p.key)) {
        seen.add(p.key);
        out.push(p);
      }
      walk(n.children);
    }
  };
  if (treeView.value === "groups") walk(groupTree.value);
  walk(tree.value);
  return out;
});

function moved(list: Group[], member: Member, groupId: string | null, index?: number): Group[] {
  const key = memberKey(member);
  const out = list.map((g) => ({ ...g, members: g.members.filter((m) => memberKey(m) !== key) }));
  const target = groupId ? out.find((g) => g.id === groupId) : undefined;
  if (target) {
    const i = index === undefined ? target.members.length : Math.max(0, Math.min(index, target.members.length));
    target.members = [...target.members.slice(0, i), member, ...target.members.slice(i)];
  }
  return out;
}

async function optimistic(next: Group[], call: () => Promise<{ groups?: Group[] } | unknown>, what: string) {
  const before = groups.value;
  groups.value = next;
  try {
    const r = (await call()) as { groups?: Group[] } | undefined;
    if (r?.groups) groups.value = r.groups;
    return true;
  } catch (e) {
    groups.value = before;
    notify(`${what} didn’t work: ${errorText(e)}`, "error");
    return false;
  }
}

/**
 * Puts a member into a group at an index (default: the end), taking it out of any other;
 * a null group takes it out of its group. The index counts the group's members without it.
 */
export function moveMember(member: Member, groupId: string | null, index?: number) {
  return optimistic(
    moved(groups.value, member, groupId, index),
    () => api.moveMember(member, groupId, index),
    groupId ? "Moving it" : "Taking it out of its group",
  );
}

export function reorderGroups(ids: string[]) {
  const byId = new Map(groups.value.map((g) => [g.id, g]));
  const next = ids.map((id) => byId.get(id)).filter((g): g is Group => !!g);
  return optimistic(next, () => api.reorderGroups(ids), "Reordering groups");
}

export async function createGroup(name: string, color: string): Promise<Group | undefined> {
  try {
    const { group } = await api.createGroup(name.trim(), color);
    if (!groups.value.some((g) => g.id === group.id)) groups.value = [...groups.value, group];
    return group;
  } catch (e) {
    notify(`Making the group didn’t work: ${errorText(e)}`, "error");
    return undefined;
  }
}

export async function updateGroup(id: string, patch: { name?: string; color?: string }) {
  const before = groups.value;
  groups.value = groups.value.map((g) => (g.id === id ? { ...g, ...patch } : g));
  try {
    const { group } = await api.updateGroup(id, patch);
    if (group) groups.value = groups.value.map((g) => (g.id === id ? group : g));
    return true;
  } catch (e) {
    groups.value = before;
    notify(`Changing the group didn’t work: ${errorText(e)}`, "error");
    return false;
  }
}

export function deleteGroup(id: string) {
  return optimistic(
    groups.value.filter((g) => g.id !== id),
    () => api.deleteGroup(id),
    "Deleting the group",
  );
}
