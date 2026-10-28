package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/local/logs-analyzer/services/ingest-service/internal/chwriter"
	"github.com/local/logs-analyzer/services/ingest-service/internal/server"
)

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func main() {
	port := getEnv("PORT", "8081")
	chURL := getEnv("CLICKHOUSE_HTTP_URL", "http://clickhouse:8123")
	importDir := getEnv("IMPORT_DIR", "/import")
	exampleDir := getEnv("EXAMPLE_DIR", "/log_example")

	writer := chwriter.NewWriter(chURL)
	srv := server.NewServer(writer, importDir, exampleDir)

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 15 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("ingest-service listening on port %s (clickhouse: %s)", port, chURL)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down ingest-service gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("error during server shutdown: %v", err)
	}
	log.Println("ingest-service stopped successfully")
}
