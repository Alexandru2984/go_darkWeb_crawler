<script setup>
import { computed, onMounted, ref } from "vue";
import { apiJSON } from "../lib/api.js";

const loading = ref(true);
const busy = ref(false);
const ready = ref(false);
const gate = ref(false);
const mfaEnabled = ref(false);
const registrationOpen = ref(false);
const users = ref([]);
const page = ref(1);
const hasMore = ref(false);
const usersReady = ref(false);
const domains = ref([]);
const blacklistReady = ref(false);
const email = ref("");
const password = ref("");
const code = ref("");
const target = ref(null);
const domain = ref("");
const unblock = ref("");
const message = ref("");
const kind = ref("info");
const canSubmit = computed(
  () => !busy.value && password.value && (!mfaEnabled.value || code.value),
);

const say = (text, type = "info") => {
  message.value = text;
  kind.value = type;
};
const loadUsers = async () => {
  const result = await apiJSON(`/api/admin/users?page=${page.value}&limit=20`);
  usersReady.value = result.ok;
  if (result.ok) {
    users.value = result.data.users || [];
    hasMore.value = !!result.data.has_more;
  } else say(result.data.error || "Could not load accounts.", "error");
};
const loadBlacklist = async () => {
  const result = await apiJSON("/api/blacklist");
  blacklistReady.value = result.ok;
  if (result.ok) domains.value = result.data.domains || [];
  else say(result.data.error || "Could not load the blacklist.", "error");
};
const load = async () => {
  loading.value = true;
  ready.value = false;
  const status = await apiJSON("/api/auth/totp");
  if (!status.ok) {
    say(status.data.error || "Could not check account security.", "error");
    loading.value = false;
    return;
  }
  mfaEnabled.value = !!status.data.enabled;
  gate.value = !!status.data.required && !mfaEnabled.value;
  if (!gate.value) {
    await Promise.all([
      loadUsers(),
      loadBlacklist(),
      (async () => {
        const config = await apiJSON("/api/config");
        if (config.ok) registrationOpen.value = !!config.data.registration_open;
      })(),
    ]);
    ready.value = true;
  }
  loading.value = false;
};
const changePage = async (delta) => {
  busy.value = true;
  page.value += delta;
  await loadUsers();
  busy.value = false;
};
const chooseResend = (user) => {
  target.value = user;
  email.value = user.email;
  password.value = "";
  code.value = "";
  document.getElementById("invite-heading")?.focus();
};
const cancelResend = () => {
  target.value = null;
  email.value = "";
  password.value = "";
  code.value = "";
};
const invite = async () => {
  if (!canSubmit.value) return;
  busy.value = true;
  try {
    const json = { password: password.value, code: code.value.trim() };
    if (!target.value) json.email = email.value.trim();
    const path = target.value
      ? `/api/admin/users/${target.value.id}/invitation`
      : "/api/admin/users";
    const result = await apiJSON(path, { method: "POST", json });
    if (result.ok) {
      say(result.data.message, "ok");
      cancelResend();
    } else say(result.data.error || "Could not send the invitation.", "error");
    if (result.ok || result.data.account_saved) await loadUsers();
  } finally {
    password.value = "";
    code.value = "";
    busy.value = false;
  }
};
const blockDomain = async () => {
  if (busy.value) return;
  busy.value = true;
  const result = await apiJSON("/api/blacklist", {
    method: "POST",
    json: { domain: domain.value.trim() },
  });
  if (result.ok) {
    domain.value = "";
    say("Domain blocked.", "ok");
    await loadBlacklist();
  } else say(result.data.error || "Could not block the domain.", "error");
  busy.value = false;
};
const unblockDomain = async (value) => {
  if (busy.value) return;
  busy.value = true;
  const result = await apiJSON(`/api/blacklist/${encodeURIComponent(value)}`, {
    method: "DELETE",
  });
  if (result.ok) {
    unblock.value = "";
    say("Domain unblocked.", "ok");
    await loadBlacklist();
  } else say(result.data.error || "Could not unblock the domain.", "error");
  busy.value = false;
};
onMounted(load);
</script>

