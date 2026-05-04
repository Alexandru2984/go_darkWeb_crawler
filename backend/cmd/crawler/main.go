package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"onion-spider/internal/crawler"
	"onion-spider/internal/database"
	"onion-spider/internal/proxy"
)

func main() {
	targetURL := flag.String("url", "", "onion URL to crawl (ex: http://xmh57jrknzkhv6y3ls3ubitzfqnkrwxhopf5aygthi7d6rplyvk3noyd.onion/)")
	proxyAddr := flag.String("proxy", "127.0.0.1:9050", "SOCKS5 proxy address (Tor)")
	dbDSN := flag.String("db", os.Getenv("DATABASE_URL"), "Database connection (default: $DATABASE_URL)")
	flag.Parse()

	if *targetURL == "" {
		log.Fatal("Error: You must specify a -url flag to crawl.")
	}

	fmt.Printf("🕷️ Starting Onion Spider Crawler...\n")
	fmt.Printf("🌐 Target: %s\n", *targetURL)
	fmt.Printf("🛡️  SOCKS5 Proxy: %s\n\n", *proxyAddr)

	var dbConn *database.DB
	if *dbDSN != "" {
		var err error
		dbConn, err = database.InitDB(*dbDSN)
		if err != nil {
			log.Printf("⚠️ Warning: Could not connect to database (%v). Results will not be saved.", err)
		}
	} else {
		log.Println("⚠️ DATABASE_URL is not set. Results will not be saved to the database.")
	}

	client, err := proxy.NewTorClient(*proxyAddr)
	if err != nil {
		log.Fatalf("❌ Error initializing Tor client: %v", err)
	}

	result, err := crawler.ScrapePage(context.Background(), client, *targetURL)
	if err != nil {
		log.Fatalf("❌ Error during crawling: %v", err)
	}

	fmt.Printf("✅ Crawl completed successfully!\n")
	fmt.Printf("📝 Page title: %s\n", result.Title)
	fmt.Printf("🖥️  Server Header: %s\n", result.ServerHeader)
	fmt.Printf("🔗 .onion links found (%d):\n", len(result.FoundOnions))

	if dbConn != nil {
		_, err = dbConn.SaveNode(*targetURL, result.Title, result.ServerHeader, result.StatusCode, "completed", result.Metadata, result.Content, result.Category, 1)
		if err != nil {
			log.Printf("❌ Error saving node: %v", err)
		}
		for _, link := range result.FoundOnions {
			if err = dbConn.SaveEdge(*targetURL, link, 1, 1); err != nil {
				log.Printf("❌ Error saving edge %s -> %s: %v", *targetURL, link, err)
			}
		}
		fmt.Printf("\n💾 All results have been saved to the database!\n")
	}

	for i, link := range result.FoundOnions {
		fmt.Printf("  [%d] %s\n", i+1, link)
	}
}
