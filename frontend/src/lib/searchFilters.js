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
};

export const emptyResearchFilters = () => ({
  title: "",
  host: "",
  discovered_from: "",
  discovered_to: "",
  tag: "",
  sort: "",
});
