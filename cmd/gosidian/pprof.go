package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"time"
)

// servePprof opens Go's profiler on GOSIDIAN_PPROF_ADDR (IMP-185), to see
// where a slow server spends its time where it runs, under its real load.
// Unset, nothing opens. The profiles have no authentication, so the address
// must be a loopback one and they are read from the same host or container:
//
//	docker exec gosidian wget -qO- 'http://127.0.0.1:6060/debug/pprof/profile?seconds=30' > cpu.out
//
// The blocking and mutex profiles are sampled too while it is on: the time a
// write spends waiting does not show in the CPU profile.
func servePprof() {
	addr := os.Getenv("GOSIDIAN_PPROF_ADDR")
	if addr == "" {
		return
	}
	if err := loopbackAddr(addr); err != nil {
		log.Printf("pprof: not opened: GOSIDIAN_PPROF_ADDR=%q: %v", addr, err)
		return
	}
	runtime.SetBlockProfileRate(int(100 * time.Microsecond))
	runtime.SetMutexProfileFraction(100)

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	// No write timeout: a CPU profile or a trace answers after its seconds.
	serveHTTP(&http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}, "pprof")
	log.Printf("pprof: open on %s/debug/pprof/", addr)
}

// loopbackAddr accepts a host:port whose host is localhost or a loopback IP.
func loopbackAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("the host must be localhost or a loopback address, such as 127.0.0.1:6060")
}
