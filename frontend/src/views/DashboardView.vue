<script setup>
import {
  computed,
  ref,
  onMounted,
  onUnmounted,
  watch,
  defineAsyncComponent,
} from "vue";
import { apiFetch, apiJSON, readJSON } from "../lib/api.js";
import { isAdmin } from "../stores/session.js";
import {
  CATEGORY_COLORS,
  CATEGORY_LABELS,
  allCategories,
} from "../lib/categories.js";
import NodeList from "../components/NodeList.vue";
import NodeInspector from "../components/NodeInspector.vue";
import NodeAnnotator from "../components/NodeAnnotator.vue";
import SavedSearches from "../components/SavedSearches.vue";
import ResearchFilters from "../components/ResearchFilters.vue";
import { CRAWL_STATUSES, emptyResearchFilters } from "../lib/searchFilters.js";
import { refreshUnseenChanges } from "../stores/changes.js";

// Loaded on demand: the graph pulls in vis-network, which is by far the largest
// dependency here. Someone who only uses the list never downloads it.
const GraphPanel = defineAsyncComponent(
  () => import("../components/GraphPanel.vue"),
);

const view = ref("list"); // 'list' | 'graph'
const nodes = ref([]);
const graphNodes = ref([]);
const edges = ref([]);
const loading = ref(false);
const graphLoading = ref(false);
const graphError = ref("");

// The site whose annotation sheet is open. Empty means closed; the dialog is
// driven by this rather than a separate boolean so the two can never disagree.
const inspecting = ref("");
const annotating = ref({ url: "", title: "" });
const openAnnotator = (node) => {
  annotating.value = { url: node.url, title: node.title };
};
const closeAnnotator = () => {
  annotating.value = { url: "", title: "" };
};

const targetUrl = ref("");
const submitting = ref(false);
const formMessage = ref("");
const formKind = ref("info");

const query = ref("");
const searching = ref(false);
const category = ref("all");
const status = ref("");
const research = ref(emptyResearchFilters());
const researchDirty = ref(false);
const researchReset = ref(0);
const collections = ref([]);
const page = ref(1);
const hasMore = ref(false);
const resultError = ref("");
const statuses = CRAWL_STATUSES;
const currentCriteria = computed(() => ({
  q: query.value.trim(),
  category: category.value === "all" ? "" : category.value,
  status: status.value,
  ...research.value,
}));
const filtersActive = computed(
  () =>
    !!(
      query.value ||
      category.value !== "all" ||
      status.value ||
      researchDirty.value ||
      Object.values(research.value).some(Boolean)
    ),
);

const toast = ref("");
let toastTimer = null;
let pollTimer = null;
let searchTimer = null;
let resultsRequest;
let graphRequest;

const showToast = (text) => {
  toast.value = text;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (toast.value = ""), 3000);
};

const loadResults = async (background = false) => {
  if (background && resultsRequest) return;
  resultsRequest?.abort();
  const request = new AbortController();
  resultsRequest = request;
  if (!background) loading.value = true;
  resultError.value = "";
  const params = new URLSearchParams({ page: page.value, limit: 30 });
  try {
    const response = await apiFetch(`/api/browse?${params}`, {
      method: "POST",
      json: currentCriteria.value,
      signal: request.signal,
    });
    const data = await readJSON(response);
    if (resultsRequest !== request) return;
    if (!response.ok) {
      resultError.value = data.error || "Could not load results.";
      return;
    }
    nodes.value = data || [];
    hasMore.value = response.headers.get("X-Has-More") === "true";
  } catch (err) {
    if (err.name !== "AbortError" && resultsRequest === request) {
      resultError.value = "Could not reach the server.";
    }
  } finally {
    if (resultsRequest === request) {
      resultsRequest = null;
      loading.value = false;
    }
  }
};

const updateResults = () => {
  clearTimeout(searchTimer);
  page.value = 1;
  hasMore.value = false;
  nodes.value = [];
  searching.value = Boolean(query.value.trim());
  return loadResults();
};

const scheduleSearch = () => {
  clearTimeout(searchTimer);
  resultsRequest?.abort();
  if (!query.value.trim()) return updateResults();
  searchTimer = setTimeout(updateResults, 350);
};

const clearSearch = () => {
  query.value = "";
  updateResults();
};

const applyFilters = (criteria) => {
  researchReset.value++;
  researchDirty.value = false;
  clearTimeout(searchTimer);
  const nextCategory = criteria.category || "all";
  const nextStatus = criteria.status || "";
  const filtersChanged =
    nextCategory !== category.value ||
    nextStatus !== status.value ||
    JSON.stringify(research.value) !==
      JSON.stringify(
        Object.fromEntries(
          Object.keys(emptyResearchFilters()).map((key) => [
            key,
            criteria[key] ?? emptyResearchFilters()[key],
          ]),
        ),
      );
  query.value = criteria.q || "";
  category.value = nextCategory;
  status.value = nextStatus;
  const nextResearch = Object.fromEntries(
    Object.keys(emptyResearchFilters()).map((key) => [
      key,
      criteria[key] ?? emptyResearchFilters()[key],
    ]),
  );
  if (JSON.stringify(research.value) !== JSON.stringify(nextResearch))
    research.value = nextResearch;
  // The filter watcher batches category/status changes into one request.
  // A query-only change needs the explicit update instead.
  if (!filtersChanged) updateResults();
};

