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
	"github.com/intob/daveg/http"
)

func main() {
	network := flag.String("network", "udp", "<udp|udp6|udp4>")
	lap := flag.String("l", ":80", "<LAP> listen address:port")
	applap := flag.String("applap", ":8080", "<APPLAP> app listen address:port")
	bap := flag.String("b", "", "<BAP> bootstrap address:port")
	bfile := flag.String("bf", "", "<BFILE> bootstrap file of address:port\\n")
	work := flag.Int("w", 3, "<WORK> minimum work to store DAT")
	flag.Parse()
	d := runDave(*network, *lap, *bfile, *bap, *work)
	http.RunApp(&http.Cfg{
		Dave:      d,
		Work:      *work,
		Version:   "1234",
		Lap:       *applap,
		Ratelimit: 300 * time.Millisecond,
		Burst:     10,
	})
}

func runDave(network, lap, bfile, bap string, work int) *godave.Dave {
	bootstrap := make([]netip.AddrPort, 0)
	if bap != "" {
		if strings.HasPrefix(bap, ":") {
			bap = "[::1]" + bap
		}
		addr, err := netip.ParseAddrPort(bap)
		if err != nil {
			exit(1, "failed to parse -b=%q: %v", bap, err)
		}
		bootstrap = append(bootstrap, addr)
	}
	if bfile != "" {
		bootstrap = append(bootstrap, readBaps(bfile)...)
	}

	laddr, err := net.ResolveUDPAddr(network, lap)
	if err != nil {
		exit(1, "failed to resolve UDP address: %v", err)
	}
	d, err := godave.NewDave(work, laddr, bootstrap)
	if err != nil {
		exit(1, "failed to make dave: %v", err)
	}

	var n int
	fmt.Printf("%v\nbootstrap\n", bootstrap)
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
