package app

import (
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
	burst           int
	tlsCert, tlsKey string
	clientmu        sync.Mutex
	clients         map[string]*client
	cache           map[uint64]*godave.Dat
	doc             []byte
	fs              http.Handler
}

type Cfg struct {
	Laddr, TLSCert, TLSKey string
	Dave                   *godave.Dave
	Ratelimit              time.Duration
	Burst                  int
	TagPrefix, Doc         []byte
}

type client struct {
	lim  *rate.Limiter
	seen time.Time
}

type msg struct {
	Val      string `json:"val"`
	Tag      string `json:"tag"`
	NonceHex string `json:"nonce"`
	WorkHex  string `json:"work"`
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
		doc:     cfg.Doc,
		fs:      http.FileServer(http.Dir(".")),
	}
	go garry.cleanupClients(10 * time.Second)
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
	msg := &msg{}
	err := jd.Decode(msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	valb := []byte(msg.Val)
	tagb := []byte(msg.Tag)
	nonce, err := hex.DecodeString(msg.NonceHex)
	if err != nil {
		http.Error(w, fmt.Sprintf("err decoding input: %v", err), http.StatusBadRequest)
		return
	}
	work, err := hex.DecodeString(msg.WorkHex)
	if err != nil {
		http.Error(w, fmt.Sprintf("err decoding input: %v", err), http.StatusBadRequest)
		return
	}
	check := godave.Check(valb, tagb, nonce, work)
	if check < godave.MINWORK {
		http.Error(w, fmt.Sprintf("invalid work: %d", check), http.StatusBadRequest)
		return
	}
	g.cache[id(work)] = &godave.Dat{Val: valb, Tag: tagb, Nonce: nonce, Work: work}
	err = dapi.SendM(g.dave, &dave.M{Op: dave.Op_SET, Val: valb, Tag: tagb, Nonce: nonce, Work: work}, 2*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (g *Garry) handleGet(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/client") {
		g.fs.ServeHTTP(w, r)
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
	dat, hit = g.cache[id(work)]
	if !hit {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Tag", string(dat.Tag))
	w.Header().Set("Nonce", hex.EncodeToString(dat.Nonce))
	w.Write(dat.Val)
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