<script setup>
import { ref, watch as vueWatch, nextTick } from "vue";
import { apiJSON } from "../lib/api.js";

const props = defineProps({
  /** The site being annotated. Empty closes the dialog. */
  url: { type: String, default: "" },
  title: { type: String, default: "" },
});
const emit = defineEmits(["close", "changed"]);

// A native <dialog> rather than a hand-built overlay: it gives focus trapping,
// Escape to close, inert background and correct semantics for a screen reader
// without any of that being reimplemented here, badly.
const dialogEl = ref(null);

const annotation = ref({ tags: [], note: "", watch: null });
const newTag = ref("");
const noteDraft = ref("");
const watchInterval = ref(1);
const loading = ref(false);
const busy = ref(false);
const error = ref("");

const load = async () => {
  loading.value = true;
  error.value = "";
  const { ok, data } = await apiJSON("/api/annotations", {
    method: "POST",
    json: { url: props.url },
  });
  loading.value = false;
  if (!ok) {
    error.value = data.error || "Could not load your notes for this site.";
    return;
  }
  apply(data);
};

const apply = (data) => {
  annotation.value = data;
  noteDraft.value = data.note || "";
  watchInterval.value = data.watch?.interval_days || 1;
};

const send = async (path, json) => {
  busy.value = true;
  error.value = "";
  const { ok, data } = await apiJSON(path, { method: "POST", json });
  busy.value = false;
  if (!ok) {
    error.value = data.error || "That did not work.";
    return null;
  }
  emit("changed");
  return data;
};

const addTag = async () => {
  const tag = newTag.value.trim();
  if (!tag) return;
  const data = await send("/api/annotations/tag", { url: props.url, tag });
  if (data) {
    apply(data);
    newTag.value = "";
  }
};

const removeTag = async (tag) => {
  const data = await send("/api/annotations/tag", {
    url: props.url,
    tag,
    remove: true,
  });
  if (data) apply(data);
};

const saveNote = async () => {
  const data = await send("/api/annotations/note", {
    url: props.url,
    body: noteDraft.value,
  });
  if (data) apply(data);
};

const startWatch = async () => {
  const data = await send("/api/watch", {
    url: props.url,
    interval_days: Number(watchInterval.value) || 1,
  });
  if (data) annotation.value = { ...annotation.value, watch: data.watch };
};

const stopWatch = async () => {
  const data = await send("/api/watch", { url: props.url, stop: true });
  if (data) annotation.value = { ...annotation.value, watch: null };
};

const close = () => emit("close");

vueWatch(
  () => props.url,
  async (url) => {
    if (!url) {
      dialogEl.value?.close();
      return;
    }
    annotation.value = { tags: [], note: "", watch: null };
    await nextTick();
    if (!dialogEl.value?.open) dialogEl.value?.showModal();
    load();
  },
  { immediate: true },
);
</script>

