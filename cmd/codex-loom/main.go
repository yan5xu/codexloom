// codex-loom is the local CodexLoom service. It governs durable Agents whose
// execution history lives in Codex Threads.
//
//	codex-loom [-port 4870] [-data ~/.codex-loom]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/yan5xu/codex-loom/internal/httpapi"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
	"github.com/yan5xu/codex-loom/internal/webui"
)

func main() {
	startedAt := time.Now()
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
	canary := flag.Bool("canary", false, "run a passive, read-only development canary")
	flag.Parse()

	st, err := store.Open(*dataDir)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	h, err := hub.OpenWithOptions(st, hub.OpenOptions{Passive: *canary})
	if err != nil {
		log.Fatalf("open Hub state: %v", err)
	}
	var startup sync.WaitGroup
	mode := "normal"
	if *canary {
		mode = "canary"
	}
	srv := httpapi.NewWithOptions(h, st, webui.FS(), httpapi.Options{StartedAt: startedAt, Mode: mode, ReadOnly: *canary})
	if !*canary {
		startup.Add(3)
		go func() {
			defer startup.Done()
			if err := h.SyncThreadNames(); err != nil {
				log.Printf("[codex-loom] sync Codex Thread names: %v", err)
			}
		}()
		go func() {
			defer startup.Done()
			srv.RestartManagedGateways()
		}()
		go func() {
			defer startup.Done()
			srv.ResumeRestartPausedGoals()
		}()
	}

	listenAddress := fmt.Sprintf(":%d", *port)
	if *canary {
		listenAddress = fmt.Sprintf("127.0.0.1:%d", *port)
	}
	httpServer := &http.Server{
		Addr:    listenAddress,
		Handler: srv.Handler(),
	}

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		s := <-sig
		log.Printf("[codex-loom] %s — shutting down", s)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = httpServer.Shutdown(ctx)
		cancel()
		_ = httpServer.Close() // Close long-lived SSE streams after the request grace window.
		startup.Wait()
		h.Shutdown()
	}()

	log.Printf("[codex-loom] listening on http://localhost:%d (mode: %s, data: %s)", *port, mode, *dataDir)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	<-shutdownDone
}
