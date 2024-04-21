package app

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

type Garry struct {
	dave     *godave.Dave
	ratelim  time.Duration
	burst    int
	tlsCert  string
	tlsKey   string
	clientmu sync.Mutex
	clients  map[string]*client
	cache    map[uint64]*godave.Dat
}

type Cfg struct {
	Dave      *godave.Dave
	Laddr     string
	Ratelimit time.Duration
	Burst     int
	TagPrefix []byte
	TLSCert   string
	TLSKey    string
}

type client struct {
	lim  *rate.Limiter
	seen time.Time
}

func Run(cfg *Cfg) {
	garry := &Garry{
		dave:    cfg.Dave,
		ratelim: cfg.Ratelimit,
		burst:   cfg.Burst,
		tlsCert: cfg.TLSCert,
		tlsKey:  cfg.TLSKey,
		clients: make(map[string]*client),
		cache:   make(map[uint64]*godave.Dat),
	}
	go garry.cleanupClients()
	go garry.serve(cfg.Laddr)
	go garry.store()
	<-make(chan struct{})
}

func (g *Garry) store() {
	for m := range g.dave.Recv {
		if m.Op == dave.Op_DAT || m.Op == dave.Op_RAND {
			g.cache[id(m.Work)] = &godave.Dat{Val: m.Val, Tag: m.Tag, Nonce: m.Nonce, Work: m.Work}
		}
	}
}

func (g *Garry) serve(laddr string) {
	mux := http.NewServeMux()
	mux.Handle("/", g.rateLimitMiddleware(
		g.corsMiddleware(
			http.HandlerFunc(g.handleRequest))))
	server := &http.Server{Addr: laddr, Handler: mux}
	if g.tlsCert != "" {
		fmt.Printf("app listening https on %s\n", laddr)
		err := server.ListenAndServeTLS(g.tlsCert, g.tlsKey)
		if err != nil && err != http.ErrServerClosed {
			panic(fmt.Sprintf("failed to listen https: %v\n", err))
		}
	}
	fmt.Printf("listening http on %s\n", laddr)
	err := server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		panic(fmt.Sprintf("failed to listen http: %v\n", err))
	}
}

func (g *Garry) handleRequest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "OPTIONS":
		w.WriteHeader(http.StatusOK)
	case "GET":
		g.handleGet(w, r)
	case "POST":
		g.handlePost(w, r)
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (g *Garry) handlePost(w http.ResponseWriter, r *http.Request) {
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
	g.cache[id(dat.Work)] = dat
	err = dapi.SendM(g.dave, &dave.M{Op: dave.Op_SET, Val: dat.Val, Tag: dat.Tag, Nonce: dat.Nonce, Work: dat.Work}, 2*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (g *Garry) handleGet(w http.ResponseWriter, r *http.Request) {
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
	dat, hit = g.cache[id(work)]
	if !hit {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Write(dat.Val)
	w.Header().Set("Tag", string(dat.Tag))
	w.Header().Set("Nonce", hex.EncodeToString(dat.Nonce))
}

func (g *Garry) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET")
		next.ServeHTTP(w, r)
	})
}

func (g *Garry) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lim := g.getRateLimiter(r)
		if !lim.Allow() {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Garry) getRateLimiter(r *http.Request) *rate.Limiter {
	g.clientmu.Lock()
	defer g.clientmu.Unlock()
	key := r.Method + r.RemoteAddr
	v, exists := g.clients[key]
	if !exists {
		lim := rate.NewLimiter(rate.Every(g.ratelim), g.burst)
		g.clients[key] = &client{lim: lim}
		return lim
	}
	v.seen = time.Now()
	return v.lim
}

func (g *Garry) cleanupClients() {
	for {
		<-time.After(10 * time.Second)
		g.clientmu.Lock()
		for key, client := range g.clients {
			if time.Since(client.seen) > 10*time.Second {
				delete(g.clients, key)
			}
		}
		g.clientmu.Unlock()
	}
}

func id(v []byte) uint64 {
	h := fnv.New64a()
	h.Write(v)
	return h.Sum64()
}