<template>
  <div class="stack admin-page">
    <header>
      <h1 class="page-title">Administration</h1>
      <p class="muted">Manage account access and the crawler blacklist.</p>
    </header>
    <p v-if="message" class="msg" :class="`msg--${kind}`" role="alert">
      {{ message }}
    </p>
    <p v-if="loading" class="muted" role="status">Loading administration…</p>
    <section v-else-if="gate" class="card stack" aria-labelledby="gate-heading">
      <h2 id="gate-heading" class="section-title">
        Protect your administrator account
      </h2>
      <p>
        Enable two-factor authentication before managing accounts or the
        blacklist. Save your recovery codes when you enable it.
      </p>
      <RouterLink class="btn gate-link" to="/security"
        >Open Account security</RouterLink
      >
    </section>
    <button v-else-if="!ready" class="btn" type="button" @click="load">
      Try again
    </button>
    <template v-else>
      <section class="card stack" aria-labelledby="invite-heading">
        <div>
          <h2 id="invite-heading" class="section-title" tabindex="-1">
            {{ target ? "Send a new invitation" : "Create an account" }}
          </h2>
          <p class="muted">
            {{
              registrationOpen
                ? "Public registration is open."
                : "Public registration is closed. Only an administrator can create accounts here."
            }}
            Each invitation creates a regular user; the recipient chooses their
            own password.
          </p>
        </div>
        <form class="stack" @submit.prevent="invite">
          <fieldset class="stack" :disabled="busy">
            <div>
              <label class="label" for="invite-email">{{
                target ? "Pending account" : "Recipient email"
              }}</label>
              <input
                id="invite-email"
                v-model="email"
                class="field"
                type="email"
                autocomplete="off"
                maxlength="254"
                :readonly="!!target"
                required
              />
            </div>
            <div class="credentials">
              <div>
                <label class="label" for="admin-password"
                  >Your administrator password</label
                >
                <input
                  id="admin-password"
                  v-model="password"
                  class="field"
                  type="password"
                  autocomplete="current-password"
                  maxlength="256"
                  required
                />
              </div>
              <div v-if="mfaEnabled">
                <label class="label" for="admin-code"
                  >Authenticator or recovery code</label
                >
                <input
                  id="admin-code"
                  v-model="code"
                  class="field"
                  type="text"
                  autocomplete="one-time-code"
                  spellcheck="false"
                  autocapitalize="off"
                  maxlength="64"
                  required
                />
              </div>
            </div>
            <p class="muted small">
              Links expire after 24 hours and work once. Resending invalidates
              the previous link. Wait five minutes between invitations to the
              same account.
            </p>
            <div class="actions">
              <button class="btn" type="submit" :disabled="!canSubmit">
                {{
                  busy
                    ? "Working…"
                    : target
                      ? "Send new invitation"
                      : "Create and invite"
                }}
              </button>
              <button
                v-if="target"
                class="btn btn--ghost"
                type="button"
                @click="cancelResend"
              >
                Cancel
              </button>
            </div>
          </fieldset>
        </form>
      </section>
      <section class="card stack" aria-labelledby="users-heading">
        <div class="section-head">
          <h2 id="users-heading" class="section-title">Accounts</h2>
          <button
            class="btn btn--ghost btn--sm"
            type="button"
            :disabled="busy"
            @click="loadUsers"
          >
            Refresh accounts
          </button>
        </div>
        <p v-if="!usersReady" class="muted">
          Accounts could not be loaded. Refresh to try again.
        </p>
        <p v-else-if="!users.length" class="muted">No accounts on this page.</p>
        <ul v-else class="records">
          <li v-for="user in users" :key="user.id" class="record">
            <div class="record-detail">
              <strong>{{ user.email }}</strong>
              <div class="badges">
                <span class="badge">{{
                  user.role === "admin" ? "Administrator" : "User"
                }}</span>
                <span
                  class="badge"
                  :class="user.is_verified ? 'badge--ok' : 'badge--pending'"
                  >{{
                    user.is_verified ? "Active" : "Pending activation"
                  }}</span
                >
                <span class="muted small"
                  >MFA {{ user.mfa_enabled ? "on" : "off" }}</span
                >
              </div>
            </div>
            <button
              v-if="!user.is_verified && user.role === 'user'"
              class="btn btn--ghost btn--sm"
              type="button"
              :disabled="busy"
              :aria-label="`Prepare new invitation for ${user.email}`"
              @click="chooseResend(user)"
            >
              New invitation
            </button>
          </li>
        </ul>
        <div class="actions">
          <button
            class="btn btn--ghost btn--sm"
            type="button"
            :disabled="busy || page <= 1"
            @click="changePage(-1)"
          >
            Previous
          </button>
          <span class="muted small" aria-live="polite">Page {{ page }}</span>
          <button
            class="btn btn--ghost btn--sm"
            type="button"
            :disabled="busy || !usersReady || !hasMore"
            @click="changePage(1)"
          >
            Next
          </button>
        </div>
      </section>
      <section class="card stack" aria-labelledby="blacklist-heading">
        <div>
          <h2 id="blacklist-heading" class="section-title">
            Crawler blacklist
          </h2>
          <p class="muted">
            Blocked v3 .onion domains are skipped for every account. Blocking a
            domain keeps existing crawl records.
          </p>
        </div>
        <form class="stack" @submit.prevent="blockDomain">
          <div>
            <label class="label" for="blocked-domain">Domain to block</label>
            <input
              id="blocked-domain"
              v-model="domain"
              class="field"
              type="text"
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
              maxlength="255"
              placeholder="56-character-address.onion"
              required
              :disabled="busy"
            />
          </div>
          <button
            class="btn gate-link"
            type="submit"
            :disabled="busy || !domain.trim()"
          >
            Block domain
          </button>
        </form>
        <p v-if="!blacklistReady" class="muted">
          The blacklist could not be loaded.
          <button
            class="btn btn--ghost btn--sm"
            type="button"
            :disabled="busy"
            @click="loadBlacklist"
          >
            Retry
          </button>
        </p>
        <p v-else-if="!domains.length" class="muted">No blocked domains.</p>
        <ul v-else class="records">
          <li v-for="value in domains" :key="value" class="record">
            <code class="record-detail">{{ value }}</code>
            <div v-if="unblock === value" class="actions">
              <button
                class="btn btn--sm"
                type="button"
                :disabled="busy"
                :aria-label="`Confirm unblock ${value}`"
                @click="unblockDomain(value)"
              >
                Confirm unblock
              </button>
              <button
                class="btn btn--ghost btn--sm"
                type="button"
                :disabled="busy"
                @click="unblock = ''"
              >
                Cancel
              </button>
            </div>
            <button
              v-else
              class="btn btn--ghost btn--sm"
              type="button"
              :disabled="busy"
              :aria-label="`Unblock ${value}`"
              @click="unblock = value"
            >
              Unblock
            </button>
          </li>
        </ul>
      </section>
    </template>
  </div>
</template>

<style scoped>
.admin-page {
  gap: var(--s-5);
}
.btn {
  max-width: 100%;
  white-space: normal;
  overflow-wrap: anywhere;
}
fieldset {
  border: 0;
  padding: 0;
  margin: 0;
  min-width: 0;
}
.credentials {
  display: grid;
  gap: var(--s-4);
}
.section-head,
.actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--s-3);
}
.section-head {
  justify-content: space-between;
}
.gate-link {
  align-self: flex-start;
}
.small {
  font-size: 0.85rem;
}
.records {
  list-style: none;
  padding: 0;
  margin: 0;
}
.record {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--s-3);
  padding: var(--s-4) 0;
  border-top: 1px solid var(--border);
}
.record-detail {
  min-width: 0;
  overflow-wrap: anywhere;
}
.badges {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--s-2);
  margin-top: var(--s-2);
}
.badge {
  border: 1px solid var(--border);
  border-radius: var(--radius-pill);
  padding: 0.15rem 0.5rem;
  font-size: 0.75rem;
}
.badge--ok {
  color: var(--ok);
}
.badge--pending {
  color: var(--warn);
}
@media (min-width: 700px) {
  .credentials {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .record {
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
  }
  .record-detail {
    flex: 1;
  }
}
</style>
