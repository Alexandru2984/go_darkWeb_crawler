package crawler

import "regexp"

// Entities holds structured intelligence extracted from a crawled page. These
// are the artifacts that matter most when mapping dark-web infrastructure:
// cryptocurrency addresses (vendor wallets, donation/ransom addresses), PGP
// public keys (vendor/contact identity) and contact emails.
//
// All lists are de-duplicated and capped (maxEntitiesPerKind) so a hostile or
// junk page can't bloat the stored metadata.
type Entities struct {
	BTC     []string `json:"btc,omitempty"`
	XMR     []string `json:"xmr,omitempty"`
	ETH     []string `json:"eth,omitempty"`
	Emails  []string `json:"emails,omitempty"`
	PGPKeys int      `json:"pgp_keys,omitempty"`
}

// Empty reports whether nothing was extracted (so callers can skip writing an
// empty object into metadata).
func (e Entities) Empty() bool {
	return len(e.BTC) == 0 && len(e.XMR) == 0 && len(e.ETH) == 0 &&
		len(e.Emails) == 0 && e.PGPKeys == 0
}

const maxEntitiesPerKind = 50

var (
	// Bitcoin: legacy P2PKH/P2SH (base58, starts 1 or 3) and bech32 (bc1...).
	// base58 excludes 0, O, I, l — the regex enforces that alphabet.
	reBTCLegacy = regexp.MustCompile(`\b[13][a-km-zA-HJ-NP-Z1-9]{25,34}\b`)
	reBTCBech32 = regexp.MustCompile(`\bbc1[ac-hj-np-z02-9]{11,71}\b`)

	// Monero: 95-char standard/integrated/sub address starting with 4 or 8.
	reXMR = regexp.MustCompile(`\b[48][0-9AB][1-9A-HJ-NP-Za-km-z]{93}\b`)

	// Ethereum: 0x + 40 hex chars.
	reETH = regexp.MustCompile(`\b0x[a-fA-F0-9]{40}\b`)

	// Email — deliberately conservative; good enough to surface contact addrs.
	reEmail = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)

	// Count of ASCII-armored PGP public key blocks.
	rePGP = regexp.MustCompile(`-----BEGIN PGP PUBLIC KEY BLOCK-----`)
)

// ExtractEntities scans raw page text for high-value dark-web artifacts.
func ExtractEntities(content string) Entities {
	var e Entities
	e.BTC = capUnique(append(reBTCLegacy.FindAllString(content, -1), reBTCBech32.FindAllString(content, -1)...))
	e.XMR = capUnique(reXMR.FindAllString(content, -1))
	e.ETH = capUnique(reETH.FindAllString(content, -1))
	e.Emails = capUnique(reEmail.FindAllString(content, -1))
	e.PGPKeys = len(rePGP.FindAllString(content, -1))
	return e
}

// capUnique de-duplicates while preserving order and caps the slice length.
// Returns nil for an empty input so omitempty drops the field from JSON.
func capUnique(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
		if len(out) >= maxEntitiesPerKind {
			break
		}
	}
	return out
}
