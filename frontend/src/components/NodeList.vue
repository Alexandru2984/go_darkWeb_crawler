<script setup>
import { CATEGORY_COLORS, CATEGORY_LABELS } from "../lib/categories.js";

defineProps({
  nodes: { type: Array, required: true },
  loading: { type: Boolean, default: false },
});

const categoryStyle = (category) => {
  const colour = CATEGORY_COLORS[category] || CATEGORY_COLORS.unknown;
  return {
    background: `${colour}1f`,
    color: colour,
    borderColor: `${colour}66`,
  };
};

const categoryLabel = (category) =>
  CATEGORY_LABELS[category] || category || "Unknown";
const statusClass = (status) => `pill--${(status || "").replace("_", "-")}`;
</script>

<template>
  <div>
    <!-- One dataset, two presentations. Below 900px the six-column table stops
         being readable at any font size worth using, so the same rows render as
         cards; above it, the table is the better scanning surface. Both are in
         the DOM only one at a time, so a screen reader never hears duplicates. -->
    <div class="cards">
      <article v-for="node in nodes" :key="`c-${node.id}`" class="node-card">
        <div class="node-card__top">
          <span class="badge" :style="categoryStyle(node.category)">
            {{ categoryLabel(node.category) }}
          </span>
          <span class="pill" :class="statusClass(node.processing_status)">
            {{ node.processing_status }}
          </span>
        </div>
        <h3 class="node-card__title">{{ node.title || "Untitled" }}</h3>
        <p class="node-card__url mono">{{ node.url }}</p>
        <dl class="node-card__meta">
          <div>
            <dt>Code</dt>
            <dd>{{ node.status_code || "—" }}</dd>
          </div>
          <div>
            <dt>Server</dt>
            <dd>{{ node.server_header || "—" }}</dd>
          </div>
          <div>
            <dt>ID</dt>
            <dd>{{ node.id }}</dd>
          </div>
        </dl>
      </article>
    </div>

    <div class="table-responsive tablewrap">
      <table>
        <caption class="sr-only">
          Discovered onion sites
        </caption>
        <thead>
          <tr>
            <th scope="col" class="col-id">ID</th>
            <th scope="col">URL</th>
            <th scope="col">Title &amp; status</th>
            <th scope="col">Category</th>
            <th scope="col">Server</th>
            <th scope="col" class="col-code">Code</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="node in nodes" :key="node.id">
            <td class="col-id">{{ node.id }}</td>
            <td class="url mono">{{ node.url }}</td>
            <td>
              <div class="title-row">
                <span class="title">{{ node.title || "Untitled" }}</span>
                <span class="pill" :class="statusClass(node.processing_status)">
                  {{ node.processing_status }}
                </span>
              </div>
            </td>
            <td>
              <span class="badge" :style="categoryStyle(node.category)">
                {{ categoryLabel(node.category) }}
              </span>
            </td>
            <td class="muted">{{ node.server_header || "—" }}</td>
            <td class="col-code">
              <span
                class="code"
                :class="{ 'code--ok': node.status_code === 200 }"
              >
                {{ node.status_code || "—" }}
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <p v-if="!nodes.length && !loading" class="empty">
      Nothing here yet. Add an onion URL above to start crawling.
    </p>
  </div>
</template>

<style scoped>
/* ── Mobile: cards ──────────────────────────────────────────────────────── */
.cards {
  display: grid;
  gap: var(--s-3);
}
.tablewrap {
  display: none;
}

.node-card {
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: var(--s-4);
}
.node-card__top {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
  align-items: center;
  margin-bottom: var(--s-3);
}
.node-card__title {
  font-size: 0.98rem;
  font-weight: 650;
  margin-bottom: var(--s-2);
  overflow-wrap: anywhere;
}
.node-card__url {
  margin: 0 0 var(--s-3);
  font-size: 0.78rem;
  color: var(--link);
  overflow-wrap: anywhere;
}
.node-card__meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2) var(--s-5);
  margin: 0;
  font-size: 0.78rem;
}
.node-card__meta div {
  display: flex;
  gap: var(--s-2);
}
.node-card__meta dt {
  color: var(--text-faint);
}
.node-card__meta dd {
  margin: 0;
  color: var(--text);
}

/* ── Shared chips ───────────────────────────────────────────────────────── */
.badge {
  display: inline-block;
  padding: 3px 10px;
  border-radius: var(--radius-pill);
  border: 1px solid;
  font-size: 0.72rem;
  font-weight: 650;
  white-space: nowrap;
}

.pill {
  display: inline-block;
  padding: 3px 10px;
  border-radius: var(--radius-pill);
  font-size: 0.7rem;
  font-weight: 650;
  background: var(--surface-3);
  color: var(--text-dim);
  white-space: nowrap;
}
.pill--pending-v2 {
  color: var(--warn);
  background: rgba(255, 200, 87, 0.12);
}
.pill--crawling {
  color: var(--link);
  background: rgba(98, 176, 255, 0.12);
  animation: pulse 2s infinite;
}
.pill--completed {
  color: var(--ok);
  background: rgba(61, 220, 132, 0.12);
}
.pill--failed {
  color: var(--danger);
  background: rgba(255, 107, 107, 0.12);
}

@keyframes pulse {
  50% {
    opacity: 0.55;
  }
}

.empty {
  padding: var(--s-6) var(--s-4);
  text-align: center;
  color: var(--text-faint);
}

/* ── Desktop: table ─────────────────────────────────────────────────────── */
@media (min-width: 900px) {
  .cards {
    display: none;
  }
  .tablewrap {
    display: block;
  }

  table {
    width: 100%;
    border-collapse: collapse;
  }
  th,
  td {
    padding: var(--s-3) var(--s-4);
    text-align: left;
    border-bottom: 1px solid var(--border);
    vertical-align: middle;
  }
  th {
    position: sticky;
    top: 0;
    background: var(--surface-2);
    color: var(--text-faint);
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.12em;
  }
  tbody tr:hover {
    background: var(--surface-2);
  }

  .col-id,
  .col-code {
    width: 1%;
    white-space: nowrap;
  }
  .url {
    color: var(--link);
    font-size: 0.8rem;
    max-width: 280px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s-3);
  }
  .title {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 240px;
  }
  .code {
    font-family: var(--mono);
    font-size: 0.8rem;
    color: var(--text-dim);
  }
  .code--ok {
    color: var(--ok);
  }
}
</style>
