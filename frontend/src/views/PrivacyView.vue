<script setup>
import { ref, computed, onMounted } from "vue";
import { apiJSON } from "../lib/api.js";
import { session, loadSession } from "../stores/session.js";

const settings = ref({
  retention_days: 0,
  store_content: true,
  deletion_scheduled_for: null,
});
const maxRetentionDays = ref(3650);
const graceDays = ref(7);
const loading = ref(true);
const busy = ref(false);

const message = ref("");
const messageKind = ref("info");
const say = (text, kind = "info") => {
  message.value = text;
  messageKind.value = kind;
};

// Draft copies, so the form does not appear to have saved a change the server
// has not accepted yet.
const retentionDraft = ref(0);
const storeContentDraft = ref(true);

// Each destructive action opens its own confirmation, and each collects the
// password separately. A single shared prompt would let a user who typed their
// password for one action fire a different one by mistake.
const confirming = ref("");
const password = ref("");
const code = ref("");

const scheduledFor = computed(() => settings.value.deletion_scheduled_for);

const formatDate = (value) => {
  if (!value) return "";
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString();
};

const load = async () => {
  loading.value = true;
  const { ok, data } = await apiJSON("/api/privacy/settings");
  if (ok) {
    settings.value = data.settings;
    maxRetentionDays.value = data.max_retention_days ?? 3650;
    graceDays.value = data.deletion_grace ?? 7;
    retentionDraft.value = data.settings.retention_days;
    storeContentDraft.value = data.settings.store_content;
  } else {
    say(data.error || "Could not load your data settings.", "error");
  }
  loading.value = false;
};

const saveSettings = async () => {
  busy.value = true;
  const { ok, data } = await apiJSON("/api/privacy/settings", {
    method: "POST",
    json: {
      retention_days: Number(retentionDraft.value) || 0,
      store_content: storeContentDraft.value,
    },
  });
  busy.value = false;
  if (!ok) return say(data.error || "Could not save.", "error");
  settings.value = data.settings;
  say(data.message || "Saved.", "ok");
};

const openConfirm = (what) => {
  confirming.value = what;
  password.value = "";
  code.value = "";
  say("");
};

const closeConfirm = () => {
  confirming.value = "";
  password.value = "";
  code.value = "";
};

const purge = async (scope) => {
  busy.value = true;
  const { ok, data } = await apiJSON("/api/privacy/purge", {
    method: "POST",
    json: { scope, password: password.value, code: code.value.trim() },
  });
  busy.value = false;
  if (!ok) return say(data.error || "Could not delete that data.", "error");
  closeConfirm();
  say(
    `${data.message} (${data.deleted} record${data.deleted === 1 ? "" : "s"})`,
    "ok",
  );
};

const requestDeletion = async () => {
  busy.value = true;
  const { ok, data } = await apiJSON("/api/privacy/account/delete", {
    method: "POST",
    json: { password: password.value, code: code.value.trim() },
  });
  busy.value = false;
  if (!ok) return say(data.error || "Could not schedule deletion.", "error");
  closeConfirm();
  say(data.message, "ok");
  await Promise.all([load(), loadSession()]);
};

const cancelDeletion = async () => {
  busy.value = true;
  const { ok, data } = await apiJSON("/api/privacy/account/delete/cancel", {
    method: "POST",
  });
  busy.value = false;
  if (!ok) return say(data.error || "Could not cancel.", "error");
  say(data.message, "ok");
  await Promise.all([load(), loadSession()]);
};

// A plain link, not a fetch: the response is a file download, and routing it
// through JavaScript would mean holding the whole export in memory before
// handing it to the browser that was going to write it to disk anyway.
const exportHref = "/api/privacy/export";

onMounted(load);
</script>

