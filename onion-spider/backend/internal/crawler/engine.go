package crawler

import (
	"log"
	"net/url"
	"onion-spider/internal/database"
	"onion-spider/internal/proxy"
	"sync"
	"time"
)

type Engine struct {
	DB               *database.DB
	Proxy            string
	Workers          int
	domainLastAccess map[string]time.Time
	domainMu         sync.Mutex
}

func NewEngine(db *database.DB, proxyAddr string, workerCount int) *Engine {
	return &Engine{
		DB:               db,
		Proxy:            proxyAddr,
		Workers:          workerCount,
		domainLastAccess: make(map[string]time.Time),
	}
}

// Start porneste motorul de crawling cu numarul de workeri specificat
func (e *Engine) Start() {
	log.Printf("🚀 Pornire motor crawling cu %d workeri (Politeness Activ)...", e.Workers)
	for i := 0; i < e.Workers; i++ {
		go e.worker(i)
	}
}

// waitForDomain asigura ca nu stresam acelasi domeniu. Impune 5 secunde intarziere.
func (e *Engine) waitForDomain(targetUrl string) {
	parsed, err := url.Parse(targetUrl)
	if err != nil {
		return
	}
	host := parsed.Host

	e.domainMu.Lock()
	lastAccess, exists := e.domainLastAccess[host]
	
	if exists {
		elapsed := time.Since(lastAccess)
		if elapsed < 5*time.Second {
			waitTime := 5*time.Second - elapsed
			// Rezervam momentul viitor de acces pentru urmatorul worker care ar putea cere acelasi domeniu
			e.domainLastAccess[host] = time.Now().Add(waitTime) 
			e.domainMu.Unlock()
			time.Sleep(waitTime)
			return
		}
	}
	
	e.domainLastAccess[host] = time.Now()
	e.domainMu.Unlock()
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
		targetUrl, depth, err := e.DB.GetNextPendingNode()
		if err != nil {
			log.Printf("[Worker %d] Eroare la preluarea cozii: %v", id, err)
			time.Sleep(5 * time.Second)
			continue
		}

		if targetUrl == "" {
			// Coada este goala, asteptam putin
			time.Sleep(10 * time.Second)
			continue
		}

		log.Printf("[Worker %d] Asteapta permisiunea rate-limit pt: %s", id, targetUrl)
		
		// Aplicam Politeness (asteptam daca domeniul a fost lovit recent)
		e.waitForDomain(targetUrl)

		log.Printf("[Worker %d] Scanare aprobata: %s (Adancime: %d)", id, targetUrl, depth)

		// 2. Scanam pagina
		result, err := ScrapePage(client, targetUrl)
		if err != nil {
			log.Printf("[Worker %d] Eroare retea/SOCKS la %s: %v", id, targetUrl, err)
			// Aplicam mecanismul Lazarus: Programeaza un Retry pentru mai tarziu
			errRetry := e.DB.FailNodeWithRetry(targetUrl)
			if errRetry != nil {
				log.Printf("[Worker %d] DB Eroare la retry pentru %s: %v", id, targetUrl, errRetry)
			}
			continue
		}

		// 3. Salvam rezultatul si link-urile noi
		err = e.DB.SaveNode(targetUrl, result.Title, result.ServerHeader, result.StatusCode, "completed", result.Metadata, result.Content)
		if err != nil {
			log.Printf("[Worker %d] Eroare la salvare nod: %v", id, err)
		}

		// 4. Limita de adancime: Oprim recursivitatea daca suntem prea adanc (ex: max 2)
		const MAX_DEPTH = 2

		if depth < MAX_DEPTH {
			for _, foundUrl := range result.FoundOnions {
				err = e.DB.SaveEdge(targetUrl, foundUrl, depth+1)
				if err != nil {
					log.Printf("[Worker %d] Eroare la salvare edge: %v", id, err)
				}
			}
			log.Printf("[Worker %d] Finalizat: %s (gasit %d link-uri noi)", id, targetUrl, len(result.FoundOnions))
		} else {
			log.Printf("[Worker %d] Finalizat: %s (adancime maxima %d atinsa, ignoram %d link-uri noi)", id, targetUrl, MAX_DEPTH, len(result.FoundOnions))
		}
		
		// O mica pauza default ca workerul sa isi regleze ciclul
		time.Sleep(1 * time.Second)
	}
}

// AddToQueue adauga un URL manual in coada
func (e *Engine) AddToQueue(url string) error {
	return e.DB.SaveNode(url, "", "", 0, "pending_v2", "{}", "")
}