<script setup>
import { computed, onMounted, onUnmounted, ref } from "vue";
import { apiJSON } from "../lib/api.js";
import { CRAWL_STATUSES } from "../lib/searchFilters.js";
import CrawlBatchImport from "../components/CrawlBatchImport.vue";
const snapshot = ref(null),
  jobs = ref([]),
  status = ref("pending"),
  page = ref(1),
  more = ref(false),
  loading = ref(false),
  busy = ref(false),
  error = ref(""),
  message = ref(""),
  refreshedAt = ref(null);
const cancelConfirm = ref(0),
  scheduling = ref(0),
  scheduleDate = ref(""),
  scheduleTime = ref("");
const counts = computed(() => snapshot.value?.status_counts || {});
const failureLabels = {
  network: "Network request failed.",
  storage: "The crawl result could not be stored.",
  robots: "Disallowed by robots.txt. Automatic retries are disabled.",
  invalid_url: "Address rejected by crawler validation.",
  http_error: "The site returned an HTTP error.",
};
let controller, timer;
const date = (value) => {
  if (!value) return "Not scheduled";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
};
const localInput = (value) => {
  const d = new Date(value);
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16);
};
const manageable = (job) => !["crawling", "blocked"].includes(job.status);
const load = async () => {
  controller?.abort();
  const request = new AbortController();
  controller = request;
  loading.value = true;
  error.value = "";
  try {
    const [summary, list] = await Promise.all([
      apiJSON("/api/queue", { signal: request.signal }),
      apiJSON(`/api/queue/jobs?page=${page.value}&limit=20`, {
        method: "POST",
        json: { status: status.value },
        signal: request.signal,
      }),
    ]);
    if (controller !== request) return;
    if (summary.ok) {
      snapshot.value = summary.data;
      refreshedAt.value = new Date();
    } else error.value = summary.data.error || "Could not load your queue.";
    if (list.ok) {
      jobs.value = list.data.jobs || [];
      more.value = !!list.data.has_more;
      if (page.value > 1 && !jobs.value.length) {
        page.value = 1;
        await load();
      }
    } else error.value = list.data.error || "Could not load your jobs.";
  } catch (err) {
    if (err.name !== "AbortError") error.value = "Could not load your queue.";
  } finally {
    if (controller === request) loading.value = false;
  }
};
const filter = (value) => {
  status.value = value;
  page.value = 1;
  cancelConfirm.value = 0;
  scheduling.value = 0;
  load();
};
const move = (delta) => {
  page.value += delta;
  load();
};
const pause = async () => {
  if (busy.value || !snapshot.value) return;
  busy.value = true;
  error.value = "";
  const result = await apiJSON("/api/queue/pause", {
    method: "POST",
    json: { paused: !snapshot.value.paused },
  });
  busy.value = false;
  if (result.ok) {
    message.value = result.data.paused
      ? "New crawl claims are paused for your account."
      : "Your crawl queue is resumed.";
    await load();
  } else error.value = result.data.error || "Could not change pause state.";
};
const action = async (job, type) => {
  if (busy.value) return;
  let scheduled_at;
  if (type === "schedule") {
    const d = new Date(`${scheduleDate.value}T${scheduleTime.value}`);
    if (
      !scheduleDate.value ||
      !scheduleTime.value ||
      Number.isNaN(d.getTime())
    ) {
      error.value = "Choose a valid local date and time.";
      return;
    }
    scheduled_at = d.toISOString();
  }
  busy.value = true;
  error.value = "";
  const result = await apiJSON("/api/queue/action", {
    method: "POST",
    json: {
      url: job.url,
      action: type,
      ...(scheduled_at ? { scheduled_at } : {}),
    },
  });
  busy.value = false;
  if (result.ok) {
    cancelConfirm.value = 0;
    scheduling.value = 0;
    message.value =
      type === "cancel"
        ? "Future crawling cancelled. Your saved record, notes and watch are kept."
        : type === "schedule"
          ? "Crawl scheduled."
          : "Crawl queued for the next available worker.";
    await load();
  } else error.value = result.data.error || "Could not change this job.";
};
const openSchedule = (job) => {
  scheduling.value = job.id;
  cancelConfirm.value = 0;
  const next = new Date(job.scheduled_at);
  const local = localInput(
    next > new Date() ? next : new Date(Date.now() + 30 * 60 * 1000),
  );
  [scheduleDate.value, scheduleTime.value] = local.split("T");
};
onMounted(() => {
  load();
  timer = setInterval(() => {
    if (
      !document.hidden &&
      !busy.value &&
      !scheduling.value &&
      !cancelConfirm.value
    )
      load();
  }, 15000);
});
onUnmounted(() => {
  controller?.abort();
  clearInterval(timer);
});
</script>
<template>
  <div class="queue">
    <header class="queue-header">
      <div>
        <h1>Your crawl queue</h1>
        <p class="muted">
          Manage your account's pending work and future checks.
        </p>
      </div>
      <button
        class="btn btn--secondary"
        type="button"
        :disabled="loading || busy"
        @click="load"
      >
        {{ loading ? "Refreshing…" : "Refresh" }}
      </button>
    </header>
    <p v-if="error" class="msg msg--error" role="alert">{{ error }}</p>
    <p v-if="message" class="msg msg--ok" role="status">{{ message }}</p>
    <p v-if="!snapshot && loading" class="muted" role="status">
      Loading queue…
    </p>
    <template v-if="snapshot">
      <section class="card" aria-labelledby="crawl-control-heading">
        <div class="control-row">
          <div>
            <h2 id="crawl-control-heading">
              {{ snapshot.paused ? "Crawling paused" : "Crawling enabled" }}
            </h2>
            <p class="muted hint">
              Pause stops new claims for your account, including watched-site
              checks. Work already running finishes. New submissions stay
              queued.
            </p>
          </div>
          <button
            class="btn btn--secondary"
            type="button"
            :disabled="busy || loading"
            @click="pause"
          >
            {{ snapshot.paused ? "Resume crawling" : "Pause crawling" }}
          </button>
        </div>
        <div class="quota">
          <p>
            <strong>{{ counts.pending || 0 }}</strong> pending ·
            <strong>{{ snapshot.due }}</strong> due now
            <template v-if="snapshot.submission_limit"
              >· submission limit {{ snapshot.submission_limit }}</template
            ><template v-else>· submission limit disabled</template>
          </p>
          <progress
            v-if="snapshot.submission_limit"
            :value="Math.min(counts.pending || 0, snapshot.submission_limit)"
            :max="snapshot.submission_limit"
            aria-label="Pending queue submission capacity"
          />
          <p class="hint muted">
            The submission limit counts all pending records and applies to
            imports, manual retries and scheduling. Automatic discovery and
            retries may add more. Active requests are limited by worker
            availability.
          </p>
        </div>
      </section>
      <dl class="queue-counts" aria-label="Your crawl status totals">
        <div
          v-for="(label, value) in CRAWL_STATUSES"
          :key="value"
          class="count card"
          :data-status="value"
        >
          <dt>{{ label }}</dt>
          <dd>{{ (counts[value] || 0).toLocaleString() }}</dd>
        </div>
      </dl>
    </template>
    <CrawlBatchImport @changed="load" />
    <section class="card" aria-labelledby="queue-jobs-heading">
      <header class="items-header">
        <div>
          <h2 id="queue-jobs-heading">Crawl jobs</h2>
          <p v-if="refreshedAt" class="muted hint">
            Updated
            <time :datetime="refreshedAt.toISOString()">{{
              refreshedAt.toLocaleTimeString()
            }}</time>
          </p>
        </div>
        <div class="filter">
          <label class="label" for="queue-status">Job status</label
          ><select
            id="queue-status"
            class="field"
            :value="status"
            :disabled="busy"
            @change="filter($event.target.value)"
          >
            <option value="">All records</option>
            <option
              v-for="(label, value) in CRAWL_STATUSES"
              :key="value"
              :value="value"
            >
              {{ label }}
            </option>
          </select>
        </div>
      </header>
      <p class="hint muted">
        Cancelling stops future crawling and keeps your record, notes,
        collections and watch. Retry a cancelled site to resume checks. Running
        or blocked jobs cannot be changed here.
      </p>
      <p v-if="loading" class="muted" role="status">Loading jobs…</p>
      <ol v-else-if="jobs.length" class="queue-items">
        <li v-for="job in jobs" :key="job.id" class="queue-item">
          <div class="job-head">
            <h3 class="record-title">{{ job.title || "Untitled crawl" }}</h3>
            <span class="pill">{{
              CRAWL_STATUSES[job.status] || job.status
            }}</span>
          </div>
          <code>{{ job.url }}</code>
          <dl class="job-meta">
            <div>
              <dt>{{ job.status === "crawling" ? "Started" : "Scheduled" }}</dt>
              <dd>
                {{
                  date(
                    job.status === "crawling"
                      ? job.started_at
                      : job.scheduled_at,
                  )
                }}
              </dd>
            </div>
            <div>
              <dt>Failed attempts</dt>
              <dd>{{ job.retry_count }}</dd>
            </div>
            <div>
              <dt>Depth</dt>
              <dd>{{ job.depth }}</dd>
            </div>
          </dl>
          <p v-if="job.failure_code || job.status === 'failed'" class="failure">
            {{ failureLabels[job.failure_code] || "Previous crawl failed." }}
          </p>
          <div v-if="manageable(job)" class="actions">
            <button
              class="btn btn--secondary btn--sm"
              type="button"
              :disabled="busy"
              :aria-label="`Run now ${job.title || job.url}`"
              @click="action(job, 'retry')"
            >
              Run now
            </button>
            <button
              class="btn btn--ghost btn--sm"
              type="button"
              :disabled="busy"
              :aria-label="`Schedule ${job.title || job.url}`"
              @click="openSchedule(job)"
            >
              Schedule
            </button>
            <button
              v-if="job.status !== 'cancelled'"
              class="btn btn--danger btn--sm"
              type="button"
              :disabled="busy"
              :aria-label="`Cancel future crawl ${job.title || job.url}`"
              @click="
                cancelConfirm = job.id;
                scheduling = 0;
              "
            >
              Cancel future crawl
            </button>
          </div>
          <div v-if="cancelConfirm === job.id" class="confirmation">
            <p>
              Cancel future crawling of this site? Its stored data and watch are
              kept.
            </p>
            <div class="actions">
              <button
                class="btn btn--danger btn--sm"
                type="button"
                :disabled="busy"
                @click="action(job, 'cancel')"
              >
                Confirm cancellation</button
              ><button
                class="btn btn--ghost btn--sm"
                type="button"
                :disabled="busy"
                @click="cancelConfirm = 0"
              >
                Keep scheduled
              </button>
            </div>
          </div>
          <form
            v-if="scheduling === job.id && manageable(job)"
            class="schedule-form"
            @submit.prevent="action(job, 'schedule')"
          >
            <div class="schedule-fields">
              <div>
                <label class="label" :for="`schedule-date-${job.id}`"
                  >Local date</label
                ><input
                  :id="`schedule-date-${job.id}`"
                  v-model="scheduleDate"
                  class="field"
                  type="date"
                  required
                  :disabled="busy"
                />
              </div>
              <div>
                <label class="label" :for="`schedule-time-${job.id}`"
                  >Local time</label
                ><input
                  :id="`schedule-time-${job.id}`"
                  v-model="scheduleTime"
                  class="field"
                  type="time"
                  required
                  :disabled="busy"
                />
              </div>
            </div>
            <p class="hint muted">
              Choose a time within the next 30 days. It becomes eligible then;
              available workers decide the exact start.
            </p>
            <div class="actions">
              <button
                class="btn btn--sm"
                type="submit"
                :disabled="busy || !scheduleDate || !scheduleTime"
              >
                Save schedule</button
              ><button
                class="btn btn--ghost btn--sm"
                type="button"
                :disabled="busy"
                @click="scheduling = 0"
              >
                Close schedule
              </button>
            </div>
          </form>
        </li>
      </ol>
      <p v-else class="muted">No jobs match this status.</p>
      <nav class="pagination" aria-label="Crawl job pages">
        <button
          class="btn btn--ghost"
          type="button"
          :disabled="loading || page === 1 || busy"
          @click="move(-1)"
        >
          Previous</button
        ><span aria-live="polite">Page {{ page }}</span
        ><button
          class="btn btn--ghost"
          type="button"
          :disabled="loading || !more || busy"
          @click="move(1)"
        >
          Next
        </button>
      </nav>
    </section>
  </div>
