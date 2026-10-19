<script setup lang="ts">
// Every blocked agent across the fleet, in tree order, plus the quieter "done" ones.
// Answering is always a person's click: this only opens the pane.
import { computed } from "vue";
import { Bell, BellOff, BellRing, CircleCheck } from "@lucide/vue";
import { alerts, disableAlerts, enableAlerts } from "../lib/alerts";
import { blocked, done, openPane, selectedKey } from "../lib/store";
import type { PaneInfo } from "../lib/fleet";

const where = (p: PaneInfo) =>
  [p.host, p.dock.user, p.session.name !== "default" ? p.session.name : "", p.workspace?.label]
    .filter(Boolean)
    .join(" · ");

const bell = computed(() => {
  if (!alerts.supported) return undefined;
  if (alerts.permission === "denied")
    return { icon: BellOff, title: "Notifications are blocked for this page in the browser's site settings", disabled: true };
  if (alerts.on) return { icon: BellRing, title: "Notifications are on: you hear about every agent that turns blocked. Click to turn off.", disabled: false };
  return { icon: Bell, title: "Notify me when an agent turns blocked", disabled: false };
});

function toggleBell() {
  if (alerts.on) disableAlerts();
  else void enableAlerts();
}
</script>

<template>
  <section class="needs" :class="{ calm: !blocked.length }" aria-label="Needs you">
    <div class="needs-head">
      <span v-if="blocked.length" class="title"
        >NEEDS YOU · {{ blocked.length }}</span
      >
      <span v-else class="calm-text">No agent is waiting on you.</span>
      <button
        v-if="bell"
        class="icon-button"
        :class="{ on: alerts.on }"
        :disabled="bell.disabled"
        :title="bell.title"
        :aria-label="bell.title"
        :aria-pressed="alerts.on"
        @click="toggleBell"
      >
        <component :is="bell.icon" :size="16" />
      </button>
    </div>
    <ul v-if="blocked.length" class="needs-list">
      <li v-for="p in blocked" :key="p.key">
        <button class="needs-item" :class="{ sel: selectedKey === p.key }" @click="openPane(p.ref)">
          <i class="sdot blocked beacon" />
          <span class="what">
            <span class="line1"><span class="who">{{ p.name }}</span><span class="where">{{ where(p) }}</span></span>
            <span v-if="p.title" class="q">{{ p.title }}</span>
          </span>
        </button>
      </li>
    </ul>
    <details v-if="done.length" class="done-list">
      <summary><CircleCheck :size="13" />{{ done.length }} done</summary>
      <ul class="needs-list">
        <li v-for="p in done" :key="p.key">
          <button class="needs-item" :class="{ sel: selectedKey === p.key }" @click="openPane(p.ref)">
            <i class="sdot done" />
            <span class="what">
              <span class="line1"><span class="who">{{ p.name }}</span><span class="where">{{ where(p) }}</span></span>
            </span>
          </button>
        </li>
      </ul>
    </details>
  </section>
</template>
