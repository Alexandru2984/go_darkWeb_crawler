<script setup>
import { computed, ref, watch } from "vue";
import { RESULT_SORTS } from "../lib/searchFilters.js";
const props = defineProps({
  modelValue: { type: Object, required: true },
  collections: { type: Array, default: () => [] },
  disabled: { type: Boolean, default: false },
  resetVersion: { type: Number, default: 0 },
});
const emit = defineEmits(["update:modelValue", "dirty"]);
const draft = ref({ ...props.modelValue });
const reset = () => {
  draft.value = { ...props.modelValue };
  emit("dirty", false);
};
watch(() => props.modelValue, reset, { deep: true });
watch(() => props.resetVersion, reset);
const update = (key, value) => {
  draft.value = { ...draft.value, [key]: value };
  emit(
    "dirty",
    JSON.stringify(draft.value) !== JSON.stringify(props.modelValue),
  );
};
const apply = () => {
  emit("update:modelValue", { ...draft.value });
  emit("dirty", false);
};
const missing = computed(
  () =>
    draft.value.tag &&
    !props.collections.some((item) => item.tag === draft.value.tag),
);
</script>

<template>
  <details class="research-filters">
    <summary>More filters &amp; sorting</summary>
    <form class="filters-grid" @submit.prevent="apply">
      <div>
        <label class="label" for="filter-title">Title contains</label>
        <input
          id="filter-title"
          class="field"
          type="search"
          :value="draft.title"
          maxlength="200"
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          :disabled="disabled"
          @input="update('title', $event.target.value)"
        />
      </div>
      <div>
        <label class="label" for="filter-host">Onion domain</label>
        <input
          id="filter-host"
          class="field"
          type="text"
          :value="draft.host"
          maxlength="253"
          placeholder="56-character-address.onion"
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          :disabled="disabled"
          @input="update('host', $event.target.value)"
        />
      </div>
      <div>
        <label class="label" for="filter-from">Discovered from</label>
        <input
          id="filter-from"
          class="field"
          type="date"
          :value="draft.discovered_from"
          :disabled="disabled"
          @change="update('discovered_from', $event.target.value)"
        />
      </div>
      <div>
        <label class="label" for="filter-to">Discovered through</label>
        <input
          id="filter-to"
          class="field"
          type="date"
          :value="draft.discovered_to"
          :disabled="disabled"
          @change="update('discovered_to', $event.target.value)"
        />
      </div>
      <div>
        <label class="label" for="filter-collection">Private collection</label>
        <select
          id="filter-collection"
          class="field"
          :value="draft.tag"
          :disabled="disabled"
          @change="update('tag', $event.target.value)"
        >
          <option value="">All collections</option>
          <option v-if="missing" :value="draft.tag">
            Missing collection: {{ draft.tag }}
          </option>
          <option v-for="item in collections" :key="item.tag" :value="item.tag">
            {{ item.tag }} ({{ item.count }})
          </option>
        </select>
      </div>
      <div>
        <label class="label" for="filter-sort">Sort results</label>
        <select
          id="filter-sort"
          class="field"
          :value="draft.sort"
          :disabled="disabled"
          @change="update('sort', $event.target.value)"
        >
          <option
            v-for="(label, value) in RESULT_SORTS"
            :key="value"
            :value="value"
          >
            {{ label }}
          </option>
        </select>
      </div>
      <button
        class="btn btn--secondary apply"
        type="submit"
        :disabled="disabled"
      >
        Apply filters
      </button>
      <p class="hint">
        Choose Apply filters to update results. Collection filters show your own
        tagged sites, including on an administrator account. Discovery dates
        include the full selected days.
      </p>
    </form>
  </details>
</template>

<style scoped>
.research-filters {
  margin-top: var(--s-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  min-width: 0;
}
summary {
  padding: var(--s-3);
  cursor: pointer;
  min-height: var(--tap);
  font-weight: 600;
}
.filters-grid {
  display: grid;
  gap: var(--s-3);
  padding: 0 var(--s-3) var(--s-3);
}
.filters-grid > div {
  min-width: 0;
}
.hint {
  font-size: 0.8rem;
  color: var(--text-dim);
  grid-column: 1/-1;
}
.apply {
  justify-self: start;
  max-width: 100%;
  white-space: normal;
}
input[type="date"] {
  min-width: 0;
  max-width: 100%;
}
@media (min-width: 700px) {
  .filters-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
