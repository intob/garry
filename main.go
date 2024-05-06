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
	garryLaddr := flag.String("lg", "[::]:8080", "Garry listen address:port")
	tlscert := flag.String("cert", "", "TLS certificate file")
	tlskey := flag.String("key", "", "TLS key file")
	daveLaddr := flag.String("ld", "[::]:0", "Dave listen address:port")
	bap := flag.String("b", "", "Dave bootstrap address:port")
	fcap := flag.Uint("fc", 1000, "Cuckoo filter capacity")
	dcap := flag.Uint("dc", 100000, "Dat in-memory store capacity")
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
	d := makeDave(*daveLaddr, *bap, *fcap, *dcap, lw)
	lw.Flush()
	app.Run(&app.Cfg{
		Dave:      d,
		Laddr:     *garryLaddr,
		Ratelimit: time.Second,
		Burst:     10,
		Cap:       *dcap,
		TLSCert:   *tlscert,
		TLSKey:    *tlskey,
		Commit:    commit,
		ListLimit: 1000,
	})
}

func makeDave(lap, bap string, fcap, dcap uint, lw io.Writer) *godave.Dave {
	bootstraps := make([]netip.AddrPort, 0)
	if bap != "" {
		if strings.HasPrefix(bap, ":") {
			bap = "[::1]" + bap
		}
		addr, err := netip.ParseAddrPort(bap)
		if err != nil {
			exit(1, "failed to parse -b=%q: %v", bap, err)
		}
		bootstraps = append(bootstraps, addr)
	}
	laddr, err := net.ResolveUDPAddr("udp", lap)
	if err != nil {
		exit(2, "failed to resolve UDP address: %v", err)
	}
	lch := make(chan string, 1)
	go func() {
		for l := range lch {
			lw.Write([]byte(l))
		}
	}()
	d, err := godave.NewDave(&godave.Cfg{
		Listen:     laddr,
		Bootstraps: bootstraps,
		FilterCap:  fcap,
		DatCap:     dcap,
		Log:        lch})
	if err != nil {
		exit(3, "failed to make dave: %v", err)
	}
	return d
}

func exit(code int, msg string, args ...any) {
	fmt.Printf(msg, args...)
	os.Exit(code)
}
