<script setup>
import {
  ref,
  computed,
  onMounted,
  onUnmounted,
  watch,
  defineAsyncComponent,
} from "vue";
import { apiFetch, apiJSON } from "../lib/api.js";
import { isAdmin } from "../stores/session.js";
import {
  CATEGORY_COLORS,
  CATEGORY_LABELS,
  allCategories,
} from "../lib/categories.js";
import NodeList from "../components/NodeList.vue";

// Loaded on demand: the graph pulls in vis-network, which is by far the largest
// dependency here. Someone who only uses the list never downloads it.
const GraphPanel = defineAsyncComponent(
  () => import("../components/GraphPanel.vue"),
);

const view = ref("list"); // 'list' | 'graph'
const nodes = ref([]);
const edges = ref([]);
const loading = ref(false);
const graphLoading = ref(false);

const targetUrl = ref("");
const submitting = ref(false);
const formMessage = ref("");
const formKind = ref("info");

const query = ref("");
const searching = ref(false);
const category = ref("all");

const toast = ref("");
let toastTimer = null;
let pollTimer = null;
let searchTimer = null;
let searchAbort = null;

const showToast = (text) => {
  toast.value = text;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (toast.value = ""), 3000);
};

const visibleNodes = computed(() =>
  category.value === "all"
    ? nodes.value
    : nodes.value.filter((n) => n.category === category.value),
);

const fetchNodes = async () => {
  if (searching.value) return;
  loading.value = true;
  const { ok, data } = await apiJSON("/api/nodes");
  if (ok) nodes.value = data || [];
  loading.value = false;
};

const fetchEdges = async () => {
  const { ok, data } = await apiJSON("/api/edges");
  if (ok) edges.value = data || [];
};

const runSearch = async () => {
  const q = query.value.trim();
  clearTimeout(searchTimer);
  searchAbort?.abort();
  searchAbort = null;

  if (!q) {
    searching.value = false;
    return fetchNodes();
  }

  const controller = new AbortController();
  searchAbort = controller;
  searching.value = true;
  loading.value = true;
  try {
    // The query travels in a POST body, never a query string: search terms are
    // exactly the kind of value that should not reach proxy logs or history.
    const response = await apiFetch("/api/search", {
      method: "POST",
      json: { q },
      signal: controller.signal,
    });
    if (response.ok) nodes.value = (await response.json()) || [];
  } catch (err) {
    if (err.name !== "AbortError") showToast("Search failed.");
  } finally {
    if (searchAbort === controller) searchAbort = null;
    loading.value = false;
  }
};

const scheduleSearch = () => {
  clearTimeout(searchTimer);
  if (!query.value.trim()) return runSearch();
  searchTimer = setTimeout(runSearch, 350);
};

const clearSearch = () => {
  query.value = "";
  runSearch();
};

const startCrawl = async () => {
  if (!targetUrl.value.trim() || submitting.value) return;
  submitting.value = true;
  formMessage.value = "Adding to the queue…";
  formKind.value = "info";
  try {
    const { ok, data } = await apiJSON("/api/crawl", {
      method: "POST",
      json: { url: targetUrl.value.trim() },
    });
    if (ok) {
      formMessage.value = "Added to the crawl queue.";
      formKind.value = "ok";
      targetUrl.value = "";
      setTimeout(() => (formMessage.value = ""), 4000);
    } else {
      formMessage.value = data.error || "Could not queue that URL.";
      formKind.value = "error";
    }
  } catch {
    formMessage.value = "Connection error.";
    formKind.value = "error";
  } finally {
    submitting.value = false;
  }
};

const downloadExport = async (format) => {
  try {
    const response = await apiFetch(`/api/export?format=${format}`);
    if (!response.ok) throw new Error("export failed");
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `onion_spider_export.${format}`;
    a.style.display = "none";
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  } catch {
    showToast("Could not generate the export.");
  }
};

watch(view, async (v) => {
  if (v !== "graph") return;
  graphLoading.value = true;
  await Promise.all([fetchEdges(), fetchNodes()]);
  graphLoading.value = false;
});

onMounted(() => {
  fetchNodes();
  pollTimer = setInterval(() => {
    if (view.value === "list" && !searching.value) fetchNodes();
  }, 10000);
});

onUnmounted(() => {
  clearInterval(pollTimer);
  clearTimeout(toastTimer);
  clearTimeout(searchTimer);
  searchAbort?.abort();
});
</script>

