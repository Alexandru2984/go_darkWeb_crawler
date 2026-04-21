package crawler

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"onion-spider/internal/database"
	"onion-spider/internal/proxy"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Engine struct {
	DB               *database.DB
	Proxy            string
	Workers          int
	MaxDepth         int
	TorCtrl          *proxy.TorController // nil daca controlul Tor nu e configurat
	domainLastAccess map[string]time.Time
	domainMu         sync.Mutex
	wg               sync.WaitGroup
	cancel           context.CancelFunc
	// globalErrorCount numara erorile de retea consecutive pe toti workerii.
	// Cand depaseste pragul, se cere reinnoire circuit Tor.
	globalErrorCount atomic.Int32
	transports       []*http.Transport
	transportsMu     sync.Mutex
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

	// Goroutine de cleanup pentru domainLastAccess — previne memory leak cu mii de domenii
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cutoff := time.Now().Add(-10 * time.Minute)
				e.domainMu.Lock()
				for host, t := range e.domainLastAccess {
					if t.Before(cutoff) {
						delete(e.domainLastAccess, host)
					}
				}
				e.domainMu.Unlock()
			}
		}
	}()
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

	transport, client, err := proxy.NewTorClientWithTransport(e.Proxy)
	if err != nil {
		log.Printf("[Worker %d] Eroare fatala Tor: %v", id, err)
		return
	}

	// Inregistrare transport pentru CloseIdleConnections dupa NEWNYM
	e.transportsMu.Lock()
	e.transports = append(e.transports, transport)
	e.transportsMu.Unlock()

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

		// Validare la crawl-time: procesam exclusiv adrese .onion
		if parsedTarget, err := url.Parse(targetUrl); err != nil || !strings.HasSuffix(strings.ToLower(parsedTarget.Hostname()), ".onion") {
			log.Printf("[Worker %d] URL non-.onion in DB, marcat ca invalid: %s", id, targetUrl)
			_ = e.DB.MarkRobotsBlocked(targetUrl)
			continue
		}

		log.Printf("[Worker %d] Asteapta permisiunea rate-limit pt: %s", id, targetUrl)
		if !e.waitForDomain(ctx, targetUrl) {
			return // context anulat in timpul asteptarii
		}
		log.Printf("[Worker %d] Scanare aprobata: %s (Adancime: %d)", id, targetUrl, depth)

		// Verificam robots.txt inainte de scraping
		if !IsAllowed(ctx, client, targetUrl) {
			log.Printf("[Worker %d] Blocat de robots.txt: %s", id, targetUrl)
			if err := e.DB.MarkRobotsBlocked(targetUrl); err != nil {
				log.Printf("[Worker %d] DB Eroare la marcare robots blocked: %v", id, err)
			}
			continue
		}

		result, err := ScrapePage(ctx, client, targetUrl)
		if err != nil {
			log.Printf("[Worker %d] Eroare retea/SOCKS la %s: %v", id, targetUrl, err)
			if errRetry := e.DB.FailNodeWithRetry(targetUrl); errRetry != nil {
				log.Printf("[Worker %d] DB Eroare la retry pentru %s: %v", id, targetUrl, errRetry)
			}
			e.onNetworkError()
			continue
		}
		// Succes — resetam contorul de erori
		e.globalErrorCount.Store(0)
		changed, err := e.DB.SaveNode(targetUrl, result.Title, result.ServerHeader, result.StatusCode, "completed", result.Metadata, result.Content, result.Category)
		if err != nil {
			log.Printf("[Worker %d] Eroare la salvare nod: %v", id, err)
			if retryErr := e.DB.FailNodeWithRetry(targetUrl); retryErr != nil {
				log.Printf("[Worker %d] Eroare la retry dupa esec SaveNode: %v", id, retryErr)
			}
		} else if !changed {
			log.Printf("[Worker %d] Continut nemodificat (hash identic): %s", id, targetUrl)
		}

		if depth < e.MaxDepth {
			for _, foundUrl := range result.FoundOnions {
				if err = e.DB.SaveEdge(targetUrl, foundUrl, depth+1); err != nil {
					log.Printf("[Worker %d] Eroare la salvare edge: %v", id, err)
				}
			}
			// La depth=0 descarcam si sitemap.xml pentru descoperire suplimentara
			if depth == 0 {
				sitemapURLs := FetchSitemapURLs(ctx, client, targetUrl)
				for _, su := range sitemapURLs {
					if err = e.DB.SaveEdge(targetUrl, su, 1); err != nil {
						log.Printf("[Worker %d] Eroare sitemap edge: %v", id, err)
					}
				}
				if len(sitemapURLs) > 0 {
					log.Printf("[Worker %d] Sitemap: %d URL-uri suplimentare de la %s", id, len(sitemapURLs), targetUrl)
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

// onNetworkError incrementeaza contorul global de erori si cere reinnoire circuit
// daca pragul de 5 erori consecutive a fost depasit.
func (e *Engine) onNetworkError() {
	count := e.globalErrorCount.Add(1)
	const threshold = 5
	if count < threshold || e.TorCtrl == nil {
		return
	}
	renewed, err := e.TorCtrl.RenewCircuit()
	if err != nil {
		log.Printf("[TorCtrl] Eroare la reinnoire circuit: %v", err)
		return
	}
	if renewed {
		e.globalErrorCount.Store(0)
		// Inchidem conexiunile idle — circuitul vechi nu mai e valid
		e.transportsMu.Lock()
		for _, t := range e.transports {
			t.CloseIdleConnections()
		}
		e.transportsMu.Unlock()
	}
}
// AddToQueue adauga un URL manual in coada fara sa suprascrie date existente
func (e *Engine) AddToQueue(rawURL string) error {
return e.DB.EnqueueURL(rawURL, 0)
}
