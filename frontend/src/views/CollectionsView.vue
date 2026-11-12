<script setup>
import { onMounted, onUnmounted, ref } from "vue";
import { apiJSON, apiFetch, readJSON } from "../lib/api.js";
import NodeList from "../components/NodeList.vue";
import NodeInspector from "../components/NodeInspector.vue";
import NodeAnnotator from "../components/NodeAnnotator.vue";

const groups = ref([]),
  selected = ref(""),
  nodes = ref([]),
  page = ref(1),
  hasMore = ref(false);
const loading = ref(true),
  busy = ref(false),
  ready = ref(false),
  memberLoading = ref(false);
const newName = ref(""),
  firstURL = ref(""),
  addURL = ref(""),
  renameName = ref(""),
  renaming = ref(false),
  deleting = ref(false);
const message = ref(""),
  kind = ref("info"),
  memberError = ref("");
const inspecting = ref(""),
  annotating = ref({ url: "", title: "" });
let request;
let groupRequest;
const say = (text, type = "info") => {
  message.value = text;
  kind.value = type;
};

const loadGroups = async () => {
  groupRequest?.abort();
  const controller = new AbortController();
  groupRequest = controller;
  loading.value = true;
  try {
    const result = await apiJSON("/api/tags", { signal: controller.signal });
    if (groupRequest !== controller) return;
    ready.value = result.ok;
    if (result.ok) {
      groups.value = result.data || [];
      if (
        selected.value &&
        !groups.value.some((g) => g.tag === selected.value)
      ) {
        selected.value = "";
        nodes.value = [];
      }
    } else
      say(result.data.error || "Could not load your collections.", "error");
  } catch (error) {
    if (error.name !== "AbortError")
      say("Could not load your collections.", "error");
  } finally {
    if (groupRequest === controller) loading.value = false;
  }
};
const loadMembers = async () => {
  request?.abort();
  if (!selected.value) return;
  const controller = new AbortController();
  request = controller;
  memberLoading.value = true;
  memberError.value = "";
  try {
    const response = await apiFetch(`/api/browse?page=${page.value}&limit=20`, {
      method: "POST",
      json: { tag: selected.value },
      signal: controller.signal,
    });
    const data = await readJSON(response);
    if (request !== controller) return;
    if (response.ok) {
      nodes.value = data || [];
      hasMore.value = response.headers.get("X-Has-More") === "true";
      if (page.value > 1 && !nodes.value.length) {
        page.value = 1;
        await loadMembers();
      }
    } else memberError.value = data.error || "Could not load this collection.";
  } catch (error) {
    if (error.name !== "AbortError")
      memberError.value = "Could not load this collection.";
  } finally {
    if (request === controller) memberLoading.value = false;
  }
};
const open = async (tag) => {
  selected.value = tag;
  page.value = 1;
  renaming.value = false;
  deleting.value = false;
  addURL.value = "";
  await loadMembers();
};
const create = async () => {
  if (busy.value) return;
  busy.value = true;
  const result = await apiJSON("/api/collections/add", {
    method: "POST",
    json: { tag: newName.value, url: firstURL.value.trim() },
  });
  if (result.ok) {
    newName.value = "";
    firstURL.value = "";
    await loadGroups();
    await open(result.data.tag);
    say("Collection ready. You can add more of your crawled sites.", "ok");
  } else say(result.data.error || "Could not create the collection.", "error");
  busy.value = false;
};
const add = async () => {
  if (busy.value || !selected.value) return;
  busy.value = true;
  const result = await apiJSON("/api/collections/add", {
    method: "POST",
    json: { tag: selected.value, url: addURL.value.trim() },
  });
  if (result.ok) {
    addURL.value = "";
    await loadGroups();
    await loadMembers();
    say("Site added to your collection.", "ok");
  } else say(result.data.error || "Could not add this site.", "error");
  busy.value = false;
};
const rename = async () => {
  if (busy.value) return;
  busy.value = true;
  const result = await apiJSON("/api/collections/rename", {
    method: "POST",
    json: { tag: selected.value, name: renameName.value },
  });
  if (result.ok) {
    selected.value = result.data.tag;
    renaming.value = false;
    await loadGroups();
    say(result.data.message, "ok");
  } else say(result.data.error || "Could not rename this collection.", "error");
  busy.value = false;
};
const remove = async () => {
  if (busy.value) return;
  busy.value = true;
  const result = await apiJSON("/api/collections/delete", {
    method: "POST",
    json: { tag: selected.value },
  });
  if (result.ok) {
    selected.value = "";
    nodes.value = [];
    deleting.value = false;
    await loadGroups();
    say(result.data.message, "ok");
  } else say(result.data.error || "Could not remove this collection.", "error");
  busy.value = false;
};
const changed = async () => {
  await loadGroups();
  if (selected.value) await loadMembers();
};
const changePage = async (delta) => {
  page.value += delta;
  await loadMembers();
};
onMounted(loadGroups);
onUnmounted(() => {
  request?.abort();
  groupRequest?.abort();
});
</script>