<template>
  <div class="stack" style="gap: var(--s-5)">
    <!-- Submit -->
    <section class="card" aria-labelledby="crawl-heading">
      <div class="section-head">
        <h2 id="crawl-heading" class="section-title">Crawl a new address</h2>
        <div v-if="isAdmin" class="exports">
          <span class="exports__label">Export</span>
          <button
            v-for="fmt in ['csv', 'xlsx', 'pdf', 'graphml']"
            :key="fmt"
            class="btn btn--ghost btn--sm"
            type="button"
            @click="downloadExport(fmt)"
          >
            {{ fmt.toUpperCase() }}
          </button>
        </div>
      </div>

      <form class="crawl-form" @submit.prevent="startCrawl">
        <label class="sr-only" for="target">Onion URL to crawl</label>
        <input
          id="target"
          v-model="targetUrl"
          class="field"
          type="url"
          inputmode="url"
          autocomplete="off"
          spellcheck="false"
          placeholder="http://…onion"
        />
        <button class="btn" type="submit" :disabled="submitting">
          {{ submitting ? "Adding…" : "Add to queue" }}
        </button>
      </form>

      <p
        v-if="formMessage"
        class="msg"
        :class="`msg--${formKind}`"
        role="alert"
      >
        {{ formMessage }}
      </p>
    </section>

    <!-- View switch -->
    <div class="viewswitch" role="tablist" aria-label="Result view">
      <button
        class="viewswitch__btn"
        :class="{ 'is-active': view === 'list' }"
        role="tab"
        type="button"
        :aria-selected="view === 'list'"
        @click="view = 'list'"
      >
        List &amp; search
      </button>
      <button
        class="viewswitch__btn"
        :class="{ 'is-active': view === 'graph' }"
        role="tab"
        type="button"
        :aria-selected="view === 'graph'"
        @click="view = 'graph'"
      >
        Network map
      </button>
    </div>

    <!-- List -->
    <section
      v-if="view === 'list'"
      class="card"
      aria-labelledby="results-heading"
    >
      <div class="search">
        <label class="sr-only" for="search">Search crawled page content</label>
        <input
          id="search"
          v-model="query"
          class="field"
          type="search"
          placeholder="Search crawled content…"
          @input="scheduleSearch"
          @keyup.enter="runSearch"
        />
        <button
          v-if="searching"
          class="btn btn--ghost btn--sm"
          type="button"
          @click="clearSearch"
        >
          Clear
        </button>
      </div>

      <div class="filters" role="group" aria-label="Filter by category">
        <button
          class="chip"
          :class="{ 'is-active': category === 'all' }"
          type="button"
          :aria-pressed="category === 'all'"
          @click="category = 'all'"
        >
          All
        </button>
        <button
          v-for="cat in allCategories"
          :key="cat"
          class="chip"
          :class="{ 'is-active': category === cat }"
          type="button"
          :aria-pressed="category === cat"
          :style="
            category === cat
              ? {
                  background: CATEGORY_COLORS[cat],
                  borderColor: CATEGORY_COLORS[cat],
                  color: '#0a0a0a',
                }
              : { borderColor: `${CATEGORY_COLORS[cat]}66` }
          "
          @click="category = cat"
        >
          {{ CATEGORY_LABELS[cat] }}
        </button>
      </div>

      <div class="section-head section-head--tight">
        <h2 id="results-heading" class="section-title">
          {{ searching ? "Search results" : "Recently discovered" }}
          <span class="muted count">({{ visibleNodes.length }})</span>
        </h2>
        <button
          class="btn btn--ghost btn--sm"
          type="button"
          @click="fetchNodes"
        >
          Refresh
        </button>
      </div>

      <NodeList :nodes="visibleNodes" :loading="loading" />
    </section>

    <!-- Graph -->
    <template v-else>
      <p v-if="graphLoading" class="muted">Loading map…</p>
      <GraphPanel v-else :nodes="nodes" :edges="edges" @notice="showToast" />
    </template>

    <Transition name="toast">
      <p v-if="toast" class="toast" role="status">{{ toast }}</p>
    </Transition>
  </div>
</template>

<style scoped>
.section-head {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-3);
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--s-4);
}
.section-head--tight {
  margin: var(--s-4) 0 var(--s-3);
}
.section-title {
  font-size: 1rem;
}
.count {
  font-weight: 400;
  font-size: 0.85rem;
}

.exports {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--s-2);
}
.exports__label {
  font-size: 0.72rem;
  text-transform: uppercase;
  letter-spacing: 0.12em;
  color: var(--text-faint);
  font-weight: 700;
}

/* Mobile first: the field and its button stack; side by side once there is
   room for both without squeezing the input below a usable width. */
.crawl-form {
  display: flex;
  flex-direction: column;
  gap: var(--s-3);
}

.search {
  display: flex;
  gap: var(--s-2);
  align-items: center;
}

.filters {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
  margin-top: var(--s-3);
}
.chip {
  min-height: 36px;
  padding: 0 var(--s-3);
  border-radius: var(--radius-pill);
  border: 1px solid var(--border-strong);
  background: transparent;
  color: var(--text-dim);
  font: inherit;
  font-size: 0.78rem;
  font-weight: 650;
  cursor: pointer;
}
.chip:hover {
  color: var(--text);
}
.chip.is-active {
  color: #0a0a0a;
  background: var(--text);
}

.viewswitch {
  display: flex;
  gap: var(--s-2);
  padding: var(--s-1);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-pill);
}
.viewswitch__btn {
  flex: 1;
  min-height: var(--tap);
  padding: 0 var(--s-4);
  border: 0;
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--text-dim);
  font: inherit;
  font-weight: 650;
  cursor: pointer;
}
.viewswitch__btn.is-active {
  background: var(--accent);
  color: #fff;
}

.toast {
  position: fixed;
  left: 50%;
  bottom: var(--s-5);
  transform: translateX(-50%);
  margin: 0;
  padding: var(--s-3) var(--s-5);
  background: rgba(20, 20, 20, 0.97);
  border: 1px solid var(--accent);
  border-radius: var(--radius);
  box-shadow: var(--shadow-lg);
  font-size: 0.9rem;
  z-index: 50;
  pointer-events: none;
}
.toast-enter-active,
.toast-leave-active {
  transition:
    opacity 0.25s ease,
    transform 0.25s ease;
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateX(-50%) translateY(8px);
}

@media (min-width: 640px) {
  .crawl-form {
    flex-direction: row;
  }
  .crawl-form .field {
    flex: 1;
  }
}
</style>
