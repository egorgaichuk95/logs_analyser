package storage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/gateway-api/internal/metadb"
)

type Manager struct {
	chHTTPURL     string
	stagingDir    string
	reportsDir    string
	quotaBytes    int64 // 15 GB = 16106127360
	rawQuotaBytes int64 // 10 GB = 10737418240
	meta          *metadb.Store
	httpClient    *http.Client
}

func NewManager(chHTTPURL, stagingDir, reportsDir string, quotaBytes, rawQuotaBytes int64, meta *metadb.Store) *Manager {
	if quotaBytes <= 0 {
		quotaBytes = 16106127360
	}
	if rawQuotaBytes <= 0 {
		rawQuotaBytes = 10737418240
	}
	return &Manager{
		chHTTPURL:     strings.TrimRight(chHTTPURL, "/"),
		stagingDir:    stagingDir,
		reportsDir:    reportsDir,
		quotaBytes:    quotaBytes,
		rawQuotaBytes: rawQuotaBytes,
		meta:          meta,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func calculateDirSize(dir string) int64 {
	if dir == "" {
		return 0
	}
	var size int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

// GetStatus запрашивает размер таблиц ClickHouse и папок staging/reports
func (m *Manager) GetStatus(ctx context.Context) (contracts.StorageStatusResponse, error) {
	if m.chHTTPURL == "" {
		return contracts.StorageStatusResponse{}, errors.New("clickhouse url cannot be empty")
	}

	query := "SELECT table, sum(bytes_on_disk) AS bytes FROM system.parts WHERE database = 'logs_rca' AND active = 1 GROUP BY table FORMAT JSONEachRow"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.chHTTPURL, strings.NewReader(query+"\n"))
	if err != nil {
		return contracts.StorageStatusResponse{}, fmt.Errorf("create clickhouse request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return contracts.StorageStatusResponse{}, fmt.Errorf("query clickhouse parts: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return contracts.StorageStatusResponse{}, fmt.Errorf("clickhouse status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rawBytes int64
	var aggBytes int64

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var row struct {
			Table string      `json:"table"`
			Bytes json.Number `json:"bytes"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			continue
		}
		b, err := row.Bytes.Int64()
		if err != nil {
			u, uerr := strconv.ParseUint(row.Bytes.String(), 10, 64)
			if uerr == nil {
				b = int64(u)
			}
		}
		switch row.Table {
		case "events_raw", "identity_edges", "ingested_files":
			rawBytes += b
		case "events_agg_1m", "templates_dict", "service_edges":
			aggBytes += b
		}
	}
	if err := scanner.Err(); err != nil {
		return contracts.StorageStatusResponse{}, fmt.Errorf("scan clickhouse response: %w", err)
	}

	stagingBytes := calculateDirSize(m.stagingDir)
	reportsBytes := calculateDirSize(m.reportsDir)
	totalBytes := rawBytes + stagingBytes + aggBytes + reportsBytes

	return contracts.StorageStatusResponse{
		RawBytes:      rawBytes,
		StagingBytes:  stagingBytes,
		AggBytes:      aggBytes,
		ReportsBytes:  reportsBytes,
		TotalBytes:    totalBytes,
		QuotaBytes:    m.quotaBytes,
		RawQuotaBytes: m.rawQuotaBytes,
	}, nil
}

// PurgeAllRawLogs — очистка сырых логов в 1 клик!
// Выполняет TRUNCATE TABLE logs_rca.events_raw, identity_edges, ingested_files и удаляет файлы в stagingDir.
// Таблицы events_agg_1m, templates_dict, service_edges и папка reportsDir НЕ затрагиваются!
func (m *Manager) PurgeAllRawLogs(ctx context.Context) (contracts.PurgeRawResponse, error) {
	start := time.Now()

	statusBefore, err := m.GetStatus(ctx)
	if err != nil {
		return contracts.PurgeRawResponse{}, fmt.Errorf("get status before purge: %w", err)
	}

	rawTables := []string{
		"logs_rca.events_raw",
		"logs_rca.identity_edges",
		"logs_rca.ingested_files",
	}

	for _, tbl := range rawTables {
		query := fmt.Sprintf("TRUNCATE TABLE %s", tbl)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.chHTTPURL, strings.NewReader(query+"\n"))
		if err != nil {
			return contracts.PurgeRawResponse{}, fmt.Errorf("create truncate request for %s: %w", tbl, err)
		}
		resp, err := m.httpClient.Do(req)
		if err != nil {
			return contracts.PurgeRawResponse{}, fmt.Errorf("execute truncate for %s: %w", tbl, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return contracts.PurgeRawResponse{}, fmt.Errorf("clickhouse truncate %s failed (code %d): %s", tbl, resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}

	if m.stagingDir != "" {
		entries, err := os.ReadDir(m.stagingDir)
		if err == nil {
			for _, entry := range entries {
				_ = os.RemoveAll(filepath.Join(m.stagingDir, entry.Name()))
			}
		}
	}

	if m.meta != nil {
		if err := m.meta.MarkAllRawPurged(); err != nil {
			return contracts.PurgeRawResponse{}, fmt.Errorf("mark raw purged: %w", err)
		}
	}

	freedBytes := statusBefore.RawBytes + statusBefore.StagingBytes
	durationMs := time.Since(start).Milliseconds()

	return contracts.PurgeRawResponse{
		FreedBytes: freedBytes,
		DurationMs: durationMs,
		Status:     "OK",
	}, nil
}
