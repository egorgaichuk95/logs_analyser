package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/local/logs-analyzer/services/tilt-collector/internal/collector"
	"github.com/local/logs-analyzer/services/tilt-collector/internal/discovery"
	"github.com/local/logs-analyzer/services/tilt-collector/internal/server"
)

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func main() {
	port := getEnv("PORT", "8084")
	ingestURL := getEnv("INGEST_URL", "http://ingest-service:8081")
	dockerSocket := getEnv("DOCKER_SOCKET_PATH", "/var/run/docker.sock")
	k8sAPIURL := getEnv("K8S_API_URL", "")

	runtime := discovery.NewRuntimeClient(dockerSocket, k8sAPIURL)
	mgr := collector.NewManager(runtime, ingestURL)
	srv := server.NewServer(runtime, mgr)

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 15 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("tilt-collector listening on port %s (ingest: %s, docker: %s)", port, ingestURL, dockerSocket)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down tilt-collector gracefully...")

	mgr.StopCapture()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("error during server shutdown: %v", err)
	}
	log.Println("tilt-collector stopped successfully")
}
