<script setup>
import { ref, onMounted, onUnmounted, watch, nextTick } from "vue";
// vis-network is imported here and nowhere else. This component is only ever
// reached through a dynamic import, so the library stays out of the entry
// chunk — it is the single largest dependency in the app and nobody sitting on
// the login screen, or reading the list, should be paying to download it.
import { Network } from "vis-network";
import {
  CATEGORY_COLORS,
  CATEGORY_LABELS,
  allCategories,
} from "../lib/categories.js";

const props = defineProps({
  nodes: { type: Array, required: true },
  edges: { type: Array, required: true },
});
const emit = defineEmits(["notice"]);

const container = ref(null);
const physicsOn = ref(true);
let network = null;

const draw = () => {
  if (!container.value) return;

  const degree = {};
  for (const edge of props.edges) {
    degree[edge.source] = (degree[edge.source] || 0) + 1;
    degree[edge.target] = (degree[edge.target] || 0) + 1;
  }

  const visNodes = props.nodes.map((n) => {
    const colour = CATEGORY_COLORS[n.category] || CATEGORY_COLORS.unknown;
    let border = colour;
    if (n.status_code === 0 && n.processing_status === "completed")
      border = "#ff3b3b";
    else if (n.processing_status === "pending") border = "#444";

    const links = degree[n.url] || 0;
    const size = Math.min(14 + links * 2.5, 58);
    const title = n.title || "Inaccessible / secondary source";
    const label = title.length > 24 ? `${title.slice(0, 24)}…` : title;

    return {
      id: n.url,
      label,
      title: `${title}\nURL: ${n.url}\nCategory: ${CATEGORY_LABELS[n.category] || n.category}\nHTTP: ${n.status_code} · Links: ${links}\n(Double-click to copy the URL)`,
      color: {
        background: colour,
        border,
        highlight: { background: "#fff", border: colour },
        hover: { background: colour, border: "#fff" },
      },
      shape: "dot",
      size,
      borderWidth: size > 30 ? 3 : 1,
      font: { color: "#ccc", size: size > 30 ? 15 : 10, face: "Inter" },
    };
  });

  const unique = Array.from(new Map(visNodes.map((n) => [n.id, n])).values());
  const visEdges = props.edges.map((e) => ({
    from: e.source,
    to: e.target,
    arrows: { to: { enabled: true, scaleFactor: 0.4 } },
    color: { color: "rgba(90,90,90,0.4)", highlight: "#ff3b3b", hover: "#fff" },
    smooth: { type: "dynamic" },
  }));

  if (network) network.destroy();
  network = new Network(
    container.value,
    { nodes: unique, edges: visEdges },
    {
      edges: { width: 1, selectionWidth: 4 },
      physics: {
        solver: "forceAtlas2Based",
        forceAtlas2Based: {
          gravitationalConstant: -150,
          centralGravity: 0.015,
          springLength: 200,
          springConstant: 0.06,
          damping: 0.7,
        },
        maxVelocity: 50,
        minVelocity: 0.1,
        stabilization: { iterations: 250 },
      },
      interaction: {
        hover: true,
        tooltipDelay: 60,
        zoomView: true,
        dragView: true,
        hideEdgesOnDrag: true,
      },
    },
  );
  physicsOn.value = true;

  network.on("stabilizationIterationsDone", () => {
    network.setOptions({ physics: false });
    physicsOn.value = false;
  });

  network.on("doubleClick", async (params) => {
    if (!params.nodes.length) return;
    try {
      await navigator.clipboard.writeText(params.nodes[0]);
      emit("notice", "URL copied to clipboard");
    } catch {
      emit("notice", "Could not copy the URL");
    }
  });
};

const togglePhysics = () => {
  if (!network) return;
  physicsOn.value = !physicsOn.value;
  network.setOptions({ physics: physicsOn.value });
};

const fit = () => network?.fit({ animation: true });

watch(
  () => [props.nodes, props.edges],
  async () => {
    await nextTick();
    draw();
  },
);

onMounted(async () => {
  await nextTick();
  draw();
});
onUnmounted(() => network?.destroy());
</script>

<template>
  <section class="panel" aria-labelledby="graph-heading">
    <div class="panel__head">
      <h2 id="graph-heading" class="panel__title">Network map</h2>
      <div class="panel__actions">
        <button
          class="btn btn--ghost btn--sm"
          type="button"
          @click="togglePhysics"
        >
          {{ physicsOn ? "Freeze layout" : "Resume layout" }}
        </button>
        <button class="btn btn--ghost btn--sm" type="button" @click="fit">
          Fit to view
        </button>
        <button class="btn btn--secondary btn--sm" type="button" @click="draw">
          Redraw
        </button>
      </div>
    </div>

    <ul class="legend">
      <li v-for="cat in allCategories" :key="cat">
        <span
          class="legend__dot"
          :style="{ background: CATEGORY_COLORS[cat] }"
          aria-hidden="true"
        ></span>
        {{ CATEGORY_LABELS[cat] }}
      </li>
    </ul>

    <!-- The canvas is not reachable by keyboard or screen reader; the list view
         is the accessible equivalent of the same data, so say so rather than
         pretending the graph is operable. -->
    <div
      ref="container"
      class="canvas"
      role="img"
      aria-label="Force-directed map of discovered onion sites. The list view presents the same data in an accessible form."
    ></div>
  </section>
</template>

<style scoped>
.panel {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: var(--s-4);
}

.panel__head {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-3);
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--s-3);
}
.panel__title {
  font-size: 1rem;
}
.panel__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2);
}

.legend {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-2) var(--s-4);
  list-style: none;
  margin: 0 0 var(--s-3);
  padding: var(--s-3);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  font-size: 0.75rem;
  color: var(--text-dim);
}
.legend li {
  display: inline-flex;
  align-items: center;
  gap: var(--s-2);
  font-weight: 600;
}
.legend__dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
}

.canvas {
  width: 100%;
  height: 60svh;
  min-height: 320px;
  background: radial-gradient(circle, #191919 0%, #050505 100%);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  outline: none;
}

@media (min-width: 900px) {
  .panel {
    padding: var(--s-5);
  }
  .canvas {
    height: 70svh;
  }
}
</style>

<style>
/* vis-network renders its tooltip into document.body, outside this component's
   scope, so this rule cannot be scoped. */
div.vis-tooltip {
  position: absolute;
  padding: 10px 14px !important;
  background: rgba(18, 18, 18, 0.97) !important;
  color: #fff !important;
  border: 1px solid #ff3b3b !important;
  border-radius: 10px;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.9);
  pointer-events: none;
  z-index: 1000;
  font-family: "JetBrains Mono", ui-monospace, monospace !important;
  font-size: 12px !important;
  line-height: 1.5;
  white-space: pre-wrap;
}
</style>
