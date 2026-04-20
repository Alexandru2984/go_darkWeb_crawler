package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"onion-spider/internal/crawler"
	"onion-spider/internal/database"
	"onion-spider/internal/proxy"
)

func main() {
	targetURL := flag.String("url", "", "URL-ul onion de scanat (ex: http://xmh57jrknzkhv6y3ls3ubitzfqnkrwxhopf5aygthi7d6rplyvk3noyd.onion/)")
	proxyAddr := flag.String("proxy", "127.0.0.1:9050", "Adresa proxy SOCKS5 (Tor)")
	dbDSN := flag.String("db", os.Getenv("DATABASE_URL"), "Conexiune la baza de date (default: $DATABASE_URL)")
	flag.Parse()

	if *targetURL == "" {
		log.Fatal("Eroare: Trebuie sa specifici un flag -url pentru a scana.")
	}

	fmt.Printf("🕷️ Pornire Onion Spider Crawler...\n")
	fmt.Printf("🌐 Tinta: %s\n", *targetURL)
	fmt.Printf("🛡️  Proxy SOCKS5: %s\n\n", *proxyAddr)

	var dbConn *database.DB
	if *dbDSN != "" {
		var err error
		dbConn, err = database.InitDB(*dbDSN)
		if err != nil {
			log.Printf("⚠️ Atentie: Nu am putut conecta la baza de date (%v). Rezultatele nu vor fi salvate.", err)
		}
	} else {
		log.Println("⚠️ DATABASE_URL nu este setat. Rezultatele nu vor fi salvate in baza de date.")
	}

	client, err := proxy.NewTorClient(*proxyAddr)
	if err != nil {
		log.Fatalf("❌ Eroare la initializarea clientului Tor: %v", err)
	}

	result, err := crawler.ScrapePage(client, *targetURL)
	if err != nil {
		log.Fatalf("❌ Eroare in timpul scanarii: %v", err)
	}

	fmt.Printf("✅ Scanare finalizata cu succes!\n")
	fmt.Printf("📝 Titlu pagina: %s\n", result.Title)
	fmt.Printf("🖥️  Server Header: %s\n", result.ServerHeader)
	fmt.Printf("🔗 Link-uri .onion gasite (%d):\n", len(result.FoundOnions))

	if dbConn != nil {
		err = dbConn.SaveNode(*targetURL, result.Title, result.ServerHeader, result.StatusCode, "completed", result.Metadata, result.Content)
		if err != nil {
			log.Printf("❌ Eroare la salvarea nodului: %v", err)
		}
		for _, link := range result.FoundOnions {
			if err = dbConn.SaveEdge(*targetURL, link, 1); err != nil {
				log.Printf("❌ Eroare la salvarea legaturii %s -> %s: %v", *targetURL, link, err)
			}
		}
		fmt.Printf("\n💾 Toate rezultatele au fost salvate in baza de date!\n")
	}

	for i, link := range result.FoundOnions {
		fmt.Printf("  [%d] %s\n", i+1, link)
	}
}
