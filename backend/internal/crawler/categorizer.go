package crawler

import "strings"

// Categoriile recunoscute de auto-categorizator
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

// categoryRule contine keyword-urile care identifica o categorie.
// Scorul este numarul de keyword-uri gasite in titlu+continut (case-insensitive).
type categoryRule struct {
	category string
	keywords []string
}

// Ordinea conteaza: mai specifice first (marketplace > forum > wiki etc.)
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

// Categorize analizeaza titlul si continutul unei pagini si returneaza categoria cea mai probabila.
// Algoritmul numara keyword matches per categorie si o alege pe cea cu scorul maxim.
// Daca nicio categorie nu atinge pragul minim (2 matches), returneaza "unknown".
func Categorize(title, content string) string {
	combined := strings.ToLower(title + " " + content)

	bestCategory := CategoryUnknown
	bestScore := 1 // prag minim: cel putin 2 matches pentru a accepta o categorie

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
