// Herdr Ember Fleet: every agent's herdr, on every computer, in one page.

import "@fontsource/space-mono/400.css";
import "@fontsource/space-mono/700.css";
import "@fontsource/space-mono/400-italic.css";
import "./styles/ember.css";
import "./styles/fleet.css";
import "./styles/pane.css";
import "./styles/screens.css";
import "./styles/extras.css";
import "./styles/groups.css";
import "./styles/attach.css";

import { createApp } from "vue";
import App from "./App.vue";
import { startEffects } from "./fx/vfx";

createApp(App).mount("#app");

try {
  startEffects();
} catch (error) {
  console.warn("Ember effects are off:", error);
}
