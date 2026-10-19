<script setup lang="ts">
// The kind of computer a dock runs on.
import { computed } from "vue";
import { HardDrive, Laptop, Monitor, Server } from "@lucide/vue";

const props = withDefaults(defineProps<{ os?: string; size?: number }>(), { size: 18 });
const kind = computed(() => {
  const os = (props.os ?? "").toLowerCase();
  if (os === "darwin" || os === "macos") return { icon: Laptop, label: "macOS" };
  if (os === "linux") return { icon: Server, label: "Linux" };
  if (os === "windows") return { icon: Monitor, label: "Windows" };
  return { icon: HardDrive, label: os || "Computer" };
});
</script>

<template>
  <component :is="kind.icon" class="device-icon" :size="size" :aria-label="kind.label" role="img" />
</template>