<template>
  <div class="stack collections-page">
    <header>
      <h1 class="page-title">Private collections</h1>
      <p class="muted">
        Organise your crawled sites using personal tags. A site can belong to
        several collections. Removing the last member also removes that
        collection.
      </p>
    </header>
    <p v-if="message" class="msg" :class="`msg--${kind}`" role="alert">
      {{ message }}
    </p>
    <section class="card stack" aria-labelledby="create-collection-heading">
      <h2 id="create-collection-heading" class="section-title">
        Create a collection
      </h2>
      <p class="muted small">
        Start with a site already in your own crawl graph. Collection names use
        lowercase tags, up to 40 characters; each account can have 100
        collections.
      </p>
      <form class="stack" @submit.prevent="create">
        <div class="form-grid">
          <div>
            <label class="label" for="collection-name">Collection name</label
            ><input
              id="collection-name"
              v-model="newName"
              class="field"
              type="text"
              maxlength="40"
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
              :disabled="busy || !ready"
              required
            />
          </div>
          <div>
            <label class="label" for="collection-first-url"
              >First site URL</label
            ><input
              id="collection-first-url"
              v-model="firstURL"
              class="field"
              type="url"
              maxlength="2048"
              placeholder="http://…onion/"
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
              :disabled="busy || !ready"
              required
            />
          </div>
        </div>
        <button
          class="btn btn--secondary compact"
          type="submit"
          :disabled="busy || !ready || !newName.trim() || !firstURL.trim()"
        >
          Create collection
        </button>
      </form>
    </section>
    <div class="workspace">
      <section class="card stack" aria-labelledby="collection-list-heading">
        <div class="actions">
          <h2 id="collection-list-heading" class="section-title">
            Your collections
          </h2>
          <button
            class="btn btn--ghost btn--sm"
            type="button"
            :disabled="busy || loading"
            @click="changed"
          >
            Refresh collections
          </button>
        </div>
        <p v-if="loading" class="muted" role="status">Loading collections…</p>
        <button
          v-else-if="!ready"
          class="btn"
          type="button"
          @click="loadGroups"
        >
          Retry collections
        </button>
        <p v-else-if="!groups.length" class="muted">
          No collections yet. Create one above or add a tag from a site's notes
          dialog.
        </p>
        <ul v-else class="group-list">
          <li v-for="group in groups" :key="group.tag">
            <button
              class="group-button"
              type="button"
              :aria-label="`Open ${group.tag}`"
              :aria-pressed="selected === group.tag"
              :disabled="busy"
              @click="open(group.tag)"
            >
              <strong>{{ group.tag }}</strong
              ><span class="muted"
                >{{ group.count }}
                {{ group.count === 1 ? "site" : "sites" }}</span
              >
            </button>
          </li>
        </ul>
      </section>
      <section
        v-if="selected"
        class="card stack"
        aria-labelledby="selected-collection-heading"
      >
        <h2
          id="selected-collection-heading"
          class="section-title selected-name"
        >
          {{ selected }}
        </h2>
        <div class="actions">
          <button
            class="btn btn--ghost btn--sm"
            type="button"
            :disabled="busy"
            @click="
              renaming = !renaming;
              renameName = selected;
              deleting = false;
            "
          >
            Rename collection</button
          ><button
            class="btn btn--ghost btn--sm"
            type="button"
            :disabled="busy"
            @click="
              deleting = !deleting;
              renaming = false;
            "
          >
            Remove collection
          </button>
        </div>
        <form v-if="renaming" class="stack" @submit.prevent="rename">
          <label class="label" for="rename-collection"
            >New collection name</label
          ><input
            id="rename-collection"
            v-model="renameName"
            class="field"
            type="text"
            maxlength="40"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
            :disabled="busy"
            required
          />
          <div class="actions">
            <button
              class="btn btn--secondary btn--sm"
              type="submit"
              :disabled="busy || !renameName.trim()"
            >
              Save collection name</button
            ><button
              class="btn btn--ghost btn--sm"
              type="button"
              :disabled="busy"
              @click="renaming = false"
            >
              Cancel rename
            </button>
          </div>
        </form>
        <div v-if="deleting" class="stack">
          <p>
            Remove this collection from all its sites? Crawl records, notes and
            watches are kept. Saved searches using this collection will return
            no sites until you restore the tag or replace the saved search.
          </p>
          <div class="actions">
            <button
              class="btn btn--danger btn--sm"
              type="button"
              :disabled="busy"
              @click="remove"
            >
              Confirm remove collection</button
            ><button
              class="btn btn--ghost btn--sm"
              type="button"
              :disabled="busy"
              @click="deleting = false"
            >
              Cancel removal
            </button>
          </div>
        </div>
        <form class="stack add-member" @submit.prevent="add">
          <label class="label" for="collection-add-url"
            >Add a crawled site URL</label
          ><input
            id="collection-add-url"
            v-model="addURL"
            class="field"
            type="url"
            maxlength="2048"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
            :disabled="busy"
            required
          />
          <button
            class="btn btn--secondary btn--sm compact"
            type="submit"
            :disabled="busy || !addURL.trim()"
          >
            Add site
          </button>
        </form>
        <p class="muted small">
          Use “Tags, note &amp; watch” to add other collections or remove this
          tag from one site.
        </p>
        <p v-if="memberError" class="msg msg--error" role="alert">
          {{ memberError }}
        </p>
        <NodeList
          v-else
          :nodes="nodes"
          :loading="memberLoading"
          empty-message="No sites in this collection. Add a crawled site above."
          @inspect="inspecting = $event.url"
          @annotate="annotating = { url: $event.url, title: $event.title }"
        />
        <nav
          v-if="page > 1 || hasMore"
          class="actions"
          aria-label="Collection pages"
        >
          <button
            class="btn btn--ghost btn--sm"
            type="button"
            :disabled="memberLoading || page <= 1"
            @click="changePage(-1)"
          >
            Previous</button
          ><span class="muted">Page {{ page }}</span
          ><button
            class="btn btn--ghost btn--sm"
            type="button"
            :disabled="memberLoading || !hasMore"
            @click="changePage(1)"
          >
            Next
          </button>
        </nav>
      </section>
      <section v-else-if="groups.length" class="card">
        <p class="muted">Choose a collection to view its sites.</p>
      </section>
    </div>
    <NodeInspector :url="inspecting" @close="inspecting = ''" />
    <NodeAnnotator
      :url="annotating.url"
      :title="annotating.title"
      @close="annotating = { url: '', title: '' }"
      @changed="changed"
    />
  </div>
</template>

<style scoped>
.collections-page {
  gap: var(--s-5);
}
.workspace,
.form-grid {
  display: grid;
  gap: var(--s-4);
  min-width: 0;
}
.workspace > section,
.form-grid > div {
  min-width: 0;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--s-2);
}
.group-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--s-2);
  max-height: 600px;
  overflow: auto;
}
.group-button {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
  justify-content: space-between;
  align-items: center;
  min-height: var(--tap);
  width: 100%;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  color: var(--text);
  padding: var(--s-3);
  text-align: left;
  cursor: pointer;
}
.group-button[aria-pressed="true"] {
  border-color: var(--accent);
}
.group-button strong,
.selected-name {
  overflow-wrap: anywhere;
  min-width: 0;
}
.small {
  font-size: 0.85rem;
}
.compact {
  align-self: flex-start;
  justify-self: start;
}
.btn {
  white-space: normal;
  max-width: 100%;
  overflow-wrap: anywhere;
}
.add-member {
  padding-top: var(--s-3);
  border-top: 1px solid var(--border);
}
@media (min-width: 800px) {
  .form-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (min-width: 1200px) {
  .workspace {
    grid-template-columns: minmax(220px, 1fr) minmax(0, 3fr);
    align-items: start;
  }
}
</style>
