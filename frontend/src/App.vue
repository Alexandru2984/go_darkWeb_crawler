<script setup>
import { ref, watch } from "vue";
import { useRoute } from "vue-router";
import AppHeader from "./components/AppHeader.vue";
import { session, deletionPending } from "./stores/session.js";

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

  <!-- A pending deletion follows the user across every screen. Showing it only
       on the Privacy page would mean the person most likely to need it — one
       whose account somebody else scheduled for deletion — never sees it. -->
  <div v-if="deletionPending && !route.meta.public" class="alarm" role="alert">
    <span>
      This account is scheduled for deletion. Nothing has been removed yet.
    </span>
    <RouterLink to="/privacy" class="alarm__action"
      >Review or cancel</RouterLink
    >
  </div>

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

.alarm {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: var(--s-2) var(--s-4);
  padding: var(--s-3) var(--s-4);
  background: rgba(255, 200, 87, 0.12);
  border-bottom: 1px solid var(--warn);
  color: #f0d9a0;
  font-size: 0.88rem;
  text-align: center;
}
.alarm__action {
  display: inline-flex;
  align-items: center;
  min-height: var(--tap);
  font-weight: 700;
  color: var(--warn);
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
