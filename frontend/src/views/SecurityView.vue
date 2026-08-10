<script setup>
import { ref, onMounted } from "vue";
import { apiJSON } from "../lib/api.js";
import { session } from "../stores/session.js";

const totp = ref({
  enabled: false,
  required: false,
  recovery_codes_remaining: 0,
});
const sessions = ref([]);
const loading = ref(true);

// Enrolment state. The secret and the recovery codes exist in memory only, for
// as long as the user needs to record them — they are never written to storage.
const enrolling = ref(false);
const secret = ref("");
const provisioningUri = ref("");
const enrolCode = ref("");
const recoveryCodes = ref([]);

const disabling = ref(false);
const disablePassword = ref("");
const disableCode = ref("");

const message = ref("");
const messageKind = ref("info");
const busy = ref(false);

const say = (text, kind = "info") => {
  message.value = text;
  messageKind.value = kind;
};

const loadAll = async () => {
  loading.value = true;
  const [t, s] = await Promise.all([
    apiJSON("/api/auth/totp"),
    apiJSON("/api/auth/sessions"),
  ]);
  if (t.ok) totp.value = t.data;
  if (s.ok) sessions.value = s.data || [];
  loading.value = false;
};

const beginEnrol = async () => {
  busy.value = true;
  const { ok, data } = await apiJSON("/api/auth/totp/setup", {
    method: "POST",
  });
  busy.value = false;
  if (!ok) return say(data.error || "Could not start enrolment.", "error");
  secret.value = data.secret;
  provisioningUri.value = data.provisioning_uri;
  enrolling.value = true;
  say("");
};

const confirmEnrol = async () => {
  busy.value = true;
  const { ok, data } = await apiJSON("/api/auth/totp/confirm", {
    method: "POST",
    json: { code: enrolCode.value.trim() },
  });
  busy.value = false;
  if (!ok) return say(data.error || "That code was not accepted.", "error");
  recoveryCodes.value = data.recovery_codes || [];
  enrolling.value = false;
  secret.value = "";
  provisioningUri.value = "";
  enrolCode.value = "";
  say(
    "Two-factor authentication is on. Save the recovery codes below now.",
    "ok",
  );
  await loadAll();
};

const cancelEnrol = () => {
  enrolling.value = false;
  secret.value = "";
  provisioningUri.value = "";
  enrolCode.value = "";
};

const confirmDisable = async () => {
  busy.value = true;
  const { ok, data } = await apiJSON("/api/auth/totp/disable", {
    method: "POST",
    json: { password: disablePassword.value, code: disableCode.value.trim() },
  });
  busy.value = false;
  disablePassword.value = "";
  disableCode.value = "";
  if (!ok) return say(data.error || "Could not turn two-factor off.", "error");
  disabling.value = false;
  recoveryCodes.value = [];
  say("Two-factor authentication is off.", "ok");
  await loadAll();
};

const revokeSession = async (id) => {
  const { ok, data } = await apiJSON(`/api/auth/sessions/${id}`, {
    method: "DELETE",
  });
  if (!ok) return say(data.error || "Could not sign that device out.", "error");
  say("Device signed out.", "ok");
  await loadAll();
};

const copyCodes = async () => {
  try {
    await navigator.clipboard.writeText(recoveryCodes.value.join("\n"));
    say("Recovery codes copied.", "ok");
  } catch {
    say("Could not copy — select and copy them manually.", "error");
  }
};

onMounted(loadAll);
</script>