const changePage = (delta) => {
  page.value += delta;
  nodes.value = [];
  loadResults();
};

const loadGraph = async () => {
  graphRequest?.abort();
  const request = new AbortController();
  graphRequest = request;
  graphLoading.value = true;
  graphError.value = "";
  try {
    const [nodePage, edgePage] = await Promise.all([
      apiJSON("/api/nodes", { signal: request.signal }),
      apiJSON("/api/edges", { signal: request.signal }),
    ]);
    if (graphRequest !== request) return;
    if (!nodePage.ok || !edgePage.ok) throw new Error("graph request failed");
    graphNodes.value = nodePage.data || [];
    edges.value = edgePage.data || [];
  } catch (err) {
    if (err.name !== "AbortError" && graphRequest === request) {
      graphError.value = "Could not load the network map.";
    }
  } finally {
    if (graphRequest === request) graphLoading.value = false;
  }
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

const loadCollections = async () => {
  const result = await apiJSON("/api/tags");
  if (result.ok) collections.value = result.data || [];
};
const annotationsChanged = async () => {
  await Promise.all([loadCollections(), refreshUnseenChanges()]);
  await updateResults();
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

watch([category, status, research], updateResults, { deep: true });
watch(view, (current) => {
  if (current === "graph") loadGraph();
  else graphRequest?.abort();
});

onMounted(() => {
  loadResults();
  loadCollections();
  pollTimer = setInterval(() => {
    if (
      view.value === "list" &&
      page.value === 1 &&
      !query.value.trim() &&
      !document.hidden
    ) {
      loadResults(true);
    }
  }, 10000);
});

onUnmounted(() => {
  clearInterval(pollTimer);
  clearTimeout(toastTimer);
  clearTimeout(searchTimer);
  resultsRequest?.abort();
  graphRequest?.abort();
});
</script>

<template>
  <div class="stack" style="gap: var(--s-5)">
    <!-- Submit -->
    <section class="card" aria-labelledby="crawl-heading">
      <div class="section-head">
        <h2 id="crawl-heading" class="section-title">Crawl a new address</h2>
        <RouterLink class="btn btn--ghost btn--sm" :to="{ name: 'queue' }">
          View queue
        </RouterLink>
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
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          placeholder="Search crawled content…"
          @input="scheduleSearch"
          maxlength="200"
          @keyup.enter="updateResults"
        />
        <button
          v-if="query"
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

      <div class="status-filter">
        <label class="label" for="status-filter">Crawl status</label>
        <select id="status-filter" v-model="status" class="field">
          <option value="">Any status</option>
          <option
            v-for="(label, value) in statuses"
            :key="value"
            :value="value"
          >
            {{ label }}
          </option>
        </select>
      </div>

      <ResearchFilters
        v-model="research"
        :collections="collections"
        :reset-version="researchReset"
        @dirty="researchDirty = $event"
      />

      <div class="filter-actions">
        <button
          class="btn btn--ghost btn--sm"
          type="button"
          :disabled="!filtersActive"
          @click="applyFilters({})"
        >
          Reset all filters
        </button>
      </div>
      <SavedSearches
        :criteria="currentCriteria"
        :disabled="researchDirty"
        @apply="applyFilters"
      />

      <div class="section-head section-head--tight">
        <h2 id="results-heading" class="section-title">
          {{ searching ? "Search results" : "Recently discovered" }}
          <span class="muted count">({{ nodes.length }})</span>
        </h2>
        <button
          class="btn btn--ghost btn--sm"
          type="button"
          @click="loadResults()"
        >
          Refresh
        </button>
      </div>

      <p v-if="resultError" class="msg msg--error" role="alert">
        {{ resultError }}
      </p>
      <NodeList
        v-else
        :nodes="nodes"
        :loading="loading"
        :empty-message="
          filtersActive
            ? 'No sites match these filters. Adjust them or choose Reset all filters.'
            : undefined
        "
        @annotate="openAnnotator"
        @inspect="inspecting = $event.url"
      />
      <nav v-if="page > 1 || hasMore" class="pager" aria-label="Results pages">
        <button
          class="btn btn--secondary"
          type="button"
          :disabled="page === 1 || loading"
          @click="changePage(-1)"
        >
          Previous
        </button>
        <span class="muted" aria-live="polite">Page {{ page }}</span>
        <button
          class="btn btn--secondary"
          type="button"
          :disabled="!hasMore || loading"
          @click="changePage(1)"
        >
          Next
        </button>
      </nav>
    </section>

    <!-- Graph -->
    <template v-else>
      <p v-if="graphLoading" class="muted">Loading map…</p>
      <p v-else-if="graphError" class="msg msg--error" role="alert">
        {{ graphError }}
      </p>
      <GraphPanel
        v-else
        :nodes="graphNodes"
        :edges="edges"
        @notice="showToast"
      />
    </template>

    <NodeInspector :url="inspecting" @close="inspecting = ''" />

    <NodeAnnotator
      :url="annotating.url"
      :title="annotating.title"
      @close="closeAnnotator"
      @changed="annotationsChanged"
    />

    <Transition name="toast">
      <p v-if="toast" class="toast" role="status">{{ toast }}</p>
    </Transition>
  </div>
</template>

<style scoped>
.status-filter {
  margin-top: var(--s-3);
  max-width: 14rem;
}

.pager {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: var(--s-3);
  margin-top: var(--s-4);
}

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
