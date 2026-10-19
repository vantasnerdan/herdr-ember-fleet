<script setup lang="ts">
// No machines yet: a walk through adding the first one.
import AddMachine from "./AddMachine.vue";
</script>

<template>
  <div class="empty-fleet">
    <div class="empty-card">
      <img class="gate-mark" src="/mark.svg" alt="" />
      <span class="eyebrow">An empty fleet</span>
      <h1>Add your first machine</h1>
      <ol class="steps">
        <li><b>Name it.</b> The page makes a one-time join link for it.</li>
        <li>
          <b>Run one command on it</b>, as the OS user whose herdr you want here. It dials out to the relay, so the
          computer needs no SSH and no open port.
        </li>
        <li><b>Watch it arrive.</b> Every agent in its herdr shows up in the tree, with its state, live.</li>
      </ol>
      <AddMachine inline />
    </div>
  </div>
</template>
