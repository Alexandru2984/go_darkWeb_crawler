<script setup>
import AuthShell from "../components/AuthShell.vue";
import { useRouter } from "vue-router";

const router = useRouter();
</script>

<template>
  <AuthShell title="Page not found">
    <p class="muted body">That address does not exist in this application.</p>
    <button class="btn btn--block" type="button" @click="router.push('/')">
      Go to the dashboard
    </button>
  </AuthShell>
</template>

<style scoped>
.body {
  margin: 0 0 var(--s-4);
}
</style>
