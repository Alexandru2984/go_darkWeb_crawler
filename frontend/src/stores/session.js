import { reactive, computed } from "vue";
import { apiFetch, apiJSON, onSessionLost } from "../lib/api.js";

/**
 * Session state, shared by every view.
 *
 * The session token lives in an HttpOnly cookie, so this code cannot read it —
 * by design, since it means an XSS on the page has no long-lived credential to
 * exfiltrate. Whether we are signed in is therefore only knowable by asking the
 * server, which is what load() does and what makes a session survive a reload.
 */
const state = reactive({
  email: "",
  role: "",
  /** False until the first /api/auth/me answer, so the UI can avoid flashing
   *  the login form at somebody who is in fact still signed in. */
  checked: false,
});

export const session = state;
export const isLoggedIn = computed(() => !!state.email);
export const isAdmin = computed(() => state.role === "admin");

export async function loadSession() {
  const { ok, data } = await apiJSON("/api/auth/me");
  if (ok) {
    state.email = data.email || "";
    state.role = data.role || "";
  } else {
    state.email = "";
    state.role = "";
  }
  state.checked = true;
  return ok;
}

export function setSession({ email, role }) {
  state.email = email || "";
  state.role = role || "";
  state.checked = true;
}

/** Clears local state only. The cookie is HttpOnly and only the server can
 *  actually expire it — see signOut. */
export function clearSession() {
  state.email = "";
  state.role = "";
}

export async function signOut() {
  try {
    await apiFetch("/api/auth/logout", { method: "POST" });
  } catch {
    // Even if the request fails, drop the local state: leaving the dashboard
    // on screen would imply a session the user asked to end.
  }
  clearSession();
}

// Any 401 from anywhere ends the session view of the world.
onSessionLost(() => {
  if (state.email) clearSession();
});
