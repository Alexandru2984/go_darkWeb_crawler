<script setup>
import { computed, ref, watch, onUnmounted } from "vue";
import { apiJSON } from "../lib/api.js";
const emit = defineEmits(["changed"]);
const text = ref(""),
  preview = ref(null),
  committed = ref(null),
  busy = ref(false),
  error = ref("");
const request = new AbortController();
const lines = computed(() =>
  text.value
    .split(/\r?\n/)
    .map((s) => s.trim())
    .filter(Boolean),
);
watch(text, () => {
  preview.value = null;
  committed.value = null;
  error.value = "";
});
const labels = {
  accepted: "Ready to queue",
  queued: "Queued",
  invalid: "Invalid onion URL",
  duplicate: "Duplicate in this batch",
  already_exists: "Already in your graph",
  blocked: "Domain blocked",
  quota_full: "Submission limit reached",
};
const items = computed(() =>
  (preview.value?.items || []).map((item) => {
    const actual = committed.value?.items.find((row) => row.url === item.url);
    return {
      ...item,
      result:
        item.result === "accepted" && actual
          ? actual.result === "accepted"
            ? "queued"
            : actual.result
          : item.result,
    };
  }),
);
const inspect = async () => {
  if (busy.value || !lines.value.length || lines.value.length > 20) return;
  busy.value = true;
  error.value = "";
  try {
    const result = await apiJSON("/api/crawl/bulk/preview", {
      method: "POST",
      json: { urls: lines.value },
      signal: request.signal,
    });
    if (result.ok) preview.value = result.data;
    else error.value = result.data.error || "Could not preview the import.";
  } catch (err) {
    if (err.name !== "AbortError")
      error.value = "Could not preview the import.";
  } finally {
    busy.value = false;
  }
};
const accept = async () => {
  if (busy.value || committed.value || !preview.value?.added) return;
  busy.value = true;
  error.value = "";
  const urls = preview.value.items
    .filter((i) => i.result === "accepted")
    .map((i) => i.url);
  try {
    const result = await apiJSON("/api/crawl/bulk", {
      method: "POST",
      json: { urls },
      signal: request.signal,
    });
    if (
      (result.ok || result.status === 429) &&
      Array.isArray(result.data.items)
    ) {
      committed.value = result.data;
      emit("changed");
    } else error.value = result.data.error || "Could not queue these URLs.";
  } catch (err) {
    if (err.name !== "AbortError") error.value = "Could not queue these URLs.";
  } finally {
    busy.value = false;
  }
};
onUnmounted(() => request.abort());
</script>
<template>
  <details class="card batch">
    <summary>Import a batch of onion URLs</summary>
    <p class="hint muted">
      Paste up to 20 URLs, one per line. Preview checks validity, duplicate
      records, blocked domains and remaining submission capacity. It does not
      start a crawl.
    </p>
    <form class="stack" @submit.prevent="inspect">
      <label class="label" for="batch-urls">Onion URLs</label>
      <textarea
        id="batch-urls"
        v-model="text"
        class="field mono"
        rows="5"
        maxlength="43520"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        :readonly="busy"
        placeholder="http://…onion/"
      />
      <p class="hint muted" aria-live="polite">{{ lines.length }} / 20 URLs</p>
      <p v-if="lines.length > 20" class="msg msg--error" role="alert">
        Split this list into batches of up to 20 URLs.
      </p>
      <button
        class="btn btn--secondary preview"
        type="submit"
        :disabled="busy || !lines.length || lines.length > 20"
      >
        {{ busy ? "Checking…" : "Preview import" }}
      </button>
    </form>
    <p v-if="error" class="msg msg--error" role="alert">{{ error }}</p>
    <template v-if="preview">
      <p v-if="committed" class="msg msg--ok" role="status">
        Queued {{ committed.added }} URL{{ committed.added === 1 ? "" : "s" }}.
        {{ committed.skipped }} skipped after rechecking. Other preview
        exclusions were kept out of the submission.
      </p>
      <p v-else class="hint muted">
        {{ preview.added }} ready, {{ preview.skipped }} excluded. Only URLs
        marked ready will be submitted. Policy and available capacity are
        checked again when you confirm.
      </p>
      <ol class="results" aria-label="Import preview">
        <li v-for="item in items" :key="item.line">
          <div class="url">
            <span class="muted">Entry {{ item.line }}</span
            ><code v-if="item.url">{{ item.url }}</code>
          </div>
          <strong>{{ labels[item.result] || item.result }}</strong>
        </li>
      </ol>
      <button
        class="btn"
        type="button"
        :disabled="busy || !!committed || !preview.added"
        @click="accept"
      >
        Queue accepted URLs
      </button>
    </template>
  </details>
</template>
<style scoped>
summary {
  min-height: var(--tap);
  cursor: pointer;
  font-weight: 650;
}
.hint {
  font-size: 0.85rem;
  margin: var(--s-3) 0;
}
.preview {
  justify-self: start;
}
textarea {
  resize: vertical;
  min-height: 120px;
  max-height: 450px;
}
.results {
  display: grid;
  gap: var(--s-2);
  list-style: none;
  padding: 0;
  margin: var(--s-3) 0;
}
.results li {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
  justify-content: space-between;
  padding: var(--s-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  font-size: 0.82rem;
}
.url {
  display: grid;
  gap: var(--s-1);
  min-width: 0;
  flex: 1 1 220px;
}
.url code {
  overflow-wrap: anywhere;
  white-space: normal;
}
.batch > .btn {
  max-width: 100%;
  white-space: normal;
}
</style>