<template>
  <dialog ref="dialogEl" class="sheet" @close="close" @cancel="close">
    <div class="sheet__head">
      <div class="sheet__id">
        <h2 class="sheet__title">{{ title || "Untitled" }}</h2>
        <p class="sheet__url mono">{{ url }}</p>
      </div>
      <button class="btn btn--ghost btn--sm" type="button" @click="close">
        Close
      </button>
    </div>

    <p class="hint muted">
      Tags and notes are yours alone. Another account tracking the same site
      sees none of this.
    </p>

    <p v-if="error" class="msg msg--error" role="alert">{{ error }}</p>
    <p v-if="loading" class="muted">Loading…</p>

    <template v-else>
      <section class="block">
        <h3 class="block__title">Tags</h3>
        <ul v-if="annotation.tags.length" class="tags">
          <li v-for="tag in annotation.tags" :key="tag" class="tagchip">
            <span>{{ tag }}</span>
            <button
              class="tagchip__x"
              type="button"
              :disabled="busy"
              :aria-label="`Remove tag ${tag}`"
              @click="removeTag(tag)"
            >
              ×
            </button>
          </li>
        </ul>
        <p v-else class="muted small">No tags yet.</p>

        <form class="inline" @submit.prevent="addTag">
          <label class="sr-only" for="new-tag">New tag</label>
          <input
            id="new-tag"
            v-model="newTag"
            class="field"
            maxlength="40"
            placeholder="marketplace, forum, watchlist…"
          />
          <button class="btn btn--sm" type="submit" :disabled="busy || !newTag">
            Add
          </button>
        </form>
      </section>

      <section class="block">
        <h3 class="block__title">Note</h3>
        <label class="sr-only" for="note-body">Private note</label>
        <textarea
          id="note-body"
          v-model="noteDraft"
          class="field textarea"
          rows="4"
          maxlength="4000"
          placeholder="Why this site matters to you."
        ></textarea>
        <div class="row">
          <button
            class="btn btn--sm"
            type="button"
            :disabled="busy"
            @click="saveNote"
          >
            Save note
          </button>
          <span class="muted small">{{ noteDraft.length }} / 4000</span>
        </div>
      </section>

      <section class="block">
        <h3 class="block__title">Watch for changes</h3>
        <template v-if="annotation.watch">
          <p class="small">
            Checking every {{ annotation.watch.interval_days }} day{{
              annotation.watch.interval_days === 1 ? "" : "s"
            }}.
            <span v-if="annotation.watch.last_checked_at" class="muted">
              Last checked {{ annotation.watch.last_checked_at }}.
            </span>
          </p>
          <button
            class="btn btn--danger btn--sm"
            type="button"
            :disabled="busy"
            @click="stopWatch"
          >
            Stop watching
          </button>
        </template>
        <template v-else>
          <p class="muted small">
            Changes appear in your feed after the next crawl. Nothing is emailed
            — a message saying which site you are watching would tell your mail
            provider exactly that.
          </p>
          <div class="inline">
            <label class="sr-only" for="watch-interval">Check every</label>
            <input
              id="watch-interval"
              v-model="watchInterval"
              class="field field--num"
              type="number"
              min="1"
              max="365"
              inputmode="numeric"
            />
            <span class="unit">days</span>
            <button
              class="btn btn--sm"
              type="button"
              :disabled="busy"
              @click="startWatch"
            >
              Watch
            </button>
          </div>
        </template>
      </section>
    </template>
  </dialog>
</template>

<style scoped>
.sheet {
  width: min(560px, calc(100vw - 2 * var(--s-4)));
  max-height: 85svh;
  overflow-y: auto;
  padding: var(--s-5);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  background: var(--surface);
  color: var(--text);
}
.sheet::backdrop {
  background: rgba(0, 0, 0, 0.66);
}

.sheet__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--s-3);
  margin-bottom: var(--s-3);
}
.sheet__id {
  min-width: 0;
}
.sheet__title {
  font-size: 1.05rem;
  margin: 0;
}
.sheet__url {
  margin: var(--s-1) 0 0;
  font-size: 0.76rem;
  color: var(--text-faint);
  overflow-wrap: anywhere;
}

.hint {
  font-size: 0.82rem;
  margin: 0 0 var(--s-4);
}
.small {
  font-size: 0.85rem;
}

.block {
  padding-top: var(--s-4);
  margin-top: var(--s-4);
  border-top: 1px solid var(--border);
}
.block:first-of-type {
  padding-top: 0;
  margin-top: 0;
  border-top: 0;
}
.block__title {
  font-size: 0.9rem;
  margin: 0 0 var(--s-3);
}

.tags {
  list-style: none;
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
  margin: 0 0 var(--s-3);
  padding: 0;
}
.tagchip {
  display: inline-flex;
  align-items: center;
  gap: var(--s-1);
  padding: 4px 4px 4px 10px;
  border-radius: var(--radius-pill);
  background: var(--surface-3);
  font-size: 0.82rem;
}
.tagchip__x {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--text-dim);
  font-size: 1rem;
  line-height: 1;
  cursor: pointer;
}
.tagchip__x:hover {
  background: var(--surface-2);
  color: var(--text);
}

.inline {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--s-2);
}
.inline .field {
  flex: 1 1 160px;
}
.field--num {
  flex: 0 0 90px;
}
.unit {
  color: var(--text-dim);
  font-size: 0.9rem;
}

.textarea {
  width: 100%;
  resize: vertical;
  font-family: inherit;
  margin-bottom: var(--s-3);
}

.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--s-3);
}
</style>
