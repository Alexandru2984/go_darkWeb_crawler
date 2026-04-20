package crawler

import (
	"context"
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
	MaxDepth         int
	domainLastAccess map[string]time.Time
	domainMu         sync.Mutex
	wg               sync.WaitGroup
	cancel           context.CancelFunc
}

func NewEngine(db *database.DB, proxyAddr string, workerCount int, maxDepth int) *Engine {
	return &Engine{
		DB:               db,
		Proxy:            proxyAddr,
		Workers:          workerCount,
		MaxDepth:         maxDepth,
		domainLastAccess: make(map[string]time.Time),
	}
}

// Start porneste motorul de crawling cu numarul de workeri specificat.
// Fiecare worker se reporneste automat daca iese din cauza unei erori Tor.
func (e *Engine) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	log.Printf("🚀 Pornire motor crawling cu %d workeri (MaxDepth: %d, Politeness Activ)...", e.Workers, e.MaxDepth)
	for i := 0; i < e.Workers; i++ {
		e.wg.Add(1)
		go func(id int) {
			defer e.wg.Done()
			for {
				e.worker(ctx, id)
				select {
				case <-ctx.Done():
					log.Printf("[Worker %d] Oprit definitiv.", id)
					return
				case <-time.After(10 * time.Second):
					log.Printf("[Worker %d] Repornire dupa eroare Tor...", id)
				}
			}
		}(i)
	}
}

// Stop opreste toti workerii si asteapta finalizarea lor
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
	log.Println("🛑 Motor crawling oprit.")
}

// waitForDomain asigura ca nu stresam acelasi domeniu — impune 5 secunde intarziere.
// Returneaza false daca contextul a fost anulat in timpul asteptarii.
func (e *Engine) waitForDomain(ctx context.Context, targetUrl string) bool {
	parsed, err := url.Parse(targetUrl)
	if err != nil {
		return true
	}
	host := parsed.Host

	e.domainMu.Lock()
	lastAccess, exists := e.domainLastAccess[host]

	if exists {
		elapsed := time.Since(lastAccess)
		if elapsed < 5*time.Second {
			waitTime := 5*time.Second - elapsed
			e.domainLastAccess[host] = time.Now().Add(waitTime)
			e.domainMu.Unlock()
			select {
			case <-ctx.Done():
				return false
			case <-time.After(waitTime):
			}
			e.domainMu.Lock()
			e.domainLastAccess[host] = time.Now()
			e.domainMu.Unlock()
			return true
		}
	}

	e.domainLastAccess[host] = time.Now()
	e.domainMu.Unlock()
	return true
}

func (e *Engine) worker(ctx context.Context, id int) {
	log.Printf("[Worker %d] Activ", id)

	client, err := proxy.NewTorClient(e.Proxy)
	if err != nil {
		log.Printf("[Worker %d] Eroare fatala Tor: %v", id, err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("[Worker %d] Oprit.", id)
			return
		default:
		}

		targetUrl, depth, err := e.DB.GetNextPendingNode()
		if err != nil {
			log.Printf("[Worker %d] Eroare la preluarea cozii: %v", id, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		if targetUrl == "" {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
			continue
		}

		log.Printf("[Worker %d] Asteapta permisiunea rate-limit pt: %s", id, targetUrl)
		if !e.waitForDomain(ctx, targetUrl) {
			return // context anulat in timpul asteptarii
		}
		log.Printf("[Worker %d] Scanare aprobata: %s (Adancime: %d)", id, targetUrl, depth)

		result, err := ScrapePage(ctx, client, targetUrl)
		if err != nil {
			log.Printf("[Worker %d] Eroare retea/SOCKS la %s: %v", id, targetUrl, err)
			if errRetry := e.DB.FailNodeWithRetry(targetUrl); errRetry != nil {
				log.Printf("[Worker %d] DB Eroare la retry pentru %s: %v", id, targetUrl, errRetry)
			}
			continue
		}

		if err = e.DB.SaveNode(targetUrl, result.Title, result.ServerHeader, result.StatusCode, "completed", result.Metadata, result.Content); err != nil {
			log.Printf("[Worker %d] Eroare la salvare nod: %v", id, err)
		}

		if depth < e.MaxDepth {
			for _, foundUrl := range result.FoundOnions {
				if err = e.DB.SaveEdge(targetUrl, foundUrl, depth+1); err != nil {
					log.Printf("[Worker %d] Eroare la salvare edge: %v", id, err)
				}
			}
			log.Printf("[Worker %d] Finalizat: %s (gasit %d link-uri noi)", id, targetUrl, len(result.FoundOnions))
		} else {
			log.Printf("[Worker %d] Finalizat: %s (adancime maxima %d atinsa, ignoram %d link-uri noi)", id, targetUrl, e.MaxDepth, len(result.FoundOnions))
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Second):
		}
	}
}

// AddToQueue adauga un URL manual in coada fara sa suprascrie date existente
func (e *Engine) AddToQueue(rawURL string) error {
	return e.DB.EnqueueURL(rawURL, 0)
}