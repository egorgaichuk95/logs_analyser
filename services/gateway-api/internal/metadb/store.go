package metadb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

type fileState struct {
	Cases        map[string]contracts.CaseMeta              `json:"cases"`
	AnalysisRuns map[string]contracts.AnalyzeCaseResponse   `json:"analysis_runs"`
	Reports      map[string]contracts.BuildEvidenceResponse `json:"reports"`
}

type Store struct {
	mu           sync.RWMutex
	path         string
	cases        map[string]contracts.CaseMeta
	analysisRuns map[string]contracts.AnalyzeCaseResponse
	reports      map[string]contracts.BuildEvidenceResponse
}

func NewStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store path cannot be empty")
	}

	s := &Store{
		path:         path,
		cases:        make(map[string]contracts.CaseMeta),
		analysisRuns: make(map[string]contracts.AnalyzeCaseResponse),
		reports:      make(map[string]contracts.BuildEvidenceResponse),
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if err := s.saveLocked(); err != nil {
				return nil, err
			}
			return s, nil
		}
		return nil, fmt.Errorf("read store file: %w", err)
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return s, nil
	}

	var state fileState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal store state: %w", err)
	}

	if state.Cases != nil {
		s.cases = state.Cases
	}
	if state.AnalysisRuns != nil {
		s.analysisRuns = state.AnalysisRuns
	}
	if state.Reports != nil {
		s.reports = state.Reports
	}

	return s, nil
}

func (s *Store) saveLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}

	state := fileState{
		Cases:        s.cases,
		AnalysisRuns: s.analysisRuns,
		Reports:      s.reports,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal store state: %w", err)
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write tmp store file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(s.path)
		if err2 := os.Rename(tmpPath, s.path); err2 != nil {
			return fmt.Errorf("rename tmp store file: %w", err)
		}
	}

	return nil
}

func (s *Store) SaveCase(c contracts.CaseMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if c.ID == "" {
		return contracts.ErrEmptyCaseID
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	if c.Status == "" {
		c.Status = contracts.CaseStatusCreated
	}

	s.cases[c.ID] = c
	return s.saveLocked()
}

func (s *Store) GetCase(id string) (contracts.CaseMeta, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if id == "" {
		return contracts.CaseMeta{}, false
	}

	c, ok := s.cases[id]
	return c, ok
}

func (s *Store) ListCases() []contracts.CaseMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]contracts.CaseMeta, 0, len(s.cases))
	for _, c := range s.cases {
		res = append(res, c)
	}

	sort.Slice(res, func(i, j int) bool {
		if res[i].CreatedAt.Equal(res[j].CreatedAt) {
			return res[i].ID < res[j].ID
		}
		return res[i].CreatedAt.After(res[j].CreatedAt)
	})

	return res
}

func (s *Store) DeleteCase(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" {
		return contracts.ErrEmptyCaseID
	}

	if _, ok := s.cases[id]; !ok {
		return fmt.Errorf("case %q not found", id)
	}

	delete(s.cases, id)

	for k, run := range s.analysisRuns {
		if run.CaseID == id {
			delete(s.analysisRuns, k)
		}
	}

	for k, rep := range s.reports {
		if rep.CaseID == id {
			delete(s.reports, k)
		}
	}

	return s.saveLocked()
}

func (s *Store) MarkAllRawPurged() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, c := range s.cases {
		c.Status = contracts.CaseStatusRawPurged
		s.cases[k] = c
	}

	return s.saveLocked()
}

func (s *Store) SaveAnalysisRun(res contracts.AnalyzeCaseResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if res.CaseID == "" {
		return contracts.ErrEmptyCaseID
	}
	if res.RunID == "" {
		return errors.New("run_id cannot be empty")
	}

	key := res.CaseID + ":" + res.RunID
	s.analysisRuns[key] = res

	if c, ok := s.cases[res.CaseID]; ok {
		c.LatestRunID = res.RunID
		if c.Status != contracts.CaseStatusRawPurged {
			c.Status = contracts.CaseStatusAnalyzed
		}
		s.cases[res.CaseID] = c
	}

	return s.saveLocked()
}

func (s *Store) GetAnalysisRun(caseID, runID string) (contracts.AnalyzeCaseResponse, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if caseID == "" {
		return contracts.AnalyzeCaseResponse{}, false
	}

	targetRunID := runID
	if targetRunID == "" || targetRunID == "latest" {
		if c, ok := s.cases[caseID]; ok && c.LatestRunID != "" {
			targetRunID = c.LatestRunID
		}
	}

	if targetRunID == "" {
		return contracts.AnalyzeCaseResponse{}, false
	}

	key := caseID + ":" + targetRunID
	run, ok := s.analysisRuns[key]
	return run, ok
}

func (s *Store) FindRecurringCases(currentCaseID string, templateID uint64, sourcePlace string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if templateID == 0 && sourcePlace == "" {
		return []string{}
	}

	seen := make(map[string]bool)
	for _, run := range s.analysisRuns {
		if run.CaseID == currentCaseID {
			continue
		}
		for _, hyp := range run.Hypotheses {
			matched := false
			if templateID != 0 && hyp.TemplateID == templateID {
				matched = true
			}
			if sourcePlace != "" && hyp.SourcePlace == sourcePlace {
				matched = true
			}
			if matched {
				seen[run.CaseID] = true
				break
			}
		}
	}

	result := make([]string, 0, len(seen))
	for caseID := range seen {
		result = append(result, caseID)
	}
	sort.Strings(result)
	return result
}

func (s *Store) SaveReport(rep contracts.BuildEvidenceResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rep.CaseID == "" {
		return contracts.ErrEmptyCaseID
	}
	if rep.RunID == "" {
		return errors.New("run_id cannot be empty")
	}

	key := rep.CaseID + ":" + rep.RunID
	s.reports[key] = rep

	return s.saveLocked()
}

func (s *Store) GetReport(caseID, runID string) (contracts.BuildEvidenceResponse, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if caseID == "" {
		return contracts.BuildEvidenceResponse{}, false
	}

	targetRunID := runID
	if targetRunID == "" || targetRunID == "latest" {
		if c, ok := s.cases[caseID]; ok && c.LatestRunID != "" {
			targetRunID = c.LatestRunID
		}
	}

	if targetRunID == "" {
		return contracts.BuildEvidenceResponse{}, false
	}

	key := caseID + ":" + targetRunID
	rep, ok := s.reports[key]
	return rep, ok
}
