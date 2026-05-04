package crawler

import "strings"

// Recognized categories by the auto-categorizer
const (
	CategoryMarketplace  = "marketplace"
	CategoryForum        = "forum"
	CategorySearchEngine = "search-engine"
	CategoryBlog         = "blog"
	CategoryWiki         = "wiki"
	CategoryDirectory    = "directory"
	CategoryNews         = "news"
	CategorySocial       = "social"
	CategoryUnknown      = "unknown"
)

// categoryRule holds the keywords that identify a category.
// The score is the number of keywords found in title+content (case-insensitive).
type categoryRule struct {
	category string
	keywords []string
}

// Order matters: more specific first (marketplace > forum > wiki etc.)
var categoryRules = []categoryRule{
	{
		category: CategoryMarketplace,
		keywords: []string{
			"marketplace", "market", "shop", "store", "buy", "sell", "vendor",
			"listing", "product", "cart", "checkout", "escrow", "bitcoin",
			"monero", "drugs", "weapons", "accounts", "carding", "cvv",
		},
	},
	{
		category: CategorySearchEngine,
		keywords: []string{
			"search engine", "search the dark", "find onion", "onion search",
			"torch", "ahmia", "haystack", "not evil", "dark search",
		},
	},
	{
		category: CategoryDirectory,
		keywords: []string{
			"directory", "hidden wiki", "link list", "onion links", "dark web links",
			"fresh onions", "index of", ".onion directory", "onion index",
		},
	},
	{
		category: CategoryWiki,
		keywords: []string{
			"wiki", "encyclopedia", "knowledge base", "documentation", "howto",
			"how to", "tutorial", "guide", "faq",
		},
	},
	{
		category: CategoryForum,
		keywords: []string{
			"forum", "board", "thread", "post", "reply", "member", "registration",
			"login", "signup", "community", "discussion", "subforum", "topic",
		},
	},
	{
		category: CategoryNews,
		keywords: []string{
			"news", "breaking", "headline", "article", "report", "journalist",
			"press", "whistleblow", "leak", "transparency",
		},
	},
	{
		category: CategoryBlog,
		keywords: []string{
			"blog", "post", "entry", "archive", "written by", "author",
			"subscribe", "newsletter", "opinion", "diary",
		},
	},
	{
		category: CategorySocial,
		keywords: []string{
			"chat", "messenger", "social", "profile", "follow", "message",
			"friend", "contact", "anonymous chat", "jabber", "xmpp",
		},
	},
}

// Categorize analyzes the title and content of a page and returns the most likely category.
// The algorithm counts keyword matches per category and picks the one with the highest score.
// If no category reaches the minimum threshold (2 matches), it returns "unknown".
func Categorize(title, content string) string {
	combined := strings.ToLower(title + " " + content)

	bestCategory := CategoryUnknown
	bestScore := 1 // minimum threshold: at least 2 matches to accept a category

	for _, rule := range categoryRules {
		score := 0
		for _, kw := range rule.keywords {
			if strings.Contains(combined, kw) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			bestCategory = rule.category
		}
	}

	return bestCategory
}
