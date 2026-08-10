// Category presentation: colors and labels shared by the table, graph and
// legend. Kept in sync with the backend categorizer (internal/crawler/
// categorizer.go) — every category the backend can emit must have an entry
// here so the UI can color and filter it.

export const CATEGORY_COLORS = {
  marketplace: "#e74c3c",
  forum: "#e67e22",
  "search-engine": "#3498db",
  blog: "#9b59b6",
  wiki: "#1abc9c",
  directory: "#f39c12",
  news: "#27ae60",
  social: "#e91e63",
  hacking: "#c0392b",
  "crypto-service": "#16a085",
  hosting: "#7f8c8d",
  unknown: "#555555",
};

export const CATEGORY_LABELS = {
  marketplace: "🛒 Marketplace",
  forum: "💬 Forum",
  "search-engine": "🔍 Search Engine",
  blog: "📝 Blog",
  wiki: "📚 Wiki",
  directory: "📁 Directory",
  news: "📰 News",
  social: "👥 Social",
  hacking: "💻 Hacking",
  "crypto-service": "🪙 Crypto",
  hosting: "🗄️ Hosting",
  unknown: "❓ Unknown",
};

export const allCategories = Object.keys(CATEGORY_LABELS);

// categoryColor returns the color for a category, falling back to the
// "unknown" color for anything not in the map.
export function categoryColor(category) {
  return CATEGORY_COLORS[category] || CATEGORY_COLORS.unknown;
}

// categoryLabel returns the human label for a category, falling back to the
// raw category string for anything not in the map.
export function categoryLabel(category) {
  return CATEGORY_LABELS[category] || category;
}
