<script setup lang="ts">
import { onMounted, ref, watchEffect } from "vue";
import TopBar from "./components/TopBar.vue";
import SigninRequests from "./components/SigninRequests.vue";
import FleetSidebar from "./components/FleetSidebar.vue";
import PaneView from "./components/PaneView.vue";
import EmptyFleet from "./components/EmptyFleet.vue";
import Overview from "./components/Overview.vue";
import FleetDialog from "./components/FleetDialog.vue";
import SignIn from "./components/SignIn.vue";
import AskDialog from "./components/AskDialog.vue";
import Toasts from "./components/Toasts.vue";
import { app, blocked, boot, machines, save, selectedKey, sidebarWidth } from "./lib/store";
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
    :class="{ 'has-pane': !!selectedKey, empty: !machines.length }"
    :style="{ '--sidebar-w': `${sidebarWidth}px` }"
  >
    <TopBar />
    <SigninRequests />
    <main class="fleet-main">
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
        <PaneView v-if="selectedKey" :key="selectedKey" :pane-key="selectedKey" />
        <EmptyFleet v-else-if="!machines.length" />
        <Overview v-else />
      </section>
    </main>
    <FleetDialog v-if="app.settings" />
  </div>
  <SignIn v-else />
  <AskDialog />
  <Toasts />
</template>
