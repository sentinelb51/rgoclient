//go:build pprof

package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"
)

// Built only with `-tags pprof`. Serves the runtime profiles on loopback so a
// heap profile can be taken from a live client without shipping the endpoint.
func init() {
	go func() {
		log.Println("pprof: http://127.0.0.1:6060/debug/pprof/")
		log.Println(http.ListenAndServe("127.0.0.1:6060", nil))
	}()
}
