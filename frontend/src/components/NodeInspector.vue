<script setup>
import { computed, nextTick, onUnmounted, ref, watch } from "vue";
import { apiJSON } from "../lib/api.js";
import { categoryLabel } from "../lib/categories.js";

const props = defineProps({ url: { type: String, default: "" } });
const emit = defineEmits(["close"]);
const dialog = ref(null);
const node = ref(null);
const loading = ref(false);
const error = ref("");
const notice = ref("");
let request;

const metadata = computed(() => {
  try {
    return JSON.parse(node.value?.metadata || "{}") || {};
  } catch {
    return {};
  }
});

const close = () => emit("close");

async function load(url) {
  const controller = new AbortController();
  request = controller;
  loading.value = true;
  try {
    const { ok, data } = await apiJSON("/api/node", {
      method: "POST",
      json: { url },
      signal: controller.signal,
    });
    if (request !== controller) return;
    if (ok) node.value = data;
    else error.value = data.error || "Could not load the crawl record.";
  } catch (err) {
    if (err.name !== "AbortError" && request === controller) {
      error.value = "Could not reach the server.";
    }
  } finally {
    if (request === controller) loading.value = false;
  }
}

async function copyAddress() {
  try {
    await navigator.clipboard.writeText(props.url);
    notice.value = "Address copied.";
  } catch {
    notice.value = "Select the address above and copy it.";
  }
}

watch(
  () => props.url,
  async (url) => {
    request?.abort();
    request = null;
    node.value = null;
    error.value = "";
    notice.value = "";
    if (!url) {
      dialog.value?.close();
      return;
    }
    await nextTick();
    if (props.url !== url) return;
    if (!dialog.value?.open) dialog.value?.showModal();
    load(url);
  },
  { immediate: true },
);

onUnmounted(() => request?.abort());
</script>

<template>
  <dialog
    ref="dialog"
    class="inspector"
    aria-labelledby="record-title"
    @close="close"
    @cancel="close"
  >
    <header class="inspector-header">
      <h2 id="record-title">Crawl record</h2>
      <button class="btn btn--ghost" type="button" @click="close">Close</button>
    </header>
    <p class="address mono">{{ url }}</p>
    <button
      class="btn btn--secondary btn--sm"
      type="button"
      @click="copyAddress"
    >
      Copy address
    </button>
    <p v-if="notice" class="muted" role="status">{{ notice }}</p>
    <p v-if="loading" class="muted" role="status">Loading record…</p>
    <p v-else-if="error" class="msg msg--error" role="alert">{{ error }}</p>
    <template v-else-if="node">
      <h3>{{ node.title || "Untitled" }}</h3>
      <p v-if="metadata.description" class="description">
        {{ metadata.description }}
      </p>
      <dl class="record-fields">
        <div>
          <dt>Crawl status</dt>
          <dd>{{ node.processing_status }}</dd>
        </div>
        <div>
          <dt>HTTP response</dt>
          <dd>{{ node.status_code || "Unavailable" }}</dd>
        </div>
        <div>
          <dt>Category</dt>
          <dd>{{ categoryLabel(node.category) }}</dd>
        </div>
        <div>
          <dt>Server</dt>
          <dd>{{ node.server_header || "Unavailable" }}</dd>
        </div>
        <div>
          <dt>First seen</dt>
          <dd>{{ node.discovered_at || "Unavailable" }} UTC</dd>
        </div>
        <div>
          <dt>Last crawled</dt>
          <dd>
            {{
              node.last_crawled_at
                ? node.last_crawled_at + " UTC"
                : "Awaiting first crawl"
            }}
          </dd>
        </div>
      </dl>
      <details v-if="node.content">
        <summary>Stored page text</summary>
        <pre class="page-text">{{ node.content }}</pre>
      </details>
      <p v-else class="muted">No page text is stored for this record.</p>
      <details v-if="node.content_hash">
        <summary>Content fingerprint</summary>
        <code class="fingerprint">{{ node.content_hash }}</code>
      </details>
    </template>
  </dialog>
</template>

<style scoped>
.inspector {
  width: min(760px, calc(100vw - 24px));
  max-height: calc(100svh - 32px);
  padding: var(--s-5);
  color: var(--text);
  background: var(--surface);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
}

.inspector::backdrop {
  background: rgb(0 0 0 / 75%);
}

.inspector-header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--s-2);
}

.inspector-header h2 {
  font-size: 1.25rem;
}

.address,
.fingerprint {
  overflow-wrap: anywhere;
  font-size: 0.8rem;
}

.inspector h3 {
  margin-top: var(--s-5);
}

.description {
  color: var(--text-dim);
}

.record-fields {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 190px), 1fr));
  gap: var(--s-4);
  margin: var(--s-5) 0;
}

.record-fields dt {
  font-size: 0.8rem;
  color: var(--text-dim);
}
.record-fields dd {
  margin: var(--s-1) 0 0;
  overflow-wrap: anywhere;
}

details {
  padding: var(--s-3) 0;
  border-top: 1px solid var(--border);
}

summary {
  cursor: pointer;
  min-height: var(--tap);
  display: list-item;
  align-content: center;
}
.fingerprint {
  display: block;
  padding: var(--s-3) 0;
}

.page-text {
  max-height: 360px;
  overflow: auto;
  padding: var(--s-3);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  background: var(--bg);
  font: 0.85rem/1.65 var(--sans);
  border-radius: var(--radius-sm);
}
</style>
