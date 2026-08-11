import { createRouter, createWebHistory } from "vue-router";
import { session, loadSession, isLoggedIn } from "./stores/session.js";

/**
 * Routes are lazily imported so the login screen does not carry the dashboard.
 *
 * This is the point of the split: vis-network is roughly three quarters of the
 * bundle, and it was previously downloaded by everyone who merely opened the
 * page — including over Tor, where that is measured in seconds. It now loads
 * only when a signed-in user actually opens the graph.
 */
const routes = [
  {
    path: "/",
    name: "dashboard",
    component: () => import("./views/DashboardView.vue"),
    meta: { requiresAuth: true, title: "Dashboard" },
  },
  {
    path: "/security",
    name: "security",
    component: () => import("./views/SecurityView.vue"),
    meta: { requiresAuth: true, title: "Account security" },
  },
  {
    path: "/changes",
    name: "changes",
    component: () => import("./views/ChangesView.vue"),
    meta: { requiresAuth: true, title: "Changes" },
  },
  {
    path: "/privacy",
    name: "privacy",
    component: () => import("./views/PrivacyView.vue"),
    meta: { requiresAuth: true, title: "Privacy" },
  },
  {
    path: "/login",
    name: "login",
    component: () => import("./views/LoginView.vue"),
    meta: { guestOnly: true, title: "Sign in" },
  },
  {
    path: "/reset-password",
    name: "reset-password",
    component: () => import("./views/ResetPasswordView.vue"),
    meta: { public: true, title: "Reset your password" },
  },
  {
    path: "/verify-account",
    name: "verify-account",
    component: () => import("./views/VerifyAccountView.vue"),
    meta: { public: true, title: "Verify your account" },
  },
  {
    path: "/:pathMatch(.*)*",
    name: "not-found",
    component: () => import("./views/NotFoundView.vue"),
    meta: { public: true, title: "Page not found" },
  },
];

export const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior: () => ({ top: 0 }),
});

router.beforeEach(async (to) => {
  // Resolve the session once, before the first guarded decision. Guessing and
  // correcting later would flash the login form at a signed-in user.
  if (!session.checked) await loadSession();

  if (to.meta.requiresAuth && !isLoggedIn.value) {
    return {
      name: "login",
      query: to.fullPath === "/" ? {} : { next: to.fullPath },
    };
  }
  if (to.meta.guestOnly && isLoggedIn.value) {
    return { name: "dashboard" };
  }
  return true;
});

router.afterEach((to) => {
  document.title = to.meta.title
    ? `${to.meta.title} · Onion Spider`
    : "Onion Spider";
});
