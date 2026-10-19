<script setup lang="ts">
import { computed, onMounted, ref, watchEffect } from "vue";
import { RefreshCw } from "@lucide/vue";
import TopBar from "./components/TopBar.vue";
import SigninRequests from "./components/SigninRequests.vue";
import FleetSidebar from "./components/FleetSidebar.vue";
import PaneView from "./components/PaneView.vue";
import EmptyFleet from "./components/EmptyFleet.vue";
import Overview from "./components/Overview.vue";
import AgentsView from "./components/AgentsView.vue";
import MachinesView from "./components/MachinesView.vue";
import MachineView from "./components/MachineView.vue";
import SettingsView from "./components/SettingsView.vue";
import CommandPalette from "./components/CommandPalette.vue";
import Modal from "./components/Modal.vue";
import AddMachine from "./components/AddMachine.vue";
import SignIn from "./components/SignIn.vue";
import AskDialog from "./components/AskDialog.vue";
import Toasts from "./components/Toasts.vue";
import { app, blocked, boot, machines, page, save, selectedKey, sidebarWidth } from "./lib/store";
import { watchAlerts } from "./lib/alerts";

watchEffect(() => {
  const name = app.me?.fleet?.trim();
  const title = name && !/^(herdr )?ember fleet$/i.test(name) ? `${name} · Herdr Ember Fleet` : "Herdr Ember Fleet";
  document.title = `${blocked.value.length ? `(${blocked.value.length}) ` : ""}${title}`;
});

onMounted(() => {
  watchAlerts();
  void boot();
});

/** Agents, Machines and Settings are full pages; the overview and panes keep the fleet tree. */
const fullPage = computed(() => ["agents", "machines", "machine", "settings"].includes(page.value.name));
const paneOpen = computed(() => page.value.name === "pane");

// Ctrl/Cmd-K opens search anywhere, except Ctrl-K while typing into a terminal you control.
window.addEventListener(
  "keydown",
  (e) => {
    if (app.phase !== "ready" || e.key.toLowerCase() !== "k" || e.shiftKey || e.altKey) return;
    if (!(e.metaKey || e.ctrlKey)) return;
    const inTerminal = (e.target as HTMLElement | null)?.closest?.(".xterm");
    if (e.ctrlKey && !e.metaKey && app.typing && inTerminal) return;
    e.preventDefault();
    e.stopPropagation();
    app.palette = !app.palette;
  },
  true,
);

const reload = () => location.reload();

const dragging = ref(false);
function resize(e: PointerEvent) {
  const start = e.clientX;
  const from = sidebarWidth.value;
  dragging.value = true;
  const move = (ev: PointerEvent) => {
    sidebarWidth.value = Math.max(260, Math.min(620, from + ev.clientX - start));
  };
  const up = () => {
    dragging.value = false;
    save("ember.sidebar", sidebarWidth.value);
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", up);
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", up);
}
</script>

<template>
  <div
    v-if="app.phase === 'ready'"
    class="app-shell"
    :class="{ 'has-pane': paneOpen, 'full-page': fullPage, empty: !machines.length }"
    :style="{ '--sidebar-w': `${sidebarWidth}px` }"
  >
    <TopBar />
    <SigninRequests />
    <div v-if="app.newVersion" class="page-stale" role="status">
      <RefreshCw :size="16" /><span>The fleet hub was updated to {{ app.newVersion }}. Reload for the new page.</span>
      <button class="primary small" @click="reload">Reload</button>
    </div>
    <main v-if="fullPage" class="page-main">
      <AgentsView v-if="page.name === 'agents'" />
      <MachinesView v-else-if="page.name === 'machines'" />
      <MachineView v-else-if="page.name === 'machine'" :key="page.host" />
      <SettingsView v-else-if="page.name === 'settings'" />
    </main>
    <main v-else class="fleet-main">
      <FleetSidebar />
      <div
        class="resizer"
        :class="{ dragging }"
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize the fleet tree"
        tabindex="0"
        :aria-valuenow="sidebarWidth"
        :aria-valuemin="260"
        :aria-valuemax="620"
        @keydown.left.prevent="sidebarWidth = Math.max(260, sidebarWidth - 20); save('ember.sidebar', sidebarWidth)"
        @keydown.right.prevent="sidebarWidth = Math.min(620, sidebarWidth + 20); save('ember.sidebar', sidebarWidth)"
        @pointerdown.prevent="resize"
      />
      <section class="stage" aria-label="Agent workspace">
        <PaneView v-if="paneOpen && selectedKey" :key="selectedKey" :pane-key="selectedKey" />
        <div v-else-if="paneOpen" class="pane-missing"><p class="hint">Finding that agent…</p></div>
        <EmptyFleet v-else-if="!machines.length" />
        <Overview v-else />
      </section>
    </main>
    <Modal v-if="app.adding" eyebrow="Expand your workspace" title="Add a machine" wide @close="app.adding = false">
      <AddMachine @done="app.adding = false" @back="app.adding = false" />
    </Modal>
    <CommandPalette />
  </div>
  <SignIn v-else />
  <AskDialog />
  <Toasts />
</template>
