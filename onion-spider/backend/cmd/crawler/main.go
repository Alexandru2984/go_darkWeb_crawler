package main

import (
	"flag"
	"fmt"
	"log"
	"onion-spider/internal/crawler"
	"onion-spider/internal/proxy"
)

func main() {
	targetURL := flag.String("url", "", "URL-ul onion de scanat (ex: http://xmh57jrknzkhv6y3ls3ubitzfqnkrwxhopf5aygthi7d6rplyvk3noyd.onion/)")
	proxyAddr := flag.String("proxy", "127.0.0.1:9050", "Adresa proxy SOCKS5 (Tor)")
	flag.Parse()

	if *targetURL == "" {
		log.Fatal("Eroare: Trebuie sa specifici un flag -url pentru a scana. Ex: ./crawler_bin -url http://duckduckgogg42xjoc72x3sjiqbvzwsgxgjvpeqg5unfxgf2fsvawd.onion/")
	}

	fmt.Printf("🕷️ Pornire Onion Spider Crawler...\n")
	fmt.Printf("🌐 Tinta: %s\n", *targetURL)
	fmt.Printf("🛡️  Proxy SOCKS5: %s\n\n", *proxyAddr)

	// 1. Initializam clientul Tor
	client, err := proxy.NewTorClient(*proxyAddr)
	if err != nil {
		log.Fatalf("❌ Eroare la initializarea clientului Tor: %v", err)
	}

	// 2. Pornim Scraper-ul
	result, err := crawler.ScrapePage(client, *targetURL)
	if err != nil {
		log.Fatalf("❌ Eroare in timpul scanarii: %v", err)
	}

	// 3. Afisam rezultatele in consola
	fmt.Printf("✅ Scanare finalizata cu succes!\n")
	fmt.Printf("📝 Titlu pagina: %s\n", result.Title)
	fmt.Printf("🖥️  Server Header: %s\n", result.ServerHeader)
	fmt.Printf("🔗 Link-uri .onion gasite (%d):\n", len(result.FoundOnions))
	
	for i, link := range result.FoundOnions {
		fmt.Printf("  [%d] %s\n", i+1, link)
	}
}
