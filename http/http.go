package http

import (
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"net/http"
	"sync"
	"time"

	"github.com/intob/dave/dapi"
	"github.com/intob/dave/godave"
	"golang.org/x/time/rate"
)

type App struct {
	dave     *godave.Dave
	davemu   sync.Mutex
	ratelim  time.Duration
	burst    int
	lap      string
	tlsCert  string
	tlsKey   string
	clientmu sync.Mutex
	clients  map[string]*client
	cache    map[uint64]*godave.Dat
}

type Cfg struct {
	Dave      *godave.Dave
	Version   string
	Lap       string
	Ratelimit time.Duration
	Burst     int
	TLSCert   string
	TLSKey    string
}

type client struct {
	lim  *rate.Limiter
	seen time.Time
}

func RunApp(cfg *Cfg) {
	app := &App{
		dave:    cfg.Dave,
		ratelim: cfg.Ratelimit,
		burst:   cfg.Burst,
		lap:     cfg.Lap,
		tlsCert: cfg.TLSCert,
		tlsKey:  cfg.TLSKey,
		clients: make(map[string]*client),
		cache:   make(map[uint64]*godave.Dat),
	}
	go app.cleanupClients()
	go app.serve()
	<-make(chan struct{})
}

func (app *App) serve() {
	mux := http.NewServeMux()
	mux.Handle("/", app.rateLimitMiddleware(
		app.corsMiddleware(
			http.HandlerFunc(app.handleRequest))))
	server := &http.Server{Addr: app.lap, Handler: mux}
	if app.tlsCert != "" {
		fmt.Printf("app listening https on %s\n", app.lap)
		err := server.ListenAndServeTLS(app.tlsCert, app.tlsKey)
		if err != nil && err != http.ErrServerClosed {
			panic(fmt.Sprintf("failed to listen https: %v\n", err))
		}
	}
	fmt.Printf("listening http on %s\n", app.lap)
	err := server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		panic(fmt.Sprintf("failed to listen http: %v\n", err))
	}
}

func (app *App) handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method == "GET" {
		work, err := hex.DecodeString(r.URL.Path[1:])
		if err != nil {
			http.Error(w, fmt.Sprintf("err decoding input: %v", err), 400)
			return
		}
		if len(work) != 32 {
			http.Error(w, "input must be of length 32 bytes", 400)
			return
		}
		var dat *godave.Dat
		var hit bool
		dat, hit = app.cache[id(work)]
		if !hit {
			app.davemu.Lock()
			defer app.davemu.Unlock()
			dat, err = dapi.GetDat(app.dave, work, 2*time.Second)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			app.cache[id(work)] = dat
		}
		w.Write(dat.Val)
		w.Header().Set("Tag", string(dat.Tag))
		w.Header().Set("Nonce", hex.EncodeToString(dat.Nonce))
	}
}

func (app *App) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET")
		next.ServeHTTP(w, r)
	})
}

func (app *App) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lim := app.getRateLimiter(r)
		if !lim.Allow() {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (app *App) getRateLimiter(r *http.Request) *rate.Limiter {
	app.clientmu.Lock()
	defer app.clientmu.Unlock()
	key := r.Method + r.RemoteAddr
	v, exists := app.clients[key]
	if !exists {
		lim := rate.NewLimiter(rate.Every(app.ratelim), app.burst)
		app.clients[key] = &client{lim: lim}
		return lim
	}
	v.seen = time.Now()
	return v.lim
}

func (a *App) cleanupClients() {
	for {
		<-time.After(10 * time.Second)
		a.clientmu.Lock()
		for key, client := range a.clients {
			if time.Since(client.seen) > 10*time.Second {
				delete(a.clients, key)
			}
		}
		a.clientmu.Unlock()
	}
}

func id(v []byte) uint64 {
	h := fnv.New64a()
	h.Write(v)
	return h.Sum64()
}
