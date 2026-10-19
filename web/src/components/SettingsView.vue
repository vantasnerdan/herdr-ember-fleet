<script setup lang="ts">
// Settings as a page (#/settings/<section>): sections down the left, one at a time on
// the right. Machines have their own page; the nav links there.
import { computed } from "vue";
import {
  ArrowUpRight,
  Info,
  KeyRound,
  Link2,
  MonitorSmartphone,
  RefreshCw,
  Server,
  SlidersHorizontal,
  SquareTerminal,
  TriangleAlert,
} from "@lucide/vue";
import GeneralPanel from "./settings/GeneralPanel.vue";
import PinPanel from "./settings/PinPanel.vue";
import BrowsersPanel from "./settings/BrowsersPanel.vue";
import UpdatesPanel from "./UpdatesPanel.vue";
import LinksPanel from "./settings/LinksPanel.vue";
import TokensPanel from "./settings/TokensPanel.vue";
import AboutPanel from "./settings/AboutPanel.vue";
import DangerPanel from "./settings/DangerPanel.vue";
import { page, showMachines, showSettings } from "../lib/store";

const SECTIONS = [
  { id: "general", label: "General", icon: SlidersHorizontal, panel: GeneralPanel },
  { id: "signin", label: "Signing in", icon: KeyRound, panel: PinPanel },
  { id: "browsers", label: "Browsers", icon: MonitorSmartphone, panel: BrowsersPanel },
  { id: "updates", label: "Updates", icon: RefreshCw, panel: UpdatesPanel },
  { id: "links", label: "Join links", icon: Link2, panel: LinksPanel },
  { id: "cli", label: "Command line", icon: SquareTerminal, panel: TokensPanel },
  { id: "about", label: "About", icon: Info, panel: AboutPanel },
  { id: "danger", label: "Danger zone", icon: TriangleAlert, panel: DangerPanel },
] as const;

const current = computed(() => {
  const id = page.value.name === "settings" ? page.value.section : "general";
  return SECTIONS.find((s) => s.id === id) ?? SECTIONS[0];
});
</script>

<template>
  <div class="page settings-page">
    <div class="page-inner">
      <header class="page-head">
        <span class="eyebrow"><span class="accent-tick" />Settings</span>
        <h1>{{ current.label }}</h1>
      </header>
      <div class="settings-layout">
        <nav class="settings-nav" aria-label="Settings sections">
          <button
            v-for="s in SECTIONS"
            :key="s.id"
            :class="{ active: s.id === current.id, danger: s.id === 'danger' }"
            :aria-current="s.id === current.id ? 'page' : undefined"
            @click="showSettings(s.id)"
          >
            <component :is="s.icon" :size="16" /><span>{{ s.label }}</span>
          </button>
          <button class="nav-out" @click="showMachines">
            <Server :size="16" /><span>Machines</span><ArrowUpRight :size="13" class="out" />
          </button>
        </nav>
        <div class="settings-body">
          <component :is="current.panel" :key="current.id" />
        </div>
      </div>
    </div>
  </div>
</template>
