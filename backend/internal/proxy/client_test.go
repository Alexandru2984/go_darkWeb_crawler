package proxy

import "testing"

func TestIsolationCredentialSeparatesHosts(t *testing.T) {
	a := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.onion"
	b := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.onion"

	// Distinct hosts must map to distinct credentials — this is what makes Tor
	// (IsolateSOCKSAuth) build a separate circuit per site.
	if isolationCredential(a) == isolationCredential(b) {
		t.Fatal("distinct onion hosts produced the same credential; circuits would be shared")
	}

	// Stability: the same host must reuse its credential, otherwise every
	// request would open a fresh circuit and crawling would crawl.
	if isolationCredential(a) != isolationCredential(a) {
		t.Fatal("credential is not stable for a repeated host")
	}

	// Case folding: SplitHostPort preserves whatever case the URL carried, and
	// onion hosts are case-insensitive. Without folding, "ABC.onion" and
	// "abc.onion" would occupy two circuits for one site.
	if isolationCredential("ABC.onion") != isolationCredential("abc.onion") {
		t.Fatal("credential is case-sensitive; one host would span two circuits")
	}
}

func TestIsolationCredentialIsSOCKS5Safe(t *testing.T) {
	// SOCKS5 caps username and password at 255 bytes each. A hex-encoded
	// 128-bit digest is 32 bytes regardless of how long the input host is.
	long := ""
	for i := 0; i < 500; i++ {
		long += "x"
	}
	got := isolationCredential(long)
	if len(got) != 32 {
		t.Fatalf("credential length = %d, want 32", len(got))
	}
	for _, r := range got {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Fatalf("credential contains non-hex byte %q", r)
		}
	}
}
