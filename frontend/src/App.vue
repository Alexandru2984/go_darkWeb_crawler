<script setup>
import { ref, watch } from "vue";
import { useRoute } from "vue-router";
import AppHeader from "./components/AppHeader.vue";
import { session } from "./stores/session.js";

const route = useRoute();
const navOpen = ref(false);

// Any navigation closes the drawer. Leaving it open over the new view is the
// classic mobile-menu bug: the user taps a link and appears to go nowhere.
watch(
  () => route.fullPath,
  () => {
    navOpen.value = false;
  },
);
</script>

<template>
  <a class="skip-link" href="#main">Skip to content</a>

  <AppHeader v-if="!route.meta.public" v-model:open="navOpen" />

  <main id="main" class="main" :class="{ 'main--bare': route.meta.public }">
    <!-- Nothing renders until the session question has been answered once, so
         the login form never flashes at somebody who is already signed in. -->
    <div v-if="!session.checked && !route.meta.public" class="booting">
      <span class="sr-only">Loading</span>
      <span class="spinner" aria-hidden="true"></span>
    </div>

    <RouterView v-else v-slot="{ Component }">
      <Suspense>
        <component :is="Component" />
        <template #fallback>
          <div class="booting">
            <span class="sr-only">Loading view</span>
            <span class="spinner" aria-hidden="true"></span>
          </div>
        </template>
      </Suspense>
    </RouterView>
  </main>
</template>

<style scoped>
.main {
  flex: 1;
  width: 100%;
  max-width: var(--content-max);
  margin: 0 auto;
  padding: var(--s-5) var(--s-4) var(--s-7);
}

/* Auth and email-link screens have no chrome, so they centre themselves in the
   viewport instead of sitting under a header that is not there. */
.main--bare {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--s-4);
}

.booting {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 40svh;
}

.spinner {
  width: 28px;
  height: 28px;
  border: 3px solid var(--border-strong);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

@media (min-width: 768px) {
  .main {
    padding: var(--s-6) var(--s-5) var(--s-7);
  }
}
</style>