<template>
  <div class="stack" style="gap: var(--s-5)">
    <header>
      <h1 class="page-title">Account security</h1>
      <p class="muted">{{ session.email }}</p>
    </header>

    <p v-if="message" class="msg" :class="`msg--${messageKind}`" role="alert">
      {{ message }}
    </p>

    <!-- ── Two-factor ──────────────────────────────────────────────────── -->
    <section class="card" aria-labelledby="mfa-heading">
      <div class="head">
        <h2 id="mfa-heading" class="section-title">
          Two-factor authentication
        </h2>
        <span class="state" :class="totp.enabled ? 'state--on' : 'state--off'">
          {{ totp.enabled ? "On" : "Off" }}
        </span>
      </div>

      <p v-if="totp.required && !totp.enabled" class="notice notice--warn">
        Your account is an administrator. Administrative actions stay blocked
        until you enrol a second factor — everything else keeps working.
      </p>

      <template v-if="loading">
        <p class="muted">Loading…</p>
      </template>

      <!-- Not enrolled -->
      <template v-else-if="!totp.enabled && !enrolling">
        <p class="muted body">
          A one-time code from an authenticator app, required alongside your
          password. It keeps working even if your password leaks.
        </p>
        <button class="btn" type="button" :disabled="busy" @click="beginEnrol">
          Set up two-factor
        </button>
      </template>

      <!-- Enrolling -->
      <template v-else-if="enrolling">
        <ol class="steps">
          <li>
            Add this account to your authenticator app. Most apps let you paste
            the setup key directly:
            <code class="secret mono">{{ secret }}</code>
            <a class="uri" :href="provisioningUri"
              >Open in an installed authenticator app</a
            >
          </li>
          <li>
            Enter the six-digit code it shows:
            <div class="confirm-row">
              <label class="sr-only" for="enrol-code">Authenticator code</label>
              <input
                id="enrol-code"
                v-model="enrolCode"
                class="field field--code"
                inputmode="numeric"
                autocomplete="one-time-code"
                maxlength="6"
                placeholder="123456"
              />
              <button
                class="btn"
                type="button"
                :disabled="busy"
                @click="confirmEnrol"
              >
                Confirm
              </button>
            </div>
          </li>
        </ol>
        <button
          class="btn btn--ghost btn--sm"
          type="button"
          @click="cancelEnrol"
        >
          Cancel
        </button>
      </template>

      <!-- Enrolled -->
      <template v-else>
        <p class="muted body">
          {{ totp.recovery_codes_remaining }} unused recovery code{{
            totp.recovery_codes_remaining === 1 ? "" : "s"
          }}
          remaining.
        </p>

        <template v-if="!disabling">
          <button
            class="btn btn--danger"
            type="button"
            @click="disabling = true"
          >
            Turn off two-factor
          </button>
        </template>
        <div v-else class="stack">
          <p class="notice notice--warn">
            Confirm with your password and a current code. Requiring both is
            what stops someone who has taken over a signed-in tab from simply
            removing this protection.
          </p>
          <div>
            <label class="label" for="disable-pass">Password</label>
            <input
              id="disable-pass"
              v-model="disablePassword"
              class="field"
              type="password"
              autocomplete="current-password"
            />
          </div>
          <div>
            <label class="label" for="disable-code"
              >Authenticator or recovery code</label
            >
            <input
              id="disable-code"
              v-model="disableCode"
              class="field field--code"
              inputmode="numeric"
              autocomplete="one-time-code"
            />
          </div>
          <div class="row">
            <button
              class="btn btn--danger"
              type="button"
              :disabled="busy"
              @click="confirmDisable"
            >
              Turn off
            </button>
            <button
              class="btn btn--ghost"
              type="button"
              @click="disabling = false"
            >
              Cancel
            </button>
          </div>
        </div>
      </template>

      <!-- Shown exactly once, right after enrolment. -->
      <div v-if="recoveryCodes.length" class="codes">
        <h3 class="codes__title">Recovery codes</h3>
        <p class="notice notice--warn">
          These are shown once and cannot be retrieved later — only their hashes
          are stored. Each works a single time, if you lose your authenticator.
        </p>
        <ul class="codes__list mono">
          <li v-for="c in recoveryCodes" :key="c">{{ c }}</li>
        </ul>
        <button
          class="btn btn--secondary btn--sm"
          type="button"
          @click="copyCodes"
        >
          Copy all
        </button>
      </div>
    </section>

    <!-- ── Sessions ────────────────────────────────────────────────────── -->
    <section class="card" aria-labelledby="sessions-heading">
      <div class="head">
        <h2 id="sessions-heading" class="section-title">Signed-in devices</h2>
        <button class="btn btn--ghost btn--sm" type="button" @click="loadAll">
          Refresh
        </button>
      </div>

      <p class="muted body">
        No IP addresses are recorded. The device name is a coarse family derived
        from the browser, not a fingerprint.
      </p>

      <ul v-if="sessions.length" class="sessions">
        <li v-for="s in sessions" :key="s.id" class="sessionrow">
          <div class="sessionrow__main">
            <span class="sessionrow__name">
              {{ s.device_label || "Unknown device" }}
              <span v-if="s.current" class="tag">This device</span>
            </span>
            <span class="sessionrow__meta muted">
              Last used {{ s.last_used_at }} · signed in {{ s.created_at }}
            </span>
          </div>
          <button
            v-if="!s.current"
            class="btn btn--danger btn--sm"
            type="button"
            @click="revokeSession(s.id)"
          >
            Sign out
          </button>
        </li>
      </ul>
      <p v-else-if="!loading" class="muted">No other active sessions.</p>
    </section>
  </div>
