package crawler

import (
	"strings"
	"testing"
)

func TestExtractEntities_CryptoAddresses(t *testing.T) {
	content := `Pay to BTC 1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa or bech32 ` +
		`bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq . ` +
		`Monero: 888tNkZrPN6JsEgekjMnABU4TBzc2Dt29EPAvkRxbANsAnjyPbb3iQ1YBRk1UXcdRsiKc9dhwMVgN5S9cQUiyoogDavup3H ` +
		`ETH 0x52908400098527886E0F7030069857D2E4169EE7`
	e := ExtractEntities(content)
	if len(e.BTC) != 2 {
		t.Errorf("BTC: got %v, want 2 addresses", e.BTC)
	}
	if len(e.XMR) != 1 {
		t.Errorf("XMR: got %v, want 1 address", e.XMR)
	}
	if len(e.ETH) != 1 {
		t.Errorf("ETH: got %v, want 1 address", e.ETH)
	}
}

func TestExtractEntities_EmailsAndPGP(t *testing.T) {
	content := `Contact vendor@example.onion or support@market.com.
-----BEGIN PGP PUBLIC KEY BLOCK-----
mQENBF...fake...
-----END PGP PUBLIC KEY BLOCK-----`
	e := ExtractEntities(content)
	if len(e.Emails) != 2 {
		t.Errorf("emails: got %v, want 2", e.Emails)
	}
	if e.PGPKeys != 1 {
		t.Errorf("pgp: got %d, want 1", e.PGPKeys)
	}
}

func TestExtractEntities_Empty(t *testing.T) {
	e := ExtractEntities("just some ordinary prose with no artifacts in it")
	if !e.Empty() {
		t.Errorf("expected Empty(), got %+v", e)
	}
}

func TestExtractEntities_Dedupes(t *testing.T) {
	addr := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	e := ExtractEntities(addr + " " + addr + " " + addr)
	if len(e.BTC) != 1 {
		t.Errorf("expected dedup to 1, got %v", e.BTC)
	}
}

func TestParseRobots_CrawlDelay(t *testing.T) {
	body := "User-agent: *\nCrawl-delay: 10\nDisallow: /private\n"
	disallowed, delay := parseRobots(strings.NewReader(body), robotsUA)
	if delay.Seconds() != 10 {
		t.Errorf("crawl-delay: got %v, want 10s", delay)
	}
	if len(disallowed) != 1 || disallowed[0] != "/private" {
		t.Errorf("disallowed: got %v", disallowed)
	}
}

func TestParseRobots_UASpecificDelayWins(t *testing.T) {
	body := "User-agent: *\nCrawl-delay: 5\n\nUser-agent: OnionSpider\nCrawl-delay: 2\n"
	_, delay := parseRobots(strings.NewReader(body), robotsUA)
	if delay.Seconds() != 2 {
		t.Errorf("UA-specific crawl-delay should win: got %v, want 2s", delay)
	}
}

func TestParseRobots_NoDelay(t *testing.T) {
	_, delay := parseRobots(strings.NewReader("User-agent: *\nDisallow: /x\n"), robotsUA)
	if delay != 0 {
		t.Errorf("expected 0 delay, got %v", delay)
	}
}
