import { ref } from "vue";
import { apiJSON } from "../lib/api.js";

/**
 * Unread count for the change feed, shared between the header badge and the
 * feed itself so marking the feed read updates the badge without a round trip.
 */
export const unseenChanges = ref(0);

export async function refreshUnseenChanges() {
  // limit=1 because only the count is wanted here; the feed view asks for the
  // events themselves. Fetching the whole feed to render a number would pull an
  // account's entire watch list into memory on every page load.
  const { ok, data } = await apiJSON("/api/watch/events?limit=1");
  unseenChanges.value = ok ? data.unseen || 0 : 0;
  return unseenChanges.value;
}

export function setUnseenChanges(n) {
  unseenChanges.value = n;
}
