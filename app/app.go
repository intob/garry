package app

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/intob/godave"
	"github.com/intob/godave/dave"
	"golang.org/x/time/rate"
)

type Garry struct {
	dave            *godave.Dave
	ratelim         time.Duration
	burst, listlim  int
	tlsCert, tlsKey string
	commit          []byte
	clientmu        sync.Mutex
	clients         map[string]*client
	cache           map[uint64]map[uint64]godave.Dat
	cachemu         sync.RWMutex
	fs              http.Handler
}

type Cfg struct {
	Laddr, TLSCert, TLSKey string
	Commit                 []byte
	Dave                   *godave.Dave
	Ratelimit              time.Duration
	Burst, Dcap, ListLimit int
}

type client struct {
	lim  *rate.Limiter
	seen time.Time
}

type datjson struct {
	Val  string `json:"val"`
	Time int64  `json:"time"`
	Salt string `json:"salt"`
	Work string `json:"work"`
}

func Run(cfg *Cfg) {
	garry := &Garry{
		dave:    cfg.Dave,
		ratelim: cfg.Ratelimit,
		burst:   cfg.Burst,
		tlsCert: cfg.TLSCert,
		tlsKey:  cfg.TLSKey,
		commit:  cfg.Commit,
		clients: make(map[string]*client),
		cache:   make(map[uint64]map[uint64]godave.Dat),
		fs:      http.FileServer(http.Dir(".")),
		listlim: cfg.ListLimit,
	}
	go garry.cleanupClients(10 * time.Second)
	go garry.serve(cfg.Laddr)
	go garry.store()
	go garry.prune(cfg.Dcap, 10*time.Second)
	<-make(chan struct{})
}

func (g *Garry) store() {
	for m := range g.dave.Recv {
		if m.Op == dave.Op_DAT {
			shardi, dati, err := workid(m.W)
			if err != nil {
				continue
			}
			g.cachemu.Lock()
			_, ok := g.cache[shardi]
			if !ok {
				g.cache[shardi] = make(map[uint64]godave.Dat)
			}
			g.cache[shardi][dati] = godave.Dat{V: m.V, S: m.S, W: m.W, Ti: godave.Btt(m.T)}
			g.cachemu.Unlock()
		}
	}
}

func (g *Garry) prune(dcap int, interval time.Duration) {
	ti := time.NewTicker(interval)
	for range ti.C {
		g.cachemu.Lock()
		type hdat struct {
			shard, key uint64
			dat        godave.Dat
		}
		heaviest := make([]hdat, dcap)
		for shardId, shard := range g.cache {
			for key, dat := range shard {
				if len(heaviest) < dcap {
					heaviest = append(heaviest, hdat{shardId, key, dat})
					if len(heaviest) == dcap {
						sort.Slice(heaviest, func(i, j int) bool {
							return godave.Mass(heaviest[i].dat.W, heaviest[i].dat.Ti) < godave.Mass(heaviest[j].dat.W, heaviest[j].dat.Ti)
						})
					}
				} else if godave.Mass(dat.W, dat.Ti) > godave.Mass(heaviest[0].dat.W, heaviest[0].dat.Ti) {
					heaviest[0] = hdat{shardId, key, dat}
					sort.Slice(heaviest, func(i, j int) bool {
						return godave.Mass(heaviest[i].dat.W, heaviest[i].dat.Ti) < godave.Mass(heaviest[j].dat.W, heaviest[j].dat.Ti)
					})
				}
			}
		}
		newdats := make(map[uint64]map[uint64]godave.Dat, len(heaviest))
		for _, hdat := range heaviest {
			if _, ok := newdats[hdat.shard]; !ok {
				newdats[hdat.shard] = make(map[uint64]godave.Dat)
			}
			newdats[hdat.shard][hdat.key] = hdat.dat
		}
		g.cache = newdats
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
	salt, err := hex.DecodeString(dj.Salt)
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
	check := godave.Check([]byte(dj.Val), godave.Ttb(t), salt, work)
	if check < 0 {
		http.Error(w, fmt.Sprintf("invalid work: %d", check), http.StatusBadRequest)
		return
	}
	shardi, dati, err := workid(work)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	g.cachemu.Lock()
	_, ok := g.cache[shardi]
	if !ok {
		g.cache[shardi] = make(map[uint64]godave.Dat)
	}
	g.cache[shardi][dati] = godave.Dat{V: []byte(dj.Val), S: salt, W: work, Ti: t}
	g.cachemu.Unlock()
	g.dave.Set(godave.Dat{V: []byte(dj.Val), Ti: t, S: salt, W: work}, 9, 16)
}

func (g *Garry) handleGet(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" { // redirect to static
		r.URL.Path += "static"
	}
	if strings.HasPrefix(r.URL.Path, "/static") {
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
	shardi, dati, err := workid(work)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	g.cachemu.RLock()
	dat, hit := g.cache[shardi][dati]
	g.cachemu.RUnlock()
	if !hit {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Salt", hex.EncodeToString(dat.S))
	w.Header().Set("Time", hex.EncodeToString(godave.Ttb(dat.Ti)))
	w.Write(dat.V)
}

func (g *Garry) handleGetList(w http.ResponseWriter, r *http.Request) {
	q := []byte(r.URL.Path[len("/list/"):])
	a := make([]*datjson, 0)
	g.cachemu.RLock()
	for _, shard := range g.cache {
		for _, d := range shard {
			if bytes.HasPrefix(d.V, q) {
				a = append(a, &datjson{
					Val:  string(d.V),
					Salt: hex.EncodeToString(d.S),
					Work: hex.EncodeToString(d.W),
					Time: d.Ti.UnixMilli()})
				if len(a) >= g.listlim {
					break
				}
			}
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

func workid(v []byte) (shardi uint64, dati uint64, err error) {
	if len(v) != 32 {
		return 0, 0, errors.New("value is not of length 32 bytes")
	}
	h := fnv.New64a()
	h.Write(v[:16])
	shardi = h.Sum64()
	h.Reset()
	h.Write(v[16:])
	dati = h.Sum64()
	return
}
