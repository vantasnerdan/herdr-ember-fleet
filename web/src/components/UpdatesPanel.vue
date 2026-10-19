<script setup lang="ts">
// Fleet updates: the hub pushes its own ember-fleet build to every machine and runs
// `herdr update` there, once a day or on demand. Lives in the settings dialog.
import { computed, onMounted, ref } from "vue";
import { RefreshCw } from "@lucide/vue";
import { api, errorText } from "../lib/api";
import { ago } from "../lib/fleet";
import { now, updates } from "../lib/store";
import type { UpdateDock, UpdateSettings } from "../lib/types";

const error = ref("");
const saving = ref(false);

onMounted(async () => {
  try {
    updates.value = await api.updates();
  } catch (e) {
    error.value = errorText(e);
  }
});

const st = computed(() => updates.value);
const hours = Array.from({ length: 24 }, (_, h) => h);

async function save(patch: Partial<UpdateSettings>) {
  error.value = "";
  saving.value = true;
  try {
    updates.value = await api.setUpdates(patch);
  } catch (e) {
    error.value = errorText(e);
  } finally {
    saving.value = false;
  }
}

async function runNow() {
  error.value = "";
  try {
    updates.value = await api.runUpdates();
  } catch (e) {
    error.value = errorText(e);
  }
}

const behind = computed(() => (st.value?.docks ?? []).filter((d) => d.online && d.ember !== st.value?.target).length);

function herdrLine(d: UpdateDock): string {
  if (!d.herdr) return "herdr ?";
  if (d.herdrServer && d.herdrServer !== d.herdr) return `herdr ${d.herdr} installed · server still ${d.herdrServer}`;
  return `herdr ${d.herdr}`;
}

function resultLine(d: UpdateDock): string {
  const r = d.result;
  if (!r) return "";
  const parts: string[] = [];
  if (r.emberTo) parts.push(`ember-fleet ${r.emberFrom || "?"} → ${r.emberTo}`);
  if (r.herdrTo && r.herdrFrom !== r.herdrTo) parts.push(`herdr ${r.herdrFrom || "?"} → ${r.herdrTo}`);
  else if (r.herdrTo) parts.push(`herdr ${r.herdrTo} (current)`);
  if (r.error) parts.push(r.error);
  return `${ago(r.at, now.value)}: ${parts.join(" · ") || "nothing to do"}`;
}
</script>

<template>
  <section class="fleet-section updates">
    <h3><RefreshCw :size="16" />Updates</h3>
    <template v-if="st">
      <p class="hint">
        This hub hands out ember-fleet <b>{{ st.target }}</b> to every machine; herdr updates come from herdr.dev.
        <template v-if="behind"> {{ behind }} machine{{ behind === 1 ? "" : "s" }} behind.</template>
      </p>
      <div class="update-settings">
        <label class="check"
          ><input
            type="checkbox"
            name="updates-enabled"
            :checked="st.settings.enabled"
            :disabled="saving"
            @change="save({ enabled: ($event.target as HTMLInputElement).checked })"
          />Update every day at
          <select
            name="updates-hour"
            :value="st.settings.hour"
            :disabled="saving || !st.settings.enabled"
            @change="save({ hour: Number(($event.target as HTMLSelectElement).value) })"
          >
            <option v-for="h in hours" :key="h" :value="h">{{ String(h).padStart(2, "0") }}:00</option>
          </select>
          <small class="muted">(hub time)</small></label
        >
        <label class="check"
          ><input
            type="checkbox"
            name="updates-herdr"
            :checked="st.settings.herdr"
            :disabled="saving"
            @change="save({ herdr: ($event.target as HTMLInputElement).checked })"
          />Also update herdr</label
        >
        <label class="check"
          ><input
            type="checkbox"
            name="updates-handoff"
            :checked="st.settings.handoff"
            :disabled="saving || !st.settings.herdr"
            @change="save({ handoff: ($event.target as HTMLInputElement).checked })"
          /><span class="check-text"
            >Live handoff: move running herdr servers to the new version now, keeping pane processes
            <small class="muted"
              >(experimental in herdr: monitors, waits and messages between panes can be cut and must reconnect;
              without it, servers pick up the new version on their next restart)</small
            ></span
          ></label
        >
      </div>
      <div class="update-actions">
        <button class="secondary" :disabled="st.running" @click="runNow">
          <RefreshCw :size="15" :class="{ spin: st.running }" />{{ st.running ? "Updating…" : "Update now" }}
        </button>
        <small class="muted" v-if="st.lastRun">Last run {{ ago(st.lastRun, now) }}</small>
      </div>
      <ul class="item-list update-list">
        <li v-for="d in st.docks" :key="d.id" :class="{ offline: !d.online }">
          <div class="item-text">
            <span class="line"
              ><b>{{ d.machine }}</b><span class="muted">·</span><span>{{ d.user }}</span
              ><span class="badge" :class="d.ember === st.target ? 'ok' : 'warn'">{{ d.ember || "?" }}</span
              ><span v-if="!d.online" class="badge">offline</span></span
            >
            <small :class="{ warn: d.herdrServer && d.herdr && d.herdrServer !== d.herdr }">{{ herdrLine(d) }}</small>
            <small v-if="d.result" :class="{ 'danger-text': d.result.error }">{{ resultLine(d) }}</small>
          </div>
        </li>
      </ul>
    </template>
    <p v-else-if="!error" class="hint">Loading…</p>
    <p v-if="error" class="error-text" role="alert">{{ error }}</p>
  </section>
</template>
