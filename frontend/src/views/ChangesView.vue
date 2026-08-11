<script setup>
import { ref, onMounted } from "vue";
import { apiJSON } from "../lib/api.js";
import { setUnseenChanges } from "../stores/changes.js";

const events = ref([]);
const watches = ref([]);
const unseen = ref(0);
const loading = ref(true);
const busy = ref(false);
const message = ref("");
const messageKind = ref("info");

const say = (text, kind = "info") => {
  message.value = text;
  messageKind.value = kind;
};

const KIND_LABEL = {
  content_changed: "Content changed",
  unreachable: "Stopped answering",
  recovered: "Answering again",
};

const load = async () => {
  loading.value = true;
  const [feed, list] = await Promise.all([
    apiJSON("/api/watch/events?limit=100"),
    apiJSON("/api/watches"),
  ]);
  if (feed.ok) {
    events.value = feed.data.events || [];
    unseen.value = feed.data.unseen || 0;
    setUnseenChanges(unseen.value);
  }
  if (list.ok) watches.value = list.data || [];
  loading.value = false;
};

const markAllSeen = async () => {
  busy.value = true;
  const { ok } = await apiJSON("/api/watch/events/seen", { method: "POST" });
  busy.value = false;
  if (!ok) return say("Could not mark the feed as read.", "error");
  await load();
};

const stopWatching = async (url) => {
  busy.value = true;
  const { ok, data } = await apiJSON("/api/watch", {
    method: "POST",
    json: { url, stop: true },
  });
  busy.value = false;
  if (!ok) return say(data.error || "Could not stop that watch.", "error");
  say("No longer watching.", "ok");
  await load();
};

onMounted(load);
</script>

<template>
  <div class="stack" style="gap: var(--s-5)">
    <header class="head">
      <div>
        <h1 class="page-title">Changes</h1>
        <p class="muted">
          {{ unseen ? `${unseen} unread` : "Nothing unread" }}
        </p>
      </div>
      <div class="row">
        <button
          v-if="unseen"
          class="btn btn--secondary btn--sm"
          type="button"
          :disabled="busy"
          @click="markAllSeen"
        >
          Mark all read
        </button>
        <button class="btn btn--ghost btn--sm" type="button" @click="load">
          Refresh
        </button>
      </div>
    </header>

    <p v-if="message" class="msg" :class="`msg--${messageKind}`" role="alert">
      {{ message }}
    </p>

    <section class="card" aria-labelledby="feed-heading">
      <h2 id="feed-heading" class="section-title">Recent activity</h2>
      <p class="muted body">
        Nothing here is emailed. A message saying which hidden service you are
        watching would tell your mail provider — and every server it passes
        through — exactly that.
      </p>

      <p v-if="loading" class="muted">Loading…</p>
      <ul v-else-if="events.length" class="feed">
        <li
          v-for="e in events"
          :key="e.id"
          class="event"
          :class="{ 'event--unread': !e.seen }"
        >
          <span class="event__kind" :class="`event__kind--${e.kind}`">
            {{ KIND_LABEL[e.kind] || e.kind }}
          </span>
          <div class="event__main">
            <span class="event__title">{{ e.title || "Untitled" }}</span>
            <span class="event__url mono">{{ e.url }}</span>
          </div>
          <span class="event__when muted">
            {{ e.detected_at }}
            <template v-if="e.status_code"> · {{ e.status_code }}</template>
          </span>
        </li>
      </ul>
      <p v-else class="muted">
        No changes recorded yet. Watched sites are compared on each recrawl.
      </p>
    </section>

    <section class="card" aria-labelledby="watches-heading">
      <h2 id="watches-heading" class="section-title">Watched sites</h2>
      <p class="muted body">
        These are never deleted by your retention setting — a watch is you
        saying the site matters.
      </p>

      <ul v-if="watches.length" class="watches">
        <li v-for="w in watches" :key="w.id" class="watchrow">
          <div class="watchrow__main">
            <span class="watchrow__title">{{ w.title || "Untitled" }}</span>
            <span class="watchrow__url mono">{{ w.url }}</span>
            <span class="watchrow__meta muted">
              Every {{ w.interval_days }} day{{
                w.interval_days === 1 ? "" : "s"
              }}
              <template v-if="w.last_checked_at">
                · last checked {{ w.last_checked_at }}
              </template>
            </span>
          </div>
          <button
            class="btn btn--danger btn--sm"
            type="button"
            :disabled="busy"
            @click="stopWatching(w.url)"
          >
            Stop
          </button>
        </li>
      </ul>
      <p v-else-if="!loading" class="muted">
        Nothing watched yet. Open a site from the dashboard and choose “Watch”.
      </p>
    </section>
  </div>
</template>

<style scoped>
.page-title {
  font-size: 1.5rem;
}
.head {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-3);
  align-items: center;
  justify-content: space-between;
}
.row {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
}
.section-title {
  font-size: 1.05rem;
  margin-bottom: var(--s-3);
}
.body {
  margin: 0 0 var(--s-4);
  font-size: 0.9rem;
}

.feed,
.watches {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--s-2);
}

.event,
.watchrow {
  display: flex;
  flex-direction: column;
  gap: var(--s-2);
  padding: var(--s-3);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
}
/* Unread is marked with a border as well as colour, so it survives both a
   monochrome display and the roughly one reader in twelve who would not
   reliably see the tint. */
.event--unread {
  border-left: 3px solid var(--accent);
  background: var(--surface-3);
}

.event__kind {
  align-self: flex-start;
  padding: 3px 10px;
  border-radius: var(--radius-pill);
  font-size: 0.7rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  background: var(--surface-3);
  color: var(--text-dim);
}
.event__kind--content_changed {
  background: rgba(98, 176, 255, 0.15);
  color: var(--link);
}
.event__kind--unreachable {
  background: rgba(255, 107, 107, 0.15);
  color: var(--danger, #ff6b6b);
}
.event__kind--recovered {
  background: rgba(61, 220, 132, 0.14);
  color: var(--ok);
}

.event__main,
.watchrow__main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.event__title,
.watchrow__title {
  font-weight: 650;
}
.event__url,
.watchrow__url {
  font-size: 0.74rem;
  color: var(--text-faint);
  overflow-wrap: anywhere;
}
.event__when,
.watchrow__meta {
  font-size: 0.78rem;
}

@media (min-width: 720px) {
  .event {
    flex-direction: row;
    align-items: center;
    gap: var(--s-3);
  }
  .event__kind {
    align-self: center;
    flex: 0 0 auto;
    min-width: 140px;
    text-align: center;
  }
  .event__main {
    flex: 1 1 auto;
  }
  .event__when {
    flex: 0 0 auto;
    text-align: right;
  }
  .watchrow {
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
  }
}
</style>
