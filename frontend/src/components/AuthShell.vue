<script setup>
defineProps({ title: { type: String, required: true } });
</script>

<template>
  <div class="shell">
    <div class="shell__brand">
      <span class="shell__mark" aria-hidden="true">🕷️</span>
      <p class="shell__name">Onion Spider</p>
    </div>
    <section class="card shell__card">
      <h1 class="shell__title">{{ title }}</h1>
      <slot />
    </section>
  </div>
</template>

<style scoped>
.shell {
  width: 100%;
  max-width: 420px;
}
.shell__brand {
  text-align: center;
  margin-bottom: var(--s-5);
}
.shell__mark {
  font-size: 2.2rem;
}
.shell__name {
  margin: var(--s-2) 0 0;
  color: var(--accent);
  font-size: 1.35rem;
  font-weight: 800;
  letter-spacing: -0.03em;
}
.shell__card {
  padding: var(--s-5) var(--s-4);
}
.shell__title {
  font-size: 1.1rem;
  margin-bottom: var(--s-4);
}

@media (min-width: 480px) {
  .shell__card {
    padding: var(--s-6);
  }
}
</style>
