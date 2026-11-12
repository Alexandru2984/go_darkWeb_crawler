<script setup>
import { nextTick, onMounted, onUnmounted, ref } from "vue";
import { apiJSON } from "../lib/api.js";
import { categoryLabel } from "../lib/categories.js";
import { CRAWL_STATUSES, RESULT_SORTS } from "../lib/searchFilters.js";

const props = defineProps({
  criteria: { type: Object, required: true },
  disabled: { type: Boolean, default: false },
});
const emit = defineEmits(["apply"]);
const searches = ref([]);
const limit = ref(50);
const name = ref("");
const ready = ref(false);
const loading = ref(false);
const busy = ref(false);
const message = ref("");
const kind = ref("info");
const editing = ref(0);
const renameValue = ref("");
const deleting = ref(0);
const request = new AbortController();

const say = (text, type = "info") => {
  message.value = text;
  kind.value = type;
};
const describe = (criteria) =>
  [
    criteria.q ? `“${criteria.q}”` : "All content",
    criteria.category ? categoryLabel(criteria.category) : "All categories",
    CRAWL_STATUSES[criteria.status] || "Any status",
    ...(criteria.title ? [`Title: ${criteria.title}`] : []),
    ...(criteria.host ? [`Domain: ${criteria.host}`] : []),
    ...(criteria.discovered_from ? [`From ${criteria.discovered_from}`] : []),
    ...(criteria.discovered_to ? [`Through ${criteria.discovered_to}`] : []),
    ...(criteria.tag ? [`Collection: ${criteria.tag}`] : []),
    ...(criteria.sort ? [RESULT_SORTS[criteria.sort]] : []),
  ].join(" · ");

const load = async () => {
  loading.value = true;
  try {
    const result = await apiJSON("/api/saved-searches", {
      signal: request.signal,
    });
    ready.value = result.ok;
    if (result.ok) {
      searches.value = result.data.searches || [];
      limit.value = result.data.limit || 50;
    } else say(result.data.error || "Could not load saved searches.", "error");
  } catch (error) {
    if (error.name !== "AbortError")
      say("Could not load saved searches.", "error");
  } finally {
    loading.value = false;
  }
};

const save = async () => {
  if (props.disabled || busy.value || !ready.value || !name.value.trim())
    return;
  busy.value = true;
  const result = await apiJSON("/api/saved-searches", {
    method: "POST",
    json: { name: name.value.trim(), ...props.criteria },
  });
  busy.value = false;
  if (!result.ok)
    return say(result.data.error || "Could not save this search.", "error");
  searches.value = [result.data, ...searches.value];
  name.value = "";
  say(
    "Search saved. It is available on your other devices when you sign in.",
    "ok",
  );
};
const beginRename = async (saved) => {
  deleting.value = 0;
  editing.value = saved.id;
  renameValue.value = saved.name;
  await nextTick();
  document.getElementById(`saved-name-${saved.id}`)?.focus();
};
const rename = async (saved) => {
  if (busy.value || !renameValue.value.trim()) return;
  busy.value = true;
  const result = await apiJSON(`/api/saved-searches/${saved.id}`, {
    method: "POST",
    json: { name: renameValue.value.trim() },
  });
  busy.value = false;
  if (!result.ok)
    return say(result.data.error || "Could not rename this search.", "error");
  searches.value = [
    result.data,
    ...searches.value.filter((item) => item.id !== saved.id),
  ];
  editing.value = 0;
  renameValue.value = "";
  say("Saved search renamed.", "ok");
};
const remove = async (saved) => {
  if (busy.value) return;
  busy.value = true;
  const result = await apiJSON(`/api/saved-searches/${saved.id}`, {
    method: "DELETE",
  });
  busy.value = false;
  if (!result.ok)
    return say(result.data.error || "Could not delete this search.", "error");
  searches.value = searches.value.filter((item) => item.id !== saved.id);
  deleting.value = 0;
  say("Saved search deleted. Crawl records are kept.", "ok");
};
onMounted(load);
onUnmounted(() => request.abort());
</script>

