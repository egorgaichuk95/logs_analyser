package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/tilt-collector/internal/discovery"
)

type Manager struct {
	mu            sync.RWMutex
	runtime       *discovery.RuntimeClient
	ingestURL     string
	httpClient    *http.Client
	batchSize     int
	flushInterval time.Duration

	active         bool
	caseID         string
	startedAt      *time.Time
	targets        []contracts.TiltTarget
	targetsWatched int
	linesCaptured  uint64
	batchesFlushed uint64
	lastError      string

	cancelFunc  context.CancelFunc
	recordChan  chan contracts.RawScriptRecord
	flusherDone chan struct{}
	readersWg   sync.WaitGroup
}

func NewManager(runtime *discovery.RuntimeClient, ingestURL string) *Manager {
	return &Manager{
		runtime:       runtime,
		ingestURL:     ingestURL,
		httpClient:    &http.Client{Timeout: 15 * time.Second},
		batchSize:     500,
		flushInterval: 1500 * time.Millisecond,
	}
}

// StartCapture останавливает предыдущую сессию (если была активна), находит целевые контейнеры/поды Tilt
// и запускает фоновый стриминг логов в ingest-service (/internal/ingest/batch) для указанного req.CaseID.
func (m *Manager) StartCapture(ctx context.Context, req contracts.TiltStartCaptureRequest) (contracts.TiltStatusResponse, error) {
	if err := req.Validate(); err != nil {
		return contracts.TiltStatusResponse{}, err
	}

	// Stop previous active capture session if any
	m.StopCapture()

	discovered, err := m.runtime.DiscoverTargets(ctx)
	if err != nil {
		return contracts.TiltStatusResponse{}, fmt.Errorf("discover targets: %w", err)
	}

	selected := make([]contracts.TiltTarget, 0, len(discovered))
	if len(req.TargetIDs) > 0 {
		filterMap := make(map[string]bool, len(req.TargetIDs))
		for _, id := range req.TargetIDs {
			filterMap[id] = true
		}
		for _, tgt := range discovered {
			if filterMap[tgt.ID] {
				selected = append(selected, tgt)
			}
		}
	} else {
		selected = append(selected, discovered...)
	}

	m.mu.Lock()
	now := time.Now().UTC()
	m.active = true
	m.caseID = req.CaseID
	m.startedAt = &now
	m.targets = selected
	m.targetsWatched = len(selected)
	atomic.StoreUint64(&m.linesCaptured, 0)
	atomic.StoreUint64(&m.batchesFlushed, 0)
	m.lastError = ""

	captureCtx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	recordChan := make(chan contracts.RawScriptRecord, 5000)
	m.recordChan = recordChan
	flusherDone := make(chan struct{})
	m.flusherDone = flusherDone
	m.mu.Unlock()

	for _, tgt := range selected {
		m.readersWg.Add(1)
		go func(target contracts.TiltTarget) {
			defer m.readersWg.Done()
			_ = m.runtime.StreamContainerLogs(captureCtx, target, req.SinceSeconds, func(rec contracts.RawScriptRecord) {
				select {
				case <-captureCtx.Done():
					return
				case recordChan <- rec:
				}
			})
		}(tgt)
	}

	go func(caseID string, ch chan contracts.RawScriptRecord, done chan struct{}) {
		defer close(done)
		ticker := time.NewTicker(m.flushInterval)
		defer ticker.Stop()

		batch := make([]contracts.RawScriptRecord, 0, m.batchSize)

		flush := func() {
			if len(batch) == 0 {
				return
			}
			toSend := make([]contracts.RawScriptRecord, len(batch))
			copy(toSend, batch)
			batch = batch[:0]
			if sendErr := m.sendBatch(caseID, toSend); sendErr != nil {
				m.setLastError(sendErr.Error())
			}
		}

		for {
			select {
			case rec, ok := <-ch:
				if !ok {
					flush()
					return
				}
				batch = append(batch, rec)
				atomic.AddUint64(&m.linesCaptured, 1)
				if len(batch) >= m.batchSize {
					flush()
				}
			case <-ticker.C:
				if len(batch) > 0 {
					flush()
				}
			}
		}
	}(req.CaseID, recordChan, flusherDone)

	return m.GetStatus(), nil
}

// StopCapture останавливает все горутины чтения логов, досылает остаток буфера в ingest-service и переводит Active в false.
func (m *Manager) StopCapture() contracts.TiltStatusResponse {
	m.mu.Lock()
	if !m.active {
		m.mu.Unlock()
		return m.GetStatus()
	}

	cancel := m.cancelFunc
	recordChan := m.recordChan
	flusherDone := m.flusherDone
	m.active = false
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	m.readersWg.Wait()

	if recordChan != nil {
		close(recordChan)
	}

	if flusherDone != nil {
		<-flusherDone
	}

	return m.GetStatus()
}

// GetStatus возвращает текущее состояние захвата (Active, TargetsWatched, LinesCaptured, BatchesFlushed, Targets).
func (m *Manager) GetStatus() contracts.TiltStatusResponse {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var startedCopy *time.Time
	if m.startedAt != nil {
		t := *m.startedAt
		startedCopy = &t
	}

	targetsCopy := make([]contracts.TiltTarget, len(m.targets))
	copy(targetsCopy, m.targets)

	return contracts.TiltStatusResponse{
		CaseID:         m.caseID,
		Active:         m.active,
		StartedAt:      startedCopy,
		TargetsWatched: m.targetsWatched,
		LinesCaptured:  atomic.LoadUint64(&m.linesCaptured),
		BatchesFlushed: atomic.LoadUint64(&m.batchesFlushed),
		LastError:      m.lastError,
		Targets:        targetsCopy,
	}
}

func (m *Manager) sendBatch(caseID string, records []contracts.RawScriptRecord) error {
	if len(records) == 0 {
		return nil
	}

	reqBody := contracts.IngestBatchRequest{
		CaseID:     caseID,
		SourceName: "tilt-collector",
		Records:    records,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal ingest batch: %w", err)
	}

	url := strings.TrimRight(m.ingestURL, "/") + "/internal/ingest/batch"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create ingest batch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send ingest batch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("ingest batch returned status %d", resp.StatusCode)
	}

	atomic.AddUint64(&m.batchesFlushed, 1)
	return nil
}

func (m *Manager) setLastError(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = msg
}
