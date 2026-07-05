// codex-hub — local daemon that owns codex sessions.
//
//	codex-hub [-port 4870] [-data ~/.codex-hub]
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/yan5xu/codex-hub/internal/httpapi"
	"github.com/yan5xu/codex-hub/internal/hub"
	"github.com/yan5xu/codex-hub/internal/store"
	"github.com/yan5xu/codex-hub/internal/webui"
)

func main() {
	defaultPort := 4870
	if p := os.Getenv("CODEX_HUB_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			defaultPort = v
		}
	}
	port := flag.Int("port", defaultPort, "listen port")
	dataDir := flag.String("data", store.DefaultDir(), "data directory")
	flag.Parse()

	st, err := store.Open(*dataDir)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	h := hub.New(st)
	srv := httpapi.New(h, st, webui.FS())

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: srv.Handler(),
	}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		s := <-sig
		log.Printf("[codex-hub] %s — shutting down", s)
		h.Shutdown()
		_ = httpServer.Close()
		os.Exit(0)
	}()

	log.Printf("[codex-hub] listening on http://localhost:%d (data: %s)", *port, *dataDir)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