<template>
  <details class="saved-searches">
    <summary>
      Saved searches
      <span v-if="ready" class="muted"
        >({{ searches.length }}/{{ limit }})</span
      >
    </summary>
    <div class="stack saved-body">
      <p class="muted small">
        Private to your account. Searches are saved only when you choose Save
        and are kept until you delete them. They are included in your personal
        data export.
      </p>
      <p v-if="message" class="msg" :class="`msg--${kind}`" role="status">
        {{ message }}
      </p>
      <p v-if="loading" class="muted" role="status">Loading saved searches…</p>
      <button
        v-if="!ready && !loading"
        class="btn btn--ghost btn--sm"
        type="button"
        @click="load"
      >
        Retry saved searches
      </button>
      <template v-if="ready">
        <p v-if="disabled" class="muted small">
          Apply your filter changes before saving this search.
        </p>
        <form class="stack" @submit.prevent="save">
          <p class="preview small">
            <strong>Current search:</strong> {{ describe(criteria) }}
          </p>
          <div class="save-row">
            <div class="name-field">
              <label class="label" for="saved-search-name"
                >Name this search</label
              >
              <input
                id="saved-search-name"
                v-model="name"
                class="field"
                maxlength="80"
                type="text"
                autocomplete="off"
                autocapitalize="off"
                spellcheck="false"
                required
                :disabled="
                  disabled || busy || loading || searches.length >= limit
                "
              />
            </div>
            <button
              class="btn btn--secondary"
              type="submit"
              :disabled="
                disabled ||
                busy ||
                loading ||
                !name.trim() ||
                searches.length >= limit
              "
            >
              Save search
            </button>
          </div>
        </form>
        <p v-if="searches.length >= limit" class="muted small">
          Your {{ limit }} saved search slots are full. Delete an entry to save
          another.
        </p>
        <p v-if="!searches.length" class="muted">
          No saved searches yet. Choose your filters, give the search a name and
          save it here.
        </p>
        <ul v-else class="saved-list">
          <li v-for="saved in searches" :key="saved.id" class="saved-item">
            <form
              v-if="editing === saved.id"
              class="stack"
              @submit.prevent="rename(saved)"
            >
              <div>
                <label class="label" :for="`saved-name-${saved.id}`"
                  >New search name</label
                >
                <input
                  :id="`saved-name-${saved.id}`"
                  v-model="renameValue"
                  class="field"
                  type="text"
                  maxlength="80"
                  autocomplete="off"
                  autocapitalize="off"
                  spellcheck="false"
                  required
                  :disabled="busy"
                />
              </div>
              <div class="actions">
                <button
                  class="btn btn--secondary btn--sm"
                  type="submit"
                  :disabled="busy || !renameValue.trim()"
                >
                  Save name
                </button>
                <button
                  class="btn btn--ghost btn--sm"
                  type="button"
                  :disabled="busy"
                  @click="editing = 0"
                >
                  Cancel rename
                </button>
              </div>
            </form>
            <template v-else>
              <div class="saved-heading">
                <strong class="saved-name">{{ saved.name }}</strong>
                <button
                  class="btn btn--secondary btn--sm"
                  type="button"
                  :disabled="busy || loading"
                  :aria-label="`Apply ${saved.name}`"
                  @click="emit('apply', saved)"
                >
                  Apply
                </button>
              </div>
              <p class="preview muted small">{{ describe(saved) }}</p>
              <div v-if="deleting === saved.id" class="stack">
                <p class="small">
                  Delete this saved search? Your crawl records are kept.
                </p>
                <div class="actions">
                  <button
                    class="btn btn--danger btn--sm"
                    type="button"
                    :disabled="busy"
                    :aria-label="`Confirm delete ${saved.name}`"
                    @click="remove(saved)"
                  >
                    Confirm delete
                  </button>
                  <button
                    class="btn btn--ghost btn--sm"
                    type="button"
                    :disabled="busy"
                    @click="deleting = 0"
                  >
                    Cancel delete
                  </button>
                </div>
              </div>
              <div v-else class="actions">
                <button
                  class="btn btn--ghost btn--sm"
                  type="button"
                  :disabled="busy || loading"
                  :aria-label="`Rename ${saved.name}`"
                  @click="beginRename(saved)"
                >
                  Rename
                </button>
                <button
                  class="btn btn--ghost btn--sm"
                  type="button"
                  :disabled="busy || loading"
                  :aria-label="`Delete ${saved.name}`"
                  @click="deleting = saved.id"
                >
                  Delete
                </button>
              </div>
            </template>
          </li>
        </ul>
        <button
          class="btn btn--ghost btn--sm refresh"
          type="button"
          :disabled="busy || loading"
          @click="load"
        >
          Refresh saved searches
        </button>
      </template>
    </div>
  </details>
</template>

<style scoped>
.saved-searches {
  margin: var(--s-4) 0;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  min-width: 0;
}
summary {
  padding: var(--s-3);
  cursor: pointer;
  font-weight: 600;
  min-height: var(--tap);
}
.saved-body {
  padding: 0 var(--s-3) var(--s-3);
}
.small {
  font-size: 0.85rem;
}
.preview,
.saved-name {
  overflow-wrap: anywhere;
  min-width: 0;
}
.save-row,
.saved-heading,
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
  align-items: center;
}
.save-row {
  align-items: flex-end;
}
.name-field {
  flex: 1 1 200px;
  min-width: 0;
}
.saved-list {
  list-style: none;
  padding: 0;
  margin: 0;
}
.saved-item {
  padding: var(--s-3) 0;
  border-top: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  gap: var(--s-2);
}
.saved-heading {
  justify-content: space-between;
}
.saved-name {
  flex: 1 1 130px;
}
.btn {
  max-width: 100%;
  white-space: normal;
  overflow-wrap: anywhere;
}
.refresh {
  align-self: flex-start;
}
</style>
