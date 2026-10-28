package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/local/logs-analyzer/services/gateway-api/internal/explorer"
	"github.com/local/logs-analyzer/services/gateway-api/internal/metadb"
	"github.com/local/logs-analyzer/services/gateway-api/internal/server"
	"github.com/local/logs-analyzer/services/gateway-api/internal/storage"
)

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt64(key string, defaultVal int64) int64 {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
	}
	return defaultVal
}

func main() {
	chURL := getEnv("CLICKHOUSE_HTTP_URL", "http://clickhouse:8123")
	ingestURL := getEnv("INGEST_URL", "http://ingest-service:8081")
	rcaURL := getEnv("RCA_URL", "http://rca-engine:8082")
	evidenceURL := getEnv("EVIDENCE_URL", "http://evidence-service:8083")
	tiltURL := getEnv("TILT_COLLECTOR_URL", "http://tilt-collector:8084")

	metaPath := os.Getenv("META_STATE_PATH")
	if metaPath == "" {
		metaPath = os.Getenv("SQLITE_PATH")
	}
	if metaPath == "" {
		metaPath = "/data/meta/state.json"
	}

	stagingDir := getEnv("STAGING_DIR", "/data/staging")
	reportsDir := getEnv("REPORTS_DIR", "/data/reports")
	quotaBytes := getEnvInt64("DISK_QUOTA_BYTES", 16106127360)
	rawQuotaBytes := getEnvInt64("RAW_QUOTA_BYTES", 10737418240)
	port := getEnv("PORT", "8080")

	log.Printf("[gateway-api] Starting on port %s...", port)
	log.Printf("[gateway-api] Config: ClickHouse=%s, Ingest=%s, RCA=%s, Evidence=%s, Tilt=%s", chURL, ingestURL, rcaURL, evidenceURL, tiltURL)
	log.Printf("[gateway-api] Storage: MetaPath=%s, Staging=%s, Reports=%s", metaPath, stagingDir, reportsDir)

	metaStore, err := metadb.NewStore(metaPath)
	if err != nil {
		log.Fatalf("[gateway-api] Failed to initialize metadata store: %v", err)
	}

	storageMgr := storage.NewManager(
		chURL,
		stagingDir,
		reportsDir,
		quotaBytes,
		rawQuotaBytes,
		metaStore,
	)

	exp := explorer.NewExplorer(
		chURL,
		metaStore,
		&http.Client{Timeout: 30 * time.Second},
	)

	cfg := server.Config{
		CHHTTPURL:        chURL,
		IngestURL:        ingestURL,
		RCAURL:           rcaURL,
		EvidenceURL:      evidenceURL,
		TiltCollectorURL: tiltURL,
		StagingDir:       stagingDir,
		ReportsDir:       reportsDir,
		QuotaBytes:       quotaBytes,
		RawQuotaBytes:    rawQuotaBytes,
		MetaStore:        metaStore,
		StorageMgr:       storageMgr,
		Explorer:         exp,
		HTTPClient:       &http.Client{Timeout: 60 * time.Second},
	}

	srv := server.NewServer(cfg)
	httpServer := &http.Server{
		Addr:         ":" + port,
		Handler:      srv.Routes(),
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[gateway-api] ListenAndServe error: %v", err)
		}
	}()

	log.Printf("[gateway-api] HTTP server listening on :%s", port)
	<-stopChan
	log.Println("[gateway-api] Shutting down gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("[gateway-api] Shutdown error: %v", err)
	}
	log.Println("[gateway-api] Server stopped.")
}
