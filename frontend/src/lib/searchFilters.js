export const CRAWL_STATUSES = {
  pending: "Queued",
  crawling: "Crawling",
  completed: "Completed",
  failed: "Failed",
  blocked: "Blocked",
};

export const RESULT_SORTS = {
  "": "Automatic",
  recent: "Newest discovered",
  oldest: "Oldest discovered",
  title: "Title A–Z",
  relevance: "Content relevance",
  bookmarked: "Recently bookmarked",
};

export const REVIEW_STATES = {
  unreviewed: "Not reviewed",
  reviewed: "Reviewed",
  follow_up: "Follow up",
};

export const emptyResearchFilters = () => ({
  note: "",
  bookmarked_only: false,
  review: "",
  title: "",
  host: "",
  discovered_from: "",
  discovered_to: "",
  tag: "",
  sort: "",
});