<template>
  <div class="stack" style="gap: var(--s-5)">
    <header>
      <h1 class="page-title">Privacy</h1>
      <p class="muted">{{ session.email }}</p>
    </header>

    <p v-if="message" class="msg" :class="`msg--${messageKind}`" role="alert">
      {{ message }}
    </p>

    <!-- ── Pending deletion ────────────────────────────────────────────── -->
    <section
      v-if="scheduledFor"
      class="card card--alarm"
      aria-labelledby="pending-heading"
    >
      <h2 id="pending-heading" class="section-title">
        This account is scheduled for deletion
      </h2>
      <p class="body">
        Everything is removed on <strong>{{ formatDate(scheduledFor) }}</strong
        >. Until then nothing has changed and you can stop it.
      </p>
      <button
        class="btn"
        type="button"
        :disabled="busy"
        @click="cancelDeletion"
      >
        Cancel deletion
      </button>
    </section>

    <!-- ── What we keep ────────────────────────────────────────────────── -->
    <section class="card" aria-labelledby="policy-heading">
      <h2 id="policy-heading" class="section-title">What this service keeps</h2>
      <p class="muted body">
        These settings apply from now on. They do not touch what is already
        stored — use the deletion tools below for that.
      </p>

      <p v-if="loading" class="muted">Loading…</p>

      <div v-else class="stack">
        <div>
          <label class="label" for="retention"
            >Delete crawl records after</label
          >
          <div class="inline">
            <input
              id="retention"
              v-model="retentionDraft"
              class="field field--num"
              type="number"
              min="0"
              :max="maxRetentionDays"
              inputmode="numeric"
            />
            <span class="unit">days</span>
          </div>
          <p class="hint muted">
            {{
              Number(retentionDraft) > 0
                ? `Records untouched for ${retentionDraft} days are deleted automatically.`
                : "Zero keeps everything until you delete it yourself."
            }}
          </p>
        </div>

        <div>
          <label class="check">
            <input v-model="storeContentDraft" type="checkbox" />
            <span>
              <span class="check__title">Store page content</span>
              <span class="hint muted">
                Off means metadata only: titles, status codes, categories and
                the link graph are still recorded, but no copy of the pages
                themselves is kept. Change detection keeps working — a digest of
                a page is not a copy of it.
              </span>
            </span>
          </label>
        </div>

        <div>
          <button
            class="btn"
            type="button"
            :disabled="busy"
            @click="saveSettings"
          >
            Save settings
          </button>
        </div>
      </div>
    </section>

    <!-- ── Export ──────────────────────────────────────────────────────── -->
    <section class="card" aria-labelledby="export-heading">
      <h2 id="export-heading" class="section-title">Download your data</h2>
      <p class="muted body">
        A single JSON file with your account record, your data settings, your
        signed-in devices, your sign-in history, every site you crawled and
        every link between them. Passwords, two-factor secrets and recovery
        codes are not included — those are stored only as hashes and putting
        them in a downloadable file would be worse than the transparency is
        worth.
      </p>
      <a class="btn btn--secondary" :href="exportHref" download>
        Download JSON
      </a>
    </section>

    <!-- ── Selective deletion ──────────────────────────────────────────── -->
    <section class="card" aria-labelledby="delete-data-heading">
      <h2 id="delete-data-heading" class="section-title">Delete your data</h2>
      <p class="muted body">
        Each of these asks for your password again. A signed-in tab on a shared
        or stolen device should not be able to erase your account's data on its
        own.
      </p>

      <ul class="choices">
        <li class="choice">
          <div class="choice__text">
            <span class="choice__title">Stored page content</span>
            <span class="hint muted">
              Removes the retained copies of the pages. Keeps the record of
              which sites you crawled and how they link together.
            </span>
          </div>
          <button
            class="btn btn--secondary btn--sm"
            type="button"
            @click="openConfirm('page_content')"
          >
            Delete
          </button>
        </li>

        <li class="choice">
          <div class="choice__text">
            <span class="choice__title">All crawl history</span>
            <span class="hint muted">
              Removes every site and link you have collected. Your account and
              settings stay.
            </span>
          </div>
          <button
            class="btn btn--danger btn--sm"
            type="button"
            @click="openConfirm('crawl_history')"
          >
            Delete
          </button>
        </li>

        <li class="choice">
          <div class="choice__text">
            <span class="choice__title">Sign-in history</span>
            <span class="hint muted">
              Removes your recorded authentication events. Very recent ones are
              kept briefly — they are what throttles password guessing and
              outbound email against your address.
            </span>
          </div>
          <button
            class="btn btn--secondary btn--sm"
            type="button"
            @click="openConfirm('activity_log')"
          >
            Delete
          </button>
        </li>
      </ul>
    </section>

    <!-- ── Account deletion ────────────────────────────────────────────── -->
    <section
      v-if="!scheduledFor"
      class="card card--danger"
      aria-labelledby="delete-account-heading"
    >
      <h2 id="delete-account-heading" class="section-title">
        Delete this account
      </h2>
      <p class="body">
        Removes your account, your crawl records, your links and your sign-in
        history. It takes effect after {{ graceDays }} day{{
          graceDays === 1 ? "" : "s"
        }}, and we email you when it is scheduled so you can stop it if it was
        not you. After that it cannot be undone.
      </p>
      <button
        class="btn btn--danger"
        type="button"
        @click="openConfirm('account')"
      >
        Delete my account
      </button>
    </section>

    <!-- ── Confirmation ────────────────────────────────────────────────── -->
    <section
      v-if="confirming"
      class="card card--danger"
      aria-labelledby="confirm-heading"
    >
      <h2 id="confirm-heading" class="section-title">
        Confirm with your password
      </h2>
      <p class="body">
        {{
          confirming === "account"
            ? "This schedules your account for deletion."
            : "This deletes the data you selected and cannot be undone."
        }}
      </p>

      <div class="stack">
        <div>
          <label class="label" for="confirm-pass">Password</label>
          <input
            id="confirm-pass"
            v-model="password"
            class="field"
            type="password"
            autocomplete="current-password"
          />
        </div>
        <div>
          <label class="label" for="confirm-code">
            Authenticator or recovery code
            <span class="muted">(if two-factor is on)</span>
          </label>
          <input
            id="confirm-code"
            v-model="code"
            class="field field--code"
            inputmode="numeric"
            autocomplete="one-time-code"
          />
        </div>
        <div class="row">
          <button
            class="btn btn--danger"
            type="button"
            :disabled="busy || !password"
            @click="
              confirming === 'account' ? requestDeletion() : purge(confirming)
            "
          >
            {{ confirming === "account" ? "Schedule deletion" : "Delete" }}
          </button>
          <button class="btn btn--ghost" type="button" @click="closeConfirm">
            Cancel
          </button>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.page-title {
  font-size: 1.5rem;
}
.section-title {
  font-size: 1.05rem;
  margin-bottom: var(--s-3);
}
.body {
  margin: 0 0 var(--s-4);
  font-size: 0.92rem;
}
.hint {
  display: block;
  margin-top: var(--s-1);
  font-size: 0.82rem;
  line-height: 1.5;
}