</template>

<style scoped>
.page-title {
  font-size: 1.5rem;
}

.head {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-3);
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--s-3);
}
.section-title {
  font-size: 1.05rem;
}
.body {
  margin: 0 0 var(--s-4);
  font-size: 0.92rem;
}

.state {
  padding: 4px 12px;
  border-radius: var(--radius-pill);
  font-size: 0.75rem;
  font-weight: 700;
}
.state--on {
  color: var(--ok);
  background: rgba(61, 220, 132, 0.14);
}
.state--off {
  color: var(--text-dim);
  background: var(--surface-3);
}

.notice {
  margin: 0 0 var(--s-4);
  padding: var(--s-3);
  border-radius: var(--radius-sm);
  border-left: 3px solid;
  font-size: 0.88rem;
}
.notice--warn {
  border-color: var(--warn);
  background: rgba(255, 200, 87, 0.08);
  color: #f0d9a0;
}

.steps {
  margin: 0 0 var(--s-4);
  padding-left: var(--s-5);
  display: flex;
  flex-direction: column;
  gap: var(--s-4);
  font-size: 0.92rem;
}

.secret {
  display: block;
  margin: var(--s-3) 0;
  padding: var(--s-3);
  background: #070707;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  font-size: 0.85rem;
  letter-spacing: 0.12em;
  overflow-wrap: anywhere;
}
.uri {
  font-size: 0.85rem;
}

.confirm-row {
  display: flex;
  flex-direction: column;
  gap: var(--s-2);
  margin-top: var(--s-3);
}
.field--code {
  font-family: var(--mono);
  letter-spacing: 0.3em;
  text-align: center;
}

.row {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
}

.codes {
  margin-top: var(--s-5);
  padding-top: var(--s-4);
  border-top: 1px solid var(--border);
}
.codes__title {
  font-size: 0.95rem;
  margin-bottom: var(--s-3);
}
.codes__list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  gap: var(--s-2);
  list-style: none;
  margin: 0 0 var(--s-4);
  padding: var(--s-3);
  background: #070707;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  font-size: 0.88rem;
}

.sessions {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--s-2);
}
.sessionrow {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-3);
  align-items: center;
  justify-content: space-between;
  padding: var(--s-3);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
}
.sessionrow__main {
  display: flex;
  flex-direction: column;
  gap: var(--s-1);
  min-width: 0;
}
.sessionrow__name {
  font-weight: 650;
  display: flex;
  align-items: center;
  gap: var(--s-2);
  flex-wrap: wrap;
}
.sessionrow__meta {
  font-size: 0.78rem;
}
.tag {
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  background: rgba(98, 176, 255, 0.15);
  color: var(--link);
  font-size: 0.68rem;
  font-weight: 700;
}

@media (min-width: 560px) {
  .confirm-row {
    flex-direction: row;
    align-items: center;
  }
  .confirm-row .field {
    max-width: 200px;
  }
}
</style>
