<script setup>
import { computed, onMounted, onUnmounted, ref } from "vue";
import { apiJSON } from "../lib/api.js";

const statusLabels = {
  pending: "Queued",
  crawling: "Crawling",
  completed: "Completed",
  failed: "Failed",
  blocked: "Blocked",
};

const snapshot = ref(null);
const loading = ref(false);
const error = ref("");
const refreshedAt = ref(null);
let controller;
let timer;

const counts = computed(() => snapshot.value?.status_counts ?? {});
const items = computed(() => snapshot.value?.next_items ?? []);

async function refresh() {
  if (loading.value || document.hidden) return;
  const request = new AbortController();
  controller = request;
  loading.value = true;
  error.value = "";
  try {
    const { ok, data } = await apiJSON("/api/queue", {
      signal: request.signal,
    });
    if (!ok) {
      error.value = data.error || "Could not load the queue.";
      return;
    }
    snapshot.value = data;
    refreshedAt.value = new Date();
  } catch (err) {
    if (err.name !== "AbortError") error.value = "Could not reach the server.";
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  refresh();
  timer = window.setInterval(refresh, 15000);
  document.addEventListener("visibilitychange", refresh);
});

onUnmounted(() => {
  controller?.abort();
  window.clearInterval(timer);
  document.removeEventListener("visibilitychange", refresh);
});
</script>

<template>
  <div class="queue">
    <header class="queue-header">
      <div>
        <h1>Crawl queue</h1>
        <p class="muted">Track pending addresses and crawl results.</p>
      </div>
      <button
        class="btn btn--secondary"
        type="button"
        :disabled="loading"
        @click="refresh"
      >
        {{ loading ? "Refreshing…" : "Refresh" }}
      </button>
    </header>

    <p v-if="error" class="msg msg--error" role="alert">
      {{ error }} {{ snapshot ? "Showing the last update." : "" }}
    </p>
    <p v-if="!snapshot && loading" class="muted" role="status">
      Loading queue…
    </p>

    <template v-if="snapshot">
      <dl class="queue-counts" aria-label="Crawl status totals">
        <div
          v-for="(label, status) in statusLabels"
          :key="status"
          class="count card"
          :data-status="status"
        >
          <dt>{{ label }}</dt>
          <dd>{{ (counts[status] ?? 0).toLocaleString() }}</dd>
        </div>
      </dl>

      <section class="card" aria-labelledby="up-next">
        <header class="items-header">
          <h2 id="up-next">Up next</h2>
          <p v-if="refreshedAt" class="muted updated">
            Updated
            <time :datetime="refreshedAt.toISOString()">{{
              refreshedAt.toLocaleTimeString()
            }}</time>
          </p>
        </header>
        <p v-if="items.length" class="muted">
          Shows up to 10 pending addresses in scheduled order.
        </p>
        <ol v-if="items.length" class="queue-items">
          <li v-for="(item, index) in items" :key="index" class="queue-item">
            <code>{{ item.url }}</code>
            <span class="muted">Depth {{ item.depth }}</span>
          </li>
        </ol>
        <div v-else class="queue-empty">
          <p>There are no pending addresses.</p>
          <RouterLink class="btn btn--secondary" :to="{ name: 'dashboard' }"
            >Open dashboard</RouterLink
          >
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.queue {
  display: grid;
  gap: var(--s-5);
}

.queue-header,
.items-header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--s-3);
}

.queue-header h1 {
  font-size: clamp(1.5rem, 3vw, 2.25rem);
}

.queue-header p,
.items-header p {
  margin: var(--s-1) 0 0;
}

.items-header h2 {
  font-size: 1.2rem;
}

.queue-counts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 126px), 1fr));
  gap: var(--s-3);
  margin: 0;
}

.count {
  border-top: 3px solid var(--count-color, var(--border-strong));
}

.count[data-status="pending"] {
  --count-color: var(--warn);
}
.count[data-status="crawling"] {
  --count-color: var(--link);
}
.count[data-status="completed"] {
  --count-color: var(--ok);
}
.count[data-status="failed"] {
  --count-color: var(--danger);
}

.count dt,
.updated {
  color: var(--text-dim);
  font-size: 0.85rem;
}

.count dd {
  margin: var(--s-2) 0 0;
  font-size: clamp(1.25rem, 4vw, 2rem);
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}

.queue-items {
  display: grid;
  gap: var(--s-2);
  margin: var(--s-4) 0 0;
  padding: 0;
  list-style: none;
}

.queue-item {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--s-2) var(--s-4);
  padding: var(--s-3);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
}

.queue-item code {
  min-width: 0;
  overflow-wrap: anywhere;
  font-family: var(--mono);
  font-size: 0.85rem;
}

.queue-item span {
  font-size: 0.8rem;
}

.queue-empty {
  margin-top: var(--s-4);
  padding: var(--s-4);
  border: 1px dashed var(--border-strong);
  border-radius: var(--radius-sm);
}

.queue-empty p {
  margin: 0 0 var(--s-3);
}
</style>
