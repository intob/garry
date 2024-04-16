package http

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/intob/dave/godave"
	"github.com/intob/dave/godave/dave"
	"golang.org/x/time/rate"
)

type App struct {
	dave     *godave.Dave
	davemu   sync.Mutex
	work     int
	ratelim  time.Duration
	burst    int
	lap      string
	tlsCert  string
	tlsKey   string
	version  string
	started  time.Time
	clientMu sync.Mutex
	clients  map[string]*client
}

type Cfg struct {
	Dave      *godave.Dave
	Work      int
	Version   string
	Lap       string
	Ratelimit time.Duration
	Burst     int
	TLSCert   string
	TLSKey    string
}

type client struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func RunApp(cfg *Cfg) {
	app := &App{
		dave:    cfg.Dave,
		work:    cfg.Work,
		ratelim: cfg.Ratelimit,
		burst:   cfg.Burst,
		lap:     cfg.Lap,
		tlsCert: cfg.TLSCert,
		tlsKey:  cfg.TLSKey,
		started: time.Now(),
		version: cfg.Version,
		clients: make(map[string]*client),
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
		head, err := hex.DecodeString(r.URL.Path[1:])
		if err != nil {
			http.Error(w, fmt.Sprintf("err decoding head: %v", err), 400)
			return
		}
		if len(head) != 32 {
			http.Error(w, "input must be of length 32 bytes", 400)
			return
		}
		app.davemu.Lock()
		defer app.davemu.Unlock()
		result := make([]byte, 0)
		fch := getFile(app.dave, app.work, head)
		t := time.After(time.Second)
		for {
			select {
			case dat, ok := <-fch:
				result = append(dat, result...)
				if !ok {
					w.Write(result)
					return
				}
			case <-t:
				w.WriteHeader(http.StatusGatewayTimeout)
				return
			}
		}
	}
}

func getFile(d *godave.Dave, work int, head []byte) <-chan []byte {
	out := make(chan []byte)
	go func() {
	init:
		for {
			select {
			case <-d.Recv:
			case d.Send <- &dave.Msg{Op: dave.Op_GETDAT, Work: head}:
				break init
			}
		}
		var i int
		for {
			select {
			case m := <-d.Recv:
				if m.Op == dave.Op_DAT && bytes.Equal(m.Work, head) {
					check := godave.CheckWork(m)
					if check < work {
						fmt.Printf("invalid work: %v, require: %v", check, work)
						close(out)
						return
					}
					out <- m.Val
					head = m.Prev
					i++
					fmt.Printf("GOT DAT %d PREV::%x\n", i, head)
					if head == nil {
						close(out)
						return
					}
				send:
					for {
						select {
						case <-d.Recv:
						case d.Send <- &dave.Msg{Op: dave.Op_GETDAT, Work: head}:
							break send
						}

					}
				}
			case <-time.After(5 * time.Second):
				d.Send <- &dave.Msg{Op: dave.Op_GETDAT, Work: head}
			}
		}
	}()
	return out
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
		limiter := app.getRateLimiter(r)
		if !limiter.Allow() {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (app *App) getRateLimiter(r *http.Request) *rate.Limiter {
	app.clientMu.Lock()
	defer app.clientMu.Unlock()
	key := r.Method + r.RemoteAddr
	v, exists := app.clients[key]
	if !exists {
		lim := rate.NewLimiter(rate.Every(app.ratelim), app.burst)
		app.clients[key] = &client{limiter: lim}
		return lim
	}
	v.lastSeen = time.Now()
	return v.limiter
}

func (a *App) cleanupClients() {
	for {
		<-time.After(10 * time.Second)
		a.clientMu.Lock()
		for key, client := range a.clients {
			if time.Since(client.lastSeen) > 10*time.Second {
				delete(a.clients, key)
			}
		}
		a.clientMu.Unlock()
	}
}
