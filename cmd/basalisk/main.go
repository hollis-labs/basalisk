// Command basalisk serves the Basalisk dashboard UI.
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hollis-labs/basalisk/internal/account"
	"github.com/hollis-labs/basalisk/internal/appdirs"
	"github.com/hollis-labs/basalisk/internal/assurance"
	"github.com/hollis-labs/basalisk/internal/requestorigin"
	"github.com/hollis-labs/basalisk/internal/webui"
)

func main() {
	addr := listenAddr()
	proxy := os.Getenv("BASALISK_HTTPS_PROXY")
	if proxy != "" && proxy != "0" && proxy != "1" {
		log.Fatal("BASALISK_HTTPS_PROXY must be 0 or 1")
	}
	dirs, err := appdirs.Resolve(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	assuranceConfig, err := assurance.LoadHostConfig(dirs.AssuranceConfigPath(os.Getenv))
	if err != nil {
		log.Fatal("Invalid Basalisk assurance configuration")
	}
	if assuranceConfig != nil && proxy != "1" {
		log.Fatal("Assurance requires BASALISK_HTTPS_PROXY=1")
	}

	hostName, _, err := net.SplitHostPort(addr)
	if err != nil || (hostName != "localhost" && (net.ParseIP(hostName) == nil || !net.ParseIP(hostName).IsLoopback())) {
		log.Fatal("Basalisk currently requires a loopback LISTEN_ADDR (for example 127.0.0.1:8080)")
	}
	store, err := account.Open(dirs.Data)
	if err != nil {
		log.Fatal("Could not open Basalisk data store: ", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			log.Printf("Could not close Basalisk data store: %v", closeErr)
		}
	}()
	statusHandler, err := store.ConfigureAssuranceHost(context.Background(), assuranceConfig, nil)
	if err != nil {
		log.Fatal("Could not configure Basalisk assurance")
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", store.Handler())
	companion := store.CompanionHandler()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go store.PruneObservations(ctx)
	worker, err := store.NewFragmentWorker(account.DeliveryConfigFromEnv(os.Getenv), nil)
	if err != nil {
		log.Fatal(err)
	}
	if worker != nil {
		done := make(chan struct{})
		go func() { defer close(done); worker.Run(ctx) }()
		defer func() { cancel(); <-done }()
	}
	h, err := webui.New(mux)
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Basalisk listening on %s; UI base %s", listener.Addr(), webui.BasePath)
	// Validate Host before the Chimera host sees a request, preventing DNS rebinding.
	handler := applicationHandler(h.Handler(), companion, statusHandler)
	server := &http.Server{Handler: requestorigin.Handler(proxy == "1", handler), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}

}

// Chimera mounts its app routes under /api/. Native enrollment and observation
// paths are separate from that cookie-authenticated surface and from SPA fallback.
func applicationHandler(ui, companion, status http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/auth/", companion)
	mux.Handle("/companion/", companion)
	if status != nil {
		mux.Handle("/assurance/", status)
	} else {
		// An unconfigured status endpoint must never return the SPA.
		mux.HandleFunc("/assurance/", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"assurance_status_unavailable"}`))
		})
	}
	mux.Handle("/", ui)
	return mux
}

func listenAddr() string {
	if addr := os.Getenv("LISTEN_ADDR"); addr != "" {
		return addr
	}
	return "127.0.0.1:8080"
}
