package app

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/intob/dave/dapi"
	"github.com/intob/dave/godave"
	"github.com/intob/dave/godave/dave"
	"golang.org/x/time/rate"
)

type Garry struct {
	dave            *godave.Dave
	ratelim         time.Duration
	burst           uint
	tlsCert, tlsKey string
	clientmu        sync.Mutex
	clients         map[string]*client
	cache           map[uint64]*godave.Dat
	cachemu         sync.RWMutex
	fs              http.Handler
}

type Cfg struct {
	Laddr, TLSCert, TLSKey string
	Dave                   *godave.Dave
	Ratelimit              time.Duration
	Burst, Cap             uint
}

type client struct {
	lim  *rate.Limiter
	seen time.Time
}

type datjson struct {
	Val   string `json:"val"`
	Nonce string `json:"nonce"`
	Work  string `json:"work"`
	Time  int64  `json:"time"`
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
		fs:      http.FileServer(http.Dir(".")),
	}
	go garry.cleanupClients(10 * time.Second)
	go garry.serve(cfg.Laddr)
	go garry.store()
	go garry.prune(cfg.Cap)
	<-make(chan struct{})
}

func (g *Garry) store() {
	for m := range g.dave.Recv {
		if m.Op == dave.Op_DAT {
			g.cachemu.RLock()
			_, ok := g.cache[id(m.Work)]
			g.cachemu.RUnlock()
			if !ok {
				g.cachemu.Lock()
				g.cache[id(m.Work)] = &godave.Dat{V: m.Val, N: m.Nonce, W: m.Work, Ti: godave.Btt(m.Time)}
				g.cachemu.Unlock()
			}
		}
	}
}

func (g *Garry) prune(cap uint) {
	ti := time.NewTicker(godave.EPOCH * godave.PRUNE)
	for range ti.C {
		g.cachemu.Lock()
		nc := make(map[uint64]*godave.Dat)
		var minw float64
		var l uint64
		for k, d := range g.cache {
			w := godave.Weight(d.W, d.Ti)
			if len(nc) >= int(cap)-1 {
				if w > minw {
					delete(nc, l)
					nc[k] = d
					l = k
					minw = w
				}
			} else {
				if w < minw {
					minw = w
				}
				nc[k] = d
			}
		}
		g.cache = nc
		g.cachemu.Unlock()
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
	dj := &datjson{}
	err := jd.Decode(dj)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	nonce, err := hex.DecodeString(dj.Nonce)
	if err != nil {
		http.Error(w, fmt.Sprintf("err decoding input: %v", err), http.StatusBadRequest)
		return
	}
	work, err := hex.DecodeString(dj.Work)
	if err != nil {
		http.Error(w, fmt.Sprintf("err decoding input: %v", err), http.StatusBadRequest)
		return
	}
	t := time.UnixMilli(dj.Time)
	check := godave.Check([]byte(dj.Val), godave.Ttb(t), nonce, work)
	if check < 0 {
		http.Error(w, fmt.Sprintf("invalid work: %d", check), http.StatusBadRequest)
		return
	}
	g.cachemu.Lock()
	g.cache[id(work)] = &godave.Dat{V: []byte(dj.Val), N: nonce, W: work, Ti: t}
	g.cachemu.Unlock()
	err = dapi.SendM(g.dave, &dave.M{Op: dave.Op_DAT, Val: []byte(dj.Val), Time: godave.Ttb(t), Nonce: nonce, Work: work})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (g *Garry) handleGet(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/client") {
		w.Header().Set("Cache-Control", "max-age=300")
		g.fs.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/list/") {
		g.handleGetList(w, r)
		return
	}
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
	g.cachemu.RLock()
	dat, hit = g.cache[id(work)]
	g.cachemu.RUnlock()
	if !hit {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Nonce", hex.EncodeToString(dat.N))
	w.Write(dat.V)
}

func (g *Garry) handleGetList(w http.ResponseWriter, r *http.Request) {
	q := []byte(r.URL.Path[len("/list/"):])
	a := make([]*datjson, 0)
	g.cachemu.RLock()
	for _, d := range g.cache {
		if bytes.HasPrefix(d.V, q) {
			a = append(a, &datjson{
				Val:   string(d.V),
				Nonce: hex.EncodeToString(d.N),
				Work:  hex.EncodeToString(d.W),
				Time:  d.Ti.UnixMilli()})
		}
	}
	g.cachemu.RUnlock()
	rj, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write(rj)
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
		lim := rate.NewLimiter(rate.Every(g.ratelim), int(g.burst))
		g.clients[key] = &client{lim: lim}
		return lim
	}
	v.seen = time.Now()
	return v.lim
}

func (g *Garry) cleanupClients(period time.Duration) {
	for {
		<-time.After(period)
		g.clientmu.Lock()
		for key, client := range g.clients {
			if time.Since(client.seen) > period {
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
