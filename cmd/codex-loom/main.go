// codex-loom is the local CodexLoom service. It governs durable Agents whose
// execution history lives in Codex Threads.
//
//	codex-loom [-port 4870] [-data ~/.codex-loom]
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

	"github.com/yan5xu/codex-loom/internal/httpapi"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
	"github.com/yan5xu/codex-loom/internal/webui"
)

func main() {
	defaultPort := 4870
	p := os.Getenv("CODEX_LOOM_PORT")
	if p == "" {
		p = os.Getenv("CODEX_HUB_PORT")
	}
	if p != "" {
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
	go func() {
		if err := h.SyncThreadNames(); err != nil {
			log.Printf("[codex-loom] sync Codex Thread names: %v", err)
		}
	}()
	srv := httpapi.New(h, st, webui.FS())

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: srv.Handler(),
	}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		s := <-sig
		log.Printf("[codex-loom] %s — shutting down", s)
		h.Shutdown()
		_ = httpServer.Close()
		os.Exit(0)
	}()

	log.Printf("[codex-loom] listening on http://localhost:%d (data: %s)", *port, *dataDir)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
