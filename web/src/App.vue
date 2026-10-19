<script setup lang="ts">
import { onMounted, ref } from "vue";
import TopBar from "./components/TopBar.vue";
import FleetSidebar from "./components/FleetSidebar.vue";
import PaneView from "./components/PaneView.vue";
import EmptyFleet from "./components/EmptyFleet.vue";
import Overview from "./components/Overview.vue";
import FleetDialog from "./components/FleetDialog.vue";
import SignIn from "./components/SignIn.vue";
import AskDialog from "./components/AskDialog.vue";
import Toasts from "./components/Toasts.vue";
import { app, boot, machines, save, selectedKey, sidebarWidth } from "./lib/store";
import { watchAlerts } from "./lib/alerts";

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
    :class="{ 'has-pane': !!selectedKey }"
    :style="{ '--sidebar-w': `${sidebarWidth}px` }"
  >
    <TopBar />
    <main class="fleet-main">
      <FleetSidebar />
      <div
        class="resizer"
        :class="{ dragging }"
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize the fleet tree"
        @pointerdown.prevent="resize"
      />
      <section class="stage">
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
