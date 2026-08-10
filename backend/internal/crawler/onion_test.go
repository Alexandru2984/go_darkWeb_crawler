package crawler

// Valid addresses from Tor's v3 onion-address specification. Tests use real
// checksummed encodings so they exercise the same validation as production.
const (
	testOnionHostA = "pg6mmjiyjmcrsslvykfwnntlaru7p5svn6y2ymmju6nubxndf4pscryd.onion"
	testOnionHostB = "sp3k262uwy4r2k3ycr5awluarykdpag6a7y33jxop4cs2lu5uz5sseqd.onion"
	testOnionHostC = "xa4r2iadxm55fbnqgwwi5mymqdcofiu3w6rpbtqn7b2dyn7mgwj64jyd.onion"
	testOnionHostD = testOnionHostA
)
