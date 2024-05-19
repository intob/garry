package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/intob/garry/app"
	"github.com/intob/godave"
)

func main() {
	garryLaddr := flag.String("lg", "[::]:6102", "Garry's listen address:port")
	tlscert := flag.String("cert", "", "TLS certificate file")
	tlskey := flag.String("key", "", "TLS key file")
	daveLaddr := flag.String("ld", "[::]:1618", "Dave's listen address:port")
	edge := flag.String("e", "", "Dave's bootstrap address:port")
	dcap := flag.Int("dcap", 100000, "Dat in-memory store capacity")
	epoch := flag.Duration("epoch", 20*time.Microsecond, "Dave epoch")
	fcap := flag.Uint("fcap", 10000, "Dave's cuckoo filter capacity")
	prune := flag.Uint("prune", 50000, "Dave's prune interval in epochs")
	verbose := flag.Bool("v", false, "Verbose logging")
	flag.Parse()
	commit, _ := os.ReadFile("static/commit")
	fmt.Println(string(commit))
	var lw *bufio.Writer
	if *verbose {
		lw = bufio.NewWriter(os.Stdout)
	} else {
		lf, err := os.Open(os.DevNull)
		if err != nil {
			exit(1, "failed to open %q: %v", os.DevNull, err)
		}
		lw = bufio.NewWriter(lf)
	}
	d := makeDave(*daveLaddr, *edge, uint(*dcap), *fcap, *prune, *epoch, lw)
	lw.Flush()
	app.Run(&app.Cfg{
		Dave:      d,
		Laddr:     *garryLaddr,
		Ratelimit: 300 * time.Millisecond,
		Burst:     100,
		Dcap:      *dcap,
		TLSCert:   *tlscert,
		TLSKey:    *tlskey,
		Commit:    commit,
		ListLimit: 1000,
	})
}

func makeDave(lap, edge string, dcap, fcap, prune uint, epoch time.Duration, lw io.Writer) *godave.Dave {
	edges := make([]netip.AddrPort, 0)
	if edge != "" {
		if strings.HasPrefix(edge, ":") {
			edge = "[::1]" + edge
		}
		addr, err := netip.ParseAddrPort(edge)
		if err != nil {
			exit(1, "failed to parse -b=%q: %v", edge, err)
		}
		edges = append(edges, addr)
	}
	laddr, err := net.ResolveUDPAddr("udp", lap)
	if err != nil {
		exit(2, "failed to resolve UDP address: %v", err)
	}
	lch := make(chan []byte, 10)
	go func() {
		for l := range lch {
			lw.Write(l)
		}
	}()
	d, err := godave.NewDave(&godave.Cfg{
		LstnAddr:  laddr,
		Edges:     edges,
		DatCap:    dcap,
		FilterCap: fcap,
		Prune:     uint64(prune),
		Epoch:     epoch,
		Log:       lch})
	if err != nil {
		exit(3, "failed to make dave: %v", err)
	}
	return d
}

func exit(code int, msg string, args ...any) {
	fmt.Printf(msg, args...)
	os.Exit(code)
}