</template>
<style scoped>
.queue {
  display: grid;
  gap: var(--s-5);
  min-width: 0;
}
.queue-header,
.items-header,
.control-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--s-3);
}
.queue-header h1 {
  font-size: clamp(1.5rem, 3vw, 2.25rem);
}
h2 {
  font-size: 1.15rem;
}
.hint {
  font-size: 0.85rem;
  margin: var(--s-2) 0;
}
.control-row > div {
  flex: 1 1 300px;
  min-width: 0;
}
.quota {
  margin-top: var(--s-4);
}
.quota progress {
  width: 100%;
  height: 10px;
  accent-color: var(--accent);
}
.queue-counts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 126px), 1fr));
  gap: var(--s-3);
  margin: 0;
}
.count {
  border-top: 3px solid var(--border-strong);
}
.count[data-status="pending"] {
  border-color: var(--warn);
}
.count[data-status="crawling"] {
  border-color: var(--link);
}
.count[data-status="failed"] {
  border-color: var(--danger);
}
.count dt {
  font-size: 0.85rem;
  color: var(--text-dim);
}
.count dd {
  margin: var(--s-2) 0 0;
  font-size: clamp(1.25rem, 4vw, 2rem);
  font-weight: 700;
}
.filter {
  min-width: 0;
  max-width: 100%;
}
.queue-items {
  display: grid;
  gap: var(--s-3);
  padding: 0;
  margin: var(--s-3) 0;
  list-style: none;
}
.queue-item {
  display: grid;
  gap: var(--s-2);
  padding: var(--s-3);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  min-width: 0;
}
.job-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--s-2);
}
.record-title {
  font: inherit;
  font-weight: 650;
  color: var(--text);
  background: none;
  border: 0;
  text-align: left;
  overflow-wrap: anywhere;
  max-width: 100%;
  padding: var(--s-1) 0;
}
.queue-item code {
  overflow-wrap: anywhere;
  font-size: 0.8rem;
  white-space: normal;
}
.job-meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2) var(--s-4);
  margin: 0;
  font-size: 0.8rem;
}
.job-meta dt {
  color: var(--text-dim);
}
.job-meta dd {
  margin: var(--s-1) 0 0;
}
.failure {
  font-size: 0.85rem;
  color: var(--warn);
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
}
.actions .btn {
  max-width: 100%;
  white-space: normal;
}
.confirmation,
.schedule-form {
  border-top: 1px solid var(--border);
  padding-top: var(--s-3);
  display: grid;
  gap: var(--s-2);
  min-width: 0;
}
.schedule-form {
  grid-template-columns: minmax(0, 1fr);
}
.schedule-fields {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr));
  gap: var(--s-3);
}
.schedule-form > div,
.schedule-fields > div {
  min-width: 0;
}
input[type="date"],
input[type="time"] {
  min-width: 0;
  max-width: 100%;
}
.pagination {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
  align-items: center;
  justify-content: space-between;
  margin-top: var(--s-3);
}
</style>
