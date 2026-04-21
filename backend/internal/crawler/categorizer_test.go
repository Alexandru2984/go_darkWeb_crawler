package crawler

import "testing"

func TestCategorize(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		content  string
		expected string
	}{
		{
			name:    "marketplace — keywords puternice",
			title:   "Dark Market",
			content: "Buy drugs weapons vendor listing escrow bitcoin monero shop",
			expected: CategoryMarketplace,
		},
		{
			name:    "forum — keywords board/thread",
			title:   "Anonymous Forum",
			content: "Welcome to our community forum. Register and login to participate in discussion threads. Reply to topics, member area.",
			expected: CategoryForum,
		},
		{
			name:    "search engine — keywords specifice",
			title:   "Onion Search Engine",
			content: "Search the dark web. Find onion sites with our search engine. Torch, Ahmia, not evil dark search.",
			expected: CategorySearchEngine,
		},
		{
			name:    "wiki — keywords enciclopedie",
			title:   "Hidden Wiki",
			content: "This wiki contains documentation, how to guides, faq and tutorials for dark web navigation.",
			expected: CategoryWiki,
		},
		{
			name:    "directory — lista de linkuri",
			title:   "Fresh Onions Directory",
			content: "Onion links directory. Hidden wiki link list. Index of .onion sites. Fresh onions.",
			expected: CategoryDirectory,
		},
		{
			name:    "news — stiri si presa",
			title:   "Dark News",
			content: "Breaking news and headline articles. Journalist reports on whistleblowing and transparency leaks.",
			expected: CategoryNews,
		},
		{
			name:    "blog — jurnal personal",
			title:   "My Anonymous Blog",
			content: "Blog post and entry. Written by anonymous author. Archive of opinion pieces. Subscribe to newsletter.",
			expected: CategoryBlog,
		},
		{
			name:    "social — chat si mesaje",
			title:   "Anonymous Chat",
			content: "Anonymous chat messenger. Send message to contact. Social profile follow. Jabber XMPP.",
			expected: CategorySocial,
		},
		{
			name:     "unknown — prea putine keywords",
			title:    "My Site",
			content:  "Welcome to my site. This is a page.",
			expected: CategoryUnknown,
		},
		{
			name:     "unknown — titlu si continut goale",
			title:    "",
			content:  "",
			expected: CategoryUnknown,
		},
		{
			name:    "case insensitive — majuscule ignorate",
			title:   "DARK MARKET",
			content: "BUY DRUGS VENDOR LISTING ESCROW BITCOIN MONERO WEAPONS SHOP",
			expected: CategoryMarketplace,
		},
		{
			name:    "marketplace bate forum la scor mai mare",
			title:   "Market Forum",
			content: "Buy sell vendor listing product cart checkout escrow bitcoin monero drugs weapons accounts carding cvv marketplace shop. Forum board thread.",
			expected: CategoryMarketplace,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Categorize(tt.title, tt.content)
			if got != tt.expected {
				t.Errorf("Categorize(%q, %q) = %q, asteptat %q", tt.title, tt.content, got, tt.expected)
			}
		})
	}
}
