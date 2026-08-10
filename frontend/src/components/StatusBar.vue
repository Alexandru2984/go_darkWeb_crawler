<script setup>
import { ref, onMounted, onUnmounted } from "vue";
import { apiJSON } from "../lib/api.js";

const status = ref({
  status: "offline",
  nodes_crawled: 0,
  pending_nodes: 0,
  db_connected: false,
  active_workers: 0,
});

let timer = null;

const fetchStatus = async () => {
  const { ok, data } = await apiJSON("/api/status");
  if (ok) {
    status.value = data;
  } else {
    status.value = { ...status.value, status: "offline", db_connected: false };
  }
};

onMounted(() => {
  fetchStatus();
  timer = setInterval(fetchStatus, 10000);
});
onUnmounted(() => clearInterval(timer));
</script>

<template>
  <!-- A live region, so a screen reader hears the crawler going offline rather
       than only seeing a dot change colour. Polite: it must not interrupt. -->
  <div class="statusbar" role="status" aria-live="polite">
    <div class="statusbar__inner">
      <span
        class="stat"
        :class="status.db_connected ? 'stat--ok' : 'stat--down'"
      >
        <span class="dot" aria-hidden="true"></span>
        Database {{ status.db_connected ? "connected" : "offline" }}
      </span>
      <span class="stat"
        ><strong>{{ status.nodes_crawled.toLocaleString() }}</strong>
        crawled</span
      >
      <span class="stat"
        ><strong>{{ status.pending_nodes.toLocaleString() }}</strong>
        queued</span
      >
      <span class="stat"
        ><strong>{{ status.active_workers }}</strong> workers</span
      >
    </div>
  </div>
</template>

<style scoped>
.statusbar {
  background: var(--surface);
  border-top: 1px solid var(--border);
}

.statusbar__inner {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2) var(--s-4);
  width: 100%;
  max-width: var(--content-max);
  margin: 0 auto;
  padding: var(--s-2) var(--s-4);
  font-size: 0.8rem;
  color: var(--text-dim);
}

.stat {
  display: inline-flex;
  align-items: center;
  gap: var(--s-2);
}
.stat strong {
  color: var(--text);
  font-variant-numeric: tabular-nums;
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--danger);
  flex: none;
}
.stat--ok .dot {
  background: var(--ok);
  box-shadow: 0 0 8px rgba(61, 220, 132, 0.5);
}
.stat--down {
  color: var(--danger);
}
</style>
