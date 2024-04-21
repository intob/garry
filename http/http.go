package http

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"sync"
	"time"

	"github.com/intob/dave/dapi"
	"github.com/intob/dave/godave"
	"github.com/intob/dave/godave/dave"
	"golang.org/x/time/rate"
)

type App struct {
	dave     *godave.Dave
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
	go app.store()
	<-make(chan struct{})
}

func (app *App) store() {
	for m := range app.dave.Recv {
		if m.Op == dave.Op_DAT || m.Op == dave.Op_RAND {
			app.cache[id(m.Work)] = &godave.Dat{Val: m.Val, Tag: m.Tag, Nonce: m.Nonce, Work: m.Work}
		}
	}
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
	switch r.Method {
	case "OPTIONS":
		w.WriteHeader(http.StatusOK)
	case "GET":
		app.handleGet(w, r)
	case "POST":
		app.handlePost(w, r)
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (app *App) handlePost(w http.ResponseWriter, r *http.Request) {
	jd := json.NewDecoder(r.Body)
	dat := &godave.Dat{}
	err := jd.Decode(dat)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	check := godave.Check(dat.Val, dat.Tag, dat.Nonce, dat.Work)
	if check < godave.MINWORK {
		http.Error(w, fmt.Sprintf("invalid work: %d", check), http.StatusBadRequest)
		return
	}
	app.cache[id(dat.Work)] = dat
	err = dapi.SendM(app.dave, &dave.M{Op: dave.Op_SET, Val: dat.Val, Tag: dat.Tag, Nonce: dat.Nonce, Work: dat.Work}, 2*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (app *App) handleGet(w http.ResponseWriter, r *http.Request) {
	work, err := hex.DecodeString(r.URL.Path[1:])
	if err != nil {
		http.Error(w, fmt.Sprintf("err decoding input: %v", err), http.StatusBadRequest)
		return
	}
	if len(work) != 32 {
		http.Error(w, "input must be of length 32 bytes", http.StatusBadRequest)
		return
	}
	var dat *godave.Dat
	var hit bool
	dat, hit = app.cache[id(work)]
	if !hit {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Write(dat.Val)
	w.Header().Set("Tag", string(dat.Tag))
	w.Header().Set("Nonce", hex.EncodeToString(dat.Nonce))
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