.card--danger {
  border-color: rgba(255, 107, 107, 0.4);
}
.card--alarm {
  border-color: var(--warn);
  background: rgba(255, 200, 87, 0.06);
}

.inline {
  display: flex;
  align-items: center;
  gap: var(--s-2);
}
.field--num {
  max-width: 120px;
}
.unit {
  color: var(--text-dim);
  font-size: 0.9rem;
}
.field--code {
  font-family: var(--mono);
  letter-spacing: 0.3em;
}

.check {
  display: flex;
  align-items: flex-start;
  gap: var(--s-3);
  cursor: pointer;
  /* The whole row is the target, so the checkbox is never the only thing worth
     hitting on a touch screen. */
  min-height: var(--tap);
}
.check input {
  width: 20px;
  height: 20px;
  margin-top: 2px;
  flex: none;
  accent-color: var(--accent);
}
.check__title {
  display: block;
  font-weight: 650;
}

.choices {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--s-2);
}
.choice {
  display: flex;
  flex-direction: column;
  gap: var(--s-3);
  padding: var(--s-3);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
}
.choice__text {
  min-width: 0;
}
.choice__title {
  display: block;
  font-weight: 650;
}
.choice .btn {
  align-self: flex-start;
}

.row {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
}

@media (min-width: 640px) {
  .choice {
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
  }
  .choice .btn {
    align-self: auto;
    flex: none;
  }
}
</style>
