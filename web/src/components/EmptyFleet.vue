<script setup lang="ts">
// No machines yet: a walk through adding the first one.
import AddMachine from "./AddMachine.vue";
</script>

<template>
  <div class="empty-fleet">
    <div class="empty-card">
      <img class="gate-mark" src="/mark.svg" alt="" />
      <span class="eyebrow">Make yourself at home</span>
      <h1>Your fleet starts here<span class="coral">.</span></h1>
      <p class="hint">Bring your machines together. Keep every agent in view.</p>
      <ol class="steps">
        <li><b>Name a machine.</b> Create a one-time join link.</li>
        <li>
          <b>Connect it.</b> Run the join command as the user whose herdr you want to see.
        </li>
        <li><b>Open an agent.</b> Watch its screen, send a prompt, or take control.</li>
      </ol>
      <AddMachine inline />
    </div>
  </div>
</template>
