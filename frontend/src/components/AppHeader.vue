<script setup>
import { computed } from "vue";
import { useRouter } from "vue-router";
import { session, isLoggedIn, signOut } from "../stores/session.js";
import StatusBar from "./StatusBar.vue";

const props = defineProps({ open: { type: Boolean, default: false } });
const emit = defineEmits(["update:open"]);

const router = useRouter();
const open = computed({
  get: () => props.open,
  set: (v) => emit("update:open", v),
});

const links = [
  { to: "/", label: "Dashboard" },
  { to: "/security", label: "Security" },
  { to: "/privacy", label: "Privacy" },
];

const handleSignOut = async () => {
  await signOut();
  router.push({ name: "login" });
};
</script>

<template>
  <header class="header">
    <div class="header__inner">
      <RouterLink to="/" class="brand">
        <span class="brand__mark" aria-hidden="true">🕷️</span>
        <span class="brand__text">
          <span class="brand__name">Onion Spider</span>
          <span class="brand__tag">Deep web explorer</span>
        </span>
      </RouterLink>

      <!-- The drawer toggle is only meaningful on small screens, where the nav
           collapses. aria-expanded/aria-controls keep it announced correctly. -->
      <button
        v-if="isLoggedIn"
        class="drawer-toggle"
        type="button"
        :aria-expanded="open"
        aria-controls="primary-nav"
        @click="open = !open"
      >
        <span class="sr-only">{{ open ? "Close menu" : "Open menu" }}</span>
        <span
          class="drawer-toggle__bars"
          :class="{ 'is-open': open }"
          aria-hidden="true"
        >
          <span></span><span></span><span></span>
        </span>
      </button>

      <nav
        v-if="isLoggedIn"
        id="primary-nav"
        class="nav"
        :class="{ 'nav--open': open }"
      >
        <RouterLink
          v-for="link in links"
          :key="link.to"
          :to="link.to"
          class="nav__link"
        >
          {{ link.label }}
        </RouterLink>
        <span class="nav__user" :title="session.email">{{
          session.email
        }}</span>
        <button
          class="btn btn--ghost btn--sm nav__signout"
          type="button"
          @click="handleSignOut"
        >
          Sign out
        </button>
      </nav>
    </div>

    <StatusBar v-if="isLoggedIn" />
  </header>
</template>

<style scoped>
.header {
  border-bottom: 1px solid var(--border);
  background: var(--bg);
  position: sticky;
  top: 0;
  z-index: 20;
}

.header__inner {
  display: flex;
  align-items: center;
  gap: var(--s-4);
  flex-wrap: wrap;
  width: 100%;
  max-width: var(--content-max);
  margin: 0 auto;
  padding: var(--s-3) var(--s-4);
}

.brand {
  display: flex;
  align-items: center;
  gap: var(--s-3);
  color: inherit;
  text-decoration: none;
  margin-right: auto;
  min-height: var(--tap);
}
.brand:hover {
  text-decoration: none;
}
.brand__mark {
  font-size: 1.6rem;
  line-height: 1;
}
.brand__text {
  display: flex;
  flex-direction: column;
}
.brand__name {
  color: var(--accent);
  font-size: 1.25rem;
  font-weight: 800;
  letter-spacing: -0.03em;
}
.brand__tag {
  color: var(--text-faint);
  font-size: 0.68rem;
  text-transform: uppercase;
  letter-spacing: 0.16em;
  font-weight: 600;
}

.drawer-toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: var(--tap);
  height: var(--tap);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  cursor: pointer;
}
.drawer-toggle__bars {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 18px;
}
.drawer-toggle__bars span {
  height: 2px;
  background: var(--text);
  border-radius: 2px;
  transition:
    transform 0.18s ease,
    opacity 0.18s ease;
}
.drawer-toggle__bars.is-open span:nth-child(1) {
  transform: translateY(6px) rotate(45deg);
}
.drawer-toggle__bars.is-open span:nth-child(2) {
  opacity: 0;
}
.drawer-toggle__bars.is-open span:nth-child(3) {
  transform: translateY(-6px) rotate(-45deg);
}

/* Mobile first: the nav is a stacked drawer, hidden until toggled. */
.nav {
  display: none;
  width: 100%;
  flex-direction: column;
  gap: var(--s-1);
  padding-top: var(--s-3);
}
.nav--open {
  display: flex;
}

.nav__link {
  display: flex;
  align-items: center;
  min-height: var(--tap);
  padding: 0 var(--s-3);
  border-radius: var(--radius-sm);
  color: var(--text-dim);
  font-weight: 600;
}
.nav__link:hover {
  background: var(--surface-2);
  color: var(--text);
  text-decoration: none;
}
.nav__link.router-link-exact-active {
  color: var(--text);
  background: var(--surface-3);
}

.nav__user {
  display: flex;
  align-items: center;
  min-height: var(--tap);
  padding: 0 var(--s-3);
  color: var(--link);
  font-size: 0.85rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.nav__signout {
  align-self: flex-start;
  margin: var(--s-1) var(--s-3) var(--s-2);
}

/* From tablet up there is room for a single row, so the drawer becomes an
   ordinary horizontal nav and the toggle disappears. */
@media (min-width: 768px) {
  .drawer-toggle {
    display: none;
  }
  .nav {
    display: flex;
    flex-direction: row;
    align-items: center;
    width: auto;
    gap: var(--s-2);
    padding-top: 0;
  }
  .nav__user {
    max-width: 200px;
  }
  .nav__signout {
    align-self: auto;
    margin: 0;
  }
}
</style>
