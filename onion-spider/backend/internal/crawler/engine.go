package crawler

import (
	"log"
	"onion-spider/internal/database"
	"onion-spider/internal/proxy"
	"time"
)

type Engine struct {
	DB      *database.DB
	Proxy   string
	Workers int
}

func NewEngine(db *database.DB, proxyAddr string, workerCount int) *Engine {
	return &Engine{
		DB:      db,
		Proxy:   proxyAddr,
		Workers: workerCount,
	}
}

// Start porneste motorul de crawling cu numarul de workeri specificat
func (e *Engine) Start() {
	log.Printf("🚀 Pornire motor crawling cu %d workeri...", e.Workers)
	for i := 0; i < e.Workers; i++ {
		go e.worker(i)
	}
}

func (e *Engine) worker(id int) {
	log.Printf("[Worker %d] Activ", id)
	
	// Initializam clientul Tor o singura data per worker
	client, err := proxy.NewTorClient(e.Proxy)
	if err != nil {
		log.Printf("[Worker %d] Eroare fatala Tor: %v", id, err)
		return
	}

	for {
		// 1. Luam urmatorul URL din DB
		url, err := e.DB.GetNextPendingNode()
		if err != nil {
			log.Printf("[Worker %d] Eroare la preluarea cozii: %v", id, err)
			time.Sleep(5 * time.Second)
			continue
		}

		if url == "" {
			// Coada este goala, asteptam putin
			time.Sleep(10 * time.Second)
			continue
		}

		log.Printf("[Worker %d] Scanare: %s", id, url)

		// 2. Scanam pagina
		result, err := ScrapePage(client, url)
		if err != nil {
			log.Printf("[Worker %d] Eroare retea/SOCKS la %s: %v", id, url, err)
			// Marcam ca finalizat dar cu status 0, sa stim ca nu e accesibil
			e.DB.SaveNode(url, "", "", 0, "completed", "{}")
			continue
		}

		// 3. Salvam rezultatul si link-urile noi
		err = e.DB.SaveNode(url, result.Title, result.ServerHeader, result.StatusCode, "completed", result.Metadata)
		if err != nil {
			log.Printf("[Worker %d] Eroare la salvare nod: %v", id, err)
		}

		for _, foundUrl := range result.FoundOnions {
			err = e.DB.SaveEdge(url, foundUrl)
			if err != nil {
				log.Printf("[Worker %d] Eroare la salvare edge: %v", id, err)
			}
		}

		log.Printf("[Worker %d] Finalizat: %s (gasit %d link-uri)", id, url, len(result.FoundOnions))
		
		// O mica pauza pentru a nu supra-solicita Tor si a nu parea prea agresivi
		time.Sleep(2 * time.Second)
	}
}

// AddToQueue adauga un URL manual in coada
func (e *Engine) AddToQueue(url string) error {
	return e.DB.SaveNode(url, "", "", 0, "pending_v2", "{}")
}
