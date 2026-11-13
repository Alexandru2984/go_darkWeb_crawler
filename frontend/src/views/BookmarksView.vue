<script setup>
import { ref, onMounted, onUnmounted } from "vue";
import { apiJSON, apiFetch, readJSON } from "../lib/api.js";
import { emptyResearchFilters } from "../lib/searchFilters.js";
import ResearchFilters from "../components/ResearchFilters.vue";
import NodeList from "../components/NodeList.vue";
import NodeAnnotator from "../components/NodeAnnotator.vue";
import NodeInspector from "../components/NodeInspector.vue";

const filters = ref({
  ...emptyResearchFilters(),
  bookmarked_only: true,
  sort: "bookmarked",
});
const groups = ref([]),
  nodes = ref([]),
  query = ref(""),
  appliedQuery = ref(""),
  page = ref(1),
  hasMore = ref(false);
const loading = ref(false),
  busy = ref(false),
  error = ref(""),
  showNotes = ref(false),
  addURL = ref("");
const inspecting = ref(""),
  annotating = ref({ url: "", title: "" });
let request;
const lifetime = new AbortController();
const load = async () => {
  request?.abort();
  const controller = new AbortController();
  request = controller;
  loading.value = true;
  error.value = "";
  try {
    const response = await apiFetch(`/api/browse?page=${page.value}&limit=20`, {
      method: "POST",
      json: { ...filters.value, q: appliedQuery.value, bookmarked_only: true },
      signal: controller.signal,
    });
    const data = await readJSON(response);
    if (request !== controller) return;
    if (response.ok) {
      nodes.value = data || [];
      hasMore.value = response.headers.get("X-Has-More") === "true";
      if (page.value > 1 && !nodes.value.length) {
        page.value = 1;
        await load();
      }
    } else error.value = data.error || "Could not load bookmarks.";
  } catch (err) {
    if (err.name !== "AbortError") error.value = "Could not load bookmarks.";
  } finally {
    if (request === controller) loading.value = false;
  }
};
const loadGroups = async () => {
  try {
    const result = await apiJSON("/api/tags", { signal: lifetime.signal });
    if (result.ok) groups.value = result.data || [];
  } catch (err) {
    if (err.name !== "AbortError") error.value = "Could not load collections.";
  }
};
const apply = (value) => {
  filters.value = { ...value, bookmarked_only: true };
  page.value = 1;
  load();
};
const search = () => {
  appliedQuery.value = query.value.trim();
  page.value = 1;
  load();
};
const move = (delta) => {
  page.value += delta;
  load();
};
const add = async () => {
  if (busy.value) return;
  busy.value = true;
  const result = await apiJSON("/api/bookmarks", {
    method: "POST",
    json: { url: addURL.value.trim() },
  });
  busy.value = false;
  if (result.ok) {
    addURL.value = "";
    page.value = 1;
    await load();
  } else error.value = result.data.error || "Could not bookmark this site.";
};
const changed = async () => {
  await Promise.all([load(), loadGroups()]);
};
onMounted(() => {
  load();
  loadGroups();
});
onUnmounted(() => {
  request?.abort();
  lifetime.abort();
});
</script>

<template>
  <div class="stack workspace">
    <header>
      <h1>Private bookmarks</h1>
      <p class="muted">
        Keep a reading list, track reviews and find your notes. Up to 1,000
        bookmarks per account.
      </p>
    </header>
    <section class="card">
      <form class="add" @submit.prevent="add">
        <div>
          <label class="label" for="bookmark-url"
            >Bookmark one of your crawled sites</label
          ><input
            id="bookmark-url"
            v-model="addURL"
            class="field"
            maxlength="2048"
            placeholder="http://…onion/"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
          />
        </div>
        <button
          class="btn btn--secondary"
          type="submit"
          :disabled="busy || !addURL.trim()"
        >
          Add bookmark
        </button>
      </form>
      <p class="hint muted">
        Bookmarks follow your crawl retention and disappear when their crawl
        record is erased. Watching a site keeps it from automatic retention
        cleanup.
      </p>
    </section>
    <section class="card" aria-labelledby="bookmark-search-heading">
      <h2 id="bookmark-search-heading">Your reading list</h2>
      <form class="add" @submit.prevent="search">
        <div>
          <label class="label" for="bookmark-query"
            >Search stored page content</label
          ><input
            id="bookmark-query"
            v-model="query"
            class="field"
            type="search"
            maxlength="200"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
          />
        </div>
        <button class="btn" type="submit" :disabled="loading">Search</button>
      </form>
      <ResearchFilters
        bookmarks-only
        :model-value="filters"
        :collections="groups"
        :disabled="loading"
        @update:model-value="apply"
      />
      <label class="preview-toggle"
        ><input v-model="showNotes" type="checkbox" /> Show private note
        previews</label
      >
      <p v-if="error" class="msg msg--error" role="alert">{{ error }}</p>
      <NodeList
        v-else
        :nodes="nodes"
        :loading="loading"
        :show-notes="showNotes"
        empty-message="No bookmarks match. Bookmark a crawled site or adjust your filters."
        @inspect="inspecting = $event.url"
        @annotate="annotating = { url: $event.url, title: $event.title }"
      />
      <nav class="pagination" aria-label="Bookmark pages">
        <button
          class="btn btn--ghost"
          type="button"
          :disabled="loading || page === 1"
          @click="move(-1)"
        >
          Previous</button
        ><span aria-live="polite">Page {{ page }}</span
        ><button
          class="btn btn--ghost"
          type="button"
          :disabled="loading || !hasMore"
          @click="move(1)"
        >
          Next
        </button>
      </nav>
    </section>
    <NodeAnnotator
      :url="annotating.url"
      :title="annotating.title"
      @close="annotating = { url: '', title: '' }"
      @changed="changed"
    />
    <NodeInspector :url="inspecting" @close="inspecting = ''" />
  </div>
</template>
<style scoped>
.workspace {
  gap: var(--s-5);
  min-width: 0;
}
h1 {
  font-size: 1.5rem;
}
h2 {
  font-size: 1.1rem;
  margin-bottom: var(--s-3);
}
.add {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
  align-items: end;
}
.add > div {
  flex: 1 1 240px;
  min-width: 0;
}
.hint {
  font-size: 0.82rem;
  margin-top: var(--s-3);
}
.preview-toggle {
  display: flex;
  align-items: center;
  gap: var(--s-2);
  min-height: var(--tap);
  margin: var(--s-3) 0;
}
.pagination {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--s-2);
  margin-top: var(--s-3);
}
</style>
