//go:build pprof

package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"
)

// Built only with `-tags pprof`. Serves the runtime profiles on loopback so a
// heap profile can be taken from a live client without shipping the endpoint.
// goroutineleak is the one to reach for here: App.epoch drops the workers of a
// replaced session rather than joining them, so one that outlived its session
// shows up as a goroutine nothing left alive can unblock.
func init() {
	go func() {
		log.Println("pprof: http://127.0.0.1:6060/debug/pprof/")
		log.Println("leaks: http://127.0.0.1:6060/debug/pprof/goroutineleak?debug=1")
		log.Println(http.ListenAndServe("127.0.0.1:6060", nil))
	}()
}
