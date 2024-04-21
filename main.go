package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/intob/dave/godave"
	"github.com/intob/garry/app"
)

func main() {
	daveLaddr := flag.String("ld", "[::]:0", "<DAVE_LADDR> dave listen address:port")
	bap := flag.String("b", "", "<BAP> bootstrap address:port")
	bfile := flag.String("bf", "", "<BFILE> bootstrap file of address:port\\n")
	garryLaddr := flag.String("la", "[::]:8080", "<GARRY_LADDR> garry listen address:port")
	flag.Parse()
	app.Run(&app.Cfg{
		Dave:      startDave(*daveLaddr, *bfile, *bap),
		Laddr:     *garryLaddr,
		Ratelimit: 100 * time.Millisecond,
		Burst:     10,
	})
}

func startDave(lap, bfile, bap string) *godave.Dave {
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
	if bfile != "" {
		bootstraps = append(bootstraps, readBaps(bfile)...)
	}

	laddr, err := net.ResolveUDPAddr("udp", lap)
	if err != nil {
		exit(1, "failed to resolve UDP address: %v", err)
	}
	d, err := godave.NewDave(&godave.Cfg{Listen: laddr, Bootstraps: bootstraps, Log: os.Stderr})
	if err != nil {
		exit(1, "failed to make dave: %v", err)
	}

	var n int
	fmt.Printf("%v\nbootstrap\n", bootstraps)
	for range d.Recv {
		n++
		fmt.Printf(".\033[0K")
		if n >= 8 {
			fmt.Print("\n\033[0K")
			break
		}
	}
	return d
}

func readBaps(fname string) []netip.AddrPort {
	addrs := make([]netip.AddrPort, 0)
	f, err := os.Open(fname)
	if err != nil {
		exit(1, "readBaps failed: %v", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		l := s.Text()
		if l != "" && !strings.HasPrefix(l, "#") {
			l = strings.ReplaceAll(l, "\t", " ")
			fields := strings.Split(l, " ")
			if len(fields) == 0 {
				continue
			}
			addr, err := netip.ParseAddrPort(fields[0])
			if err != nil {
				fmt.Println(err)
				continue
			}
			addrs = append(addrs, addr)
		}
	}
	return addrs
}

func exit(code int, msg string, args ...any) {
	fmt.Printf(msg, args...)
	os.Exit(code)
}
