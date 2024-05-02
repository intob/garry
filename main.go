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

	"github.com/intob/dave/dapi"
	"github.com/intob/dave/godave"
	"github.com/intob/garry/app"
)

func main() {
	daveLaddr := flag.String("ld", "[::]:0", "<DAVE_LADDR> dave listen address:port")
	bap := flag.String("b", "", "<BAP> bootstrap address:port")
	garryLaddr := flag.String("la", "[::]:8080", "<GARRY_LADDR> garry listen address:port")
	fcap := flag.Uint("fc", 1000000, "<FCAP> size of cuckoo filter")
	dcap := flag.Uint("dc", 1000000, "<DCAP> number of DATs to store")
	verbose := flag.Bool("v", false, "verbose logging")
	flag.Parse()
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
	dapi.WaitForFirstDat(d, os.Stdout)
	app.Run(&app.Cfg{
		Dave:      d,
		Laddr:     *garryLaddr,
		Ratelimit: 100 * time.Millisecond,
		Burst:     10,
		Cap:       *dcap,
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
