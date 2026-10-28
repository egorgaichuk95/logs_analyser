package reader_test

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/ingest-service/internal/reader"
)

func TestComputeFileSHA256(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.txt")
	content := []byte("The quick brown fox jumps over the lazy dog\n")
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	expectedHasher := sha256.New()
	expectedHasher.Write(content)
	expectedHash := hex.EncodeToString(expectedHasher.Sum(nil))

	hashHex, size, err := reader.ComputeFileSHA256(filePath)
	if err != nil {
		t.Fatalf("unexpected error from ComputeFileSHA256: %v", err)
	}

	if len(hashHex) != 64 {
		t.Fatalf("expected hash length 64, got %d (%s)", len(hashHex), hashHex)
	}
	if hashHex != expectedHash {
		t.Fatalf("hash mismatch: got %s, want %s", hashHex, expectedHash)
	}
	if size != int64(len(content)) {
		t.Fatalf("size mismatch: got %d, want %d", size, len(content))
	}

	// Test non-existent file
	_, _, err = reader.ComputeFileSHA256(filepath.Join(tmpDir, "missing.file"))
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

func TestStreamPath_JSONArray(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "chat-com_part-0024.json")

	records := []contracts.RawScriptRecord{
		{
			Timestamp: "1730114870000000000",
			Date:      "2026-10-28T11:27:50Z",
			Line:      `{"time":"260930 08:47:50 +0000","level":"INFO","msg":"starting app"}`,
			Fields: map[string]string{
				"app": "chat-com",
				"pod": "chat-com-abc",
			},
		},
		{
			Timestamp: "1730114871000000000",
			Date:      "2026-10-28T11:27:51Z",
			Line:      `{"time":"260930 08:47:51 +0000","level":"WARN","msg":"high latency"}`,
			Fields: map[string]string{
				"app": "chat-com",
				"pod": "chat-com-abc",
			},
		},
		{
			Timestamp: "1730114872000000000",
			Date:      "2026-10-28T11:27:52Z",
			Line:      `{"time":"260930 08:47:52 +0000","level":"ERROR","msg":"connection refused"}`,
			Fields: map[string]string{
				"app": "chat-com",
				"pod": "chat-com-abc",
			},
		},
	}

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal records: %v", err)
	}
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatalf("failed to write test json: %v", err)
	}

	var emitted []contracts.RawScriptRecord
	var sourceFiles []string
	var indices []uint32

	callback := func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		emitted = append(emitted, rec)
		sourceFiles = append(sourceFiles, sourceFile)
		indices = append(indices, recordIdx)
		return nil
	}

	ctx := context.Background()
	if err := reader.StreamPath(ctx, filePath, callback); err != nil {
		t.Fatalf("unexpected error from StreamPath: %v", err)
	}

	if len(emitted) != 3 {
		t.Fatalf("expected 3 records, got %d", len(emitted))
	}

	expectedBase := filepath.Base(filePath)
	for i := 0; i < 3; i++ {
		if sourceFiles[i] != expectedBase {
			t.Errorf("record %d: expected sourceFile %s, got %s", i, expectedBase, sourceFiles[i])
		}
		if indices[i] != uint32(i) {
			t.Errorf("record %d: expected idx %d, got %d", i, i, indices[i])
		}
		if emitted[i].Timestamp != records[i].Timestamp {
			t.Errorf("record %d: expected Timestamp %s, got %s", i, records[i].Timestamp, emitted[i].Timestamp)
		}
		if emitted[i].Line != records[i].Line {
			t.Errorf("record %d: expected Line %s, got %s", i, records[i].Line, emitted[i].Line)
		}
		if emitted[i].Fields["app"] != "chat-com" {
			t.Errorf("record %d: expected app chat-com, got %s", i, emitted[i].Fields["app"])
		}
	}
}

func TestStreamPath_JSONL_Formats(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "chat-session.jsonl")

	// 1 line standard RawScriptRecord, 1 line ts_ns/labels, 1 line with numeric fields
	lines := []string{
		`{"timestamp":"1730114880000000000","date":"2026-10-28T11:28:00Z","line":"GET /api/v1 200 OK","fields":{"service":"chat-session"}}`,
		`{"ts":"2026-10-28T11:28:01Z","ts_ns":1730114881000000000,"labels":{"container":"chat-session","pod":"pod-1"},"line":"POST /api/v1/auth 401"}`,
		`{"ts_ns":1730114882000000000,"labels":{"code":500},"line":"Internal Server Error"}`,
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write jsonl: %v", err)
	}

	var emitted []contracts.RawScriptRecord
	callback := func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		emitted = append(emitted, rec)
		return nil
	}

	if err := reader.StreamPath(context.Background(), filePath, callback); err != nil {
		t.Fatalf("StreamPath failed on JSONL: %v", err)
	}

	if len(emitted) != 3 {
		t.Fatalf("expected 3 records, got %d", len(emitted))
	}

	// Line 0
	if emitted[0].Timestamp != "1730114880000000000" || emitted[0].Fields["service"] != "chat-session" {
		t.Errorf("line 0 mismatch: %+v", emitted[0])
	}

	// Line 1 (ts_ns & labels conversion)
	if emitted[1].Timestamp != "1730114881000000000" {
		t.Errorf("line 1 ts_ns not converted to Timestamp string: got %s", emitted[1].Timestamp)
	}
	if emitted[1].Fields["container"] != "chat-session" || emitted[1].Fields["pod"] != "pod-1" {
		t.Errorf("line 1 labels not converted to Fields: %+v", emitted[1].Fields)
	}
	if emitted[1].Date != "2026-10-28T11:28:01Z" {
		t.Errorf("line 1 Date mismatch: got %s", emitted[1].Date)
	}

	// Line 2 (numeric label conversion)
	if emitted[2].Timestamp != "1730114882000000000" {
		t.Errorf("line 2 ts_ns mismatch: got %s", emitted[2].Timestamp)
	}
	if emitted[2].Fields["code"] != "500" {
		t.Errorf("line 2 numeric code label not converted: got %v", emitted[2].Fields["code"])
	}
}

func TestStreamPath_Gzip(t *testing.T) {
	tmpDir := t.TempDir()
	gzPath := filepath.Join(tmpDir, "logs.json.gz")

	records := []contracts.RawScriptRecord{
		{
			Timestamp: "100",
			Line:      "log entry 1",
			Fields:    map[string]string{"env": "prod"},
		},
		{
			Timestamp: "200",
			Line:      "log entry 2",
			Fields:    map[string]string{"env": "prod"},
		},
	}
	jsonData, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("json marshal error: %v", err)
	}

	gzFile, err := os.Create(gzPath)
	if err != nil {
		t.Fatalf("create gz error: %v", err)
	}
	gw := gzip.NewWriter(gzFile)
	if _, err := gw.Write(jsonData); err != nil {
		t.Fatalf("write gz error: %v", err)
	}
	_ = gw.Close()
	_ = gzFile.Close()

	var count int
	callback := func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		count++
		return nil
	}

	if err := reader.StreamPath(context.Background(), gzPath, callback); err != nil {
		t.Fatalf("StreamPath failed on .json.gz: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 records from gz, got %d", count)
	}
}

func TestStreamPath_Zip(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "all_logs.zip")

	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip error: %v", err)
	}
	zw := zip.NewWriter(zipFile)

	// File 1 in zip: array JSON
	w1, err := zw.Create("chat-com/part-01.json")
	if err != nil {
		t.Fatalf("create in zip: %v", err)
	}
	_, _ = w1.Write([]byte(`[{"timestamp":"1001","line":"chat-com line 1"},{"timestamp":"1002","line":"chat-com line 2"}]`))

	// File 2 in zip: jsonl
	w2, err := zw.Create("chat-session/session.jsonl")
	if err != nil {
		t.Fatalf("create in zip: %v", err)
	}
	_, _ = w2.Write([]byte("{\"ts_ns\":2001,\"line\":\"session line 1\"}\n{\"ts_ns\":2002,\"line\":\"session line 2\"}\n"))

	// Non-log file to ignore
	w3, err := zw.Create("readme.md")
	if err != nil {
		t.Fatalf("create in zip: %v", err)
	}
	_, _ = w3.Write([]byte("just readme"))

	_ = zw.Close()
	_ = zipFile.Close()

	var recordsByFile = make(map[string][]contracts.RawScriptRecord)
	callback := func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		recordsByFile[sourceFile] = append(recordsByFile[sourceFile], rec)
		return nil
	}

	if err := reader.StreamPath(context.Background(), zipPath, callback); err != nil {
		t.Fatalf("StreamPath on zip failed: %v", err)
	}

	if len(recordsByFile["part-01.json"]) != 2 {
		t.Errorf("expected 2 records for part-01.json, got %d", len(recordsByFile["part-01.json"]))
	}
	if len(recordsByFile["session.jsonl"]) != 2 {
		t.Errorf("expected 2 records for session.jsonl, got %d", len(recordsByFile["session.jsonl"]))
	}
	if len(recordsByFile["readme.md"]) != 0 {
		t.Errorf("expected 0 records for readme.md, got %d", len(recordsByFile["readme.md"]))
	}
}

func TestStreamPath_ZipWithGzInside(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "archive_with_gz.zip")

	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip error: %v", err)
	}
	zw := zip.NewWriter(zipFile)

	w, err := zw.Create("nested.json.gz")
	if err != nil {
		t.Fatalf("create inside zip: %v", err)
	}
	gw := gzip.NewWriter(w)
	_, _ = gw.Write([]byte(`[{"timestamp":"999","line":"inside gz in zip"}]`))
	_ = gw.Close()

	_ = zw.Close()
	_ = zipFile.Close()

	var count int
	callback := func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		if rec.Line != "inside gz in zip" {
			t.Errorf("unexpected line: %s", rec.Line)
		}
		count++
		return nil
	}

	if err := reader.StreamPath(context.Background(), zipPath, callback); err != nil {
		t.Fatalf("StreamPath failed on zip with gz: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 record from nested gz, got %d", count)
	}
}

func TestStreamPath_LargeLine200KB(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "huge_line.jsonl")

	// Generate a 200 KB line (exceeding standard bufio 64KB limit)
	hugePayload := strings.Repeat("ABCDEF1234567890", 13000) // ~208 KB
	lineJSON := fmt.Sprintf(`{"timestamp":"1730114900000000000","line":"%s","fields":{"size":"huge"}}`, hugePayload)

	if err := os.WriteFile(filePath, []byte(lineJSON+"\n"), 0644); err != nil {
		t.Fatalf("failed to write huge line file: %v", err)
	}

	var emittedLine string
	callback := func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		emittedLine = rec.Line
		return nil
	}

	if err := reader.StreamPath(context.Background(), filePath, callback); err != nil {
		t.Fatalf("StreamPath failed on 200KB line: %v", err)
	}

	if len(emittedLine) < 200000 {
		t.Fatalf("expected emitted line > 200KB, got length %d", len(emittedLine))
	}
	if emittedLine != hugePayload {
		t.Fatal("emitted huge line content mismatch")
	}
}

func TestStreamPath_EmptyAndWhitespaceFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Empty file (0 bytes)
	fEmpty := filepath.Join(tmpDir, "empty.json")
	if err := os.WriteFile(fEmpty, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	var count int
	cb := func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		count++
		return nil
	}

	if err := reader.StreamPath(context.Background(), fEmpty, cb); err != nil {
		t.Fatalf("StreamPath failed on empty file: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 records for empty file, got %d", count)
	}

	// 2. Whitespace only file
	fSpace := filepath.Join(tmpDir, "whitespace.json")
	if err := os.WriteFile(fSpace, []byte("  \t \n\r\n  "), 0644); err != nil {
		t.Fatal(err)
	}
	if err := reader.StreamPath(context.Background(), fSpace, cb); err != nil {
		t.Fatalf("StreamPath failed on whitespace file: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 records for whitespace file, got %d", count)
	}

	// 3. Empty JSON array
	fEmptyArr := filepath.Join(tmpDir, "empty_arr.json")
	if err := os.WriteFile(fEmptyArr, []byte(" [ \n\t ] "), 0644); err != nil {
		t.Fatal(err)
	}
	if err := reader.StreamPath(context.Background(), fEmptyArr, cb); err != nil {
		t.Fatalf("StreamPath failed on empty array: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 records for empty array, got %d", count)
	}
}

func TestStreamPath_ContextCanceled(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "records.json")

	// Array of records
	data := `[
		{"timestamp":"1","line":"entry 1"},
		{"timestamp":"2","line":"entry 2"},
		{"timestamp":"3","line":"entry 3"}
	]`
	if err := os.WriteFile(filePath, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before streaming

	err := reader.StreamPath(ctx, filePath, func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestStreamPath_EmitErrorHalts(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "records.json")

	data := `[
		{"timestamp":"1","line":"entry 1"},
		{"timestamp":"2","line":"entry 2"},
		{"timestamp":"3","line":"entry 3"}
	]`
	if err := os.WriteFile(filePath, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	expectedErr := errors.New("pipeline stopped intentionally")
	var count int
	err := reader.StreamPath(context.Background(), filePath, func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		count++
		if count == 2 {
			return expectedErr
		}
		return nil
	})

	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}
}

func TestStreamPath_MalformedJSON(t *testing.T) {
	tmpDir := t.TempDir()

	// Malformed array
	badArr := filepath.Join(tmpDir, "bad_array.json")
	if err := os.WriteFile(badArr, []byte(`[{"timestamp":"1", "unclosed]`), 0644); err != nil {
		t.Fatal(err)
	}

	cb := func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error { return nil }

	if err := reader.StreamPath(context.Background(), badArr, cb); err == nil {
		t.Fatal("expected error on bad JSON array, got nil")
	}

	// Malformed line
	badLine := filepath.Join(tmpDir, "bad_line.jsonl")
	if err := os.WriteFile(badLine, []byte("{\"timestamp\": \"1\", bad syntax\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := reader.StreamPath(context.Background(), badLine, cb); err == nil {
		t.Fatal("expected error on bad JSON line, got nil")
	}
}

func TestStreamPath_PlainTextLogs(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "app.log")

	content := "2026-10-28 12:00:00 starting service\n2026-10-28 12:00:01 error: connection failed\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	var emitted []contracts.RawScriptRecord
	err := reader.StreamPath(context.Background(), filePath, func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		emitted = append(emitted, rec)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamPath failed on plain log: %v", err)
	}

	if len(emitted) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(emitted))
	}
	if emitted[0].Line != "2026-10-28 12:00:00 starting service" {
		t.Errorf("line 0 mismatch: %s", emitted[0].Line)
	}
	if emitted[1].Line != "2026-10-28 12:00:01 error: connection failed" {
		t.Errorf("line 1 mismatch: %s", emitted[1].Line)
	}
}

func TestStreamPath_ErrorHandlingAndEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Missing zip file
	err := reader.StreamPath(context.Background(), filepath.Join(tmpDir, "missing.zip"), func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		return nil
	})
	if err == nil {
		t.Error("expected error for missing zip file")
	}

	// 2. Corrupt zip file
	corruptZip := filepath.Join(tmpDir, "corrupt.zip")
	_ = os.WriteFile(corruptZip, []byte("not a real zip file content"), 0644)
	err = reader.StreamPath(context.Background(), corruptZip, func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		return nil
	})
	if err == nil {
		t.Error("expected error for corrupt zip file")
	}

	// 3. Zip with directory, hidden file, MACOSX, and error during emit
	advZip := filepath.Join(tmpDir, "advanced.zip")
	zf, _ := os.Create(advZip)
	zw := zip.NewWriter(zf)

	// Directory entry
	_, _ = zw.Create("subfolder/")
	// Hidden file
	wDot, _ := zw.Create(".hidden.json")
	_, _ = wDot.Write([]byte(`[{"timestamp":"1","line":"hidden"}]`))
	// MACOSX file
	wMac, _ := zw.Create("__MACOSX/._log.json")
	_, _ = wMac.Write([]byte(`[{"timestamp":"1","line":"mac"}]`))
	// Valid file that will trigger emit error
	wEmit, _ := zw.Create("test.json")
	_, _ = wEmit.Write([]byte(`[{"timestamp":"1","line":"err"}]`))

	_ = zw.Close()
	_ = zf.Close()

	customErr := errors.New("emit fail in zip")
	err = reader.StreamPath(context.Background(), advZip, func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		return customErr
	})
	if !errors.Is(err, customErr) {
		t.Errorf("expected customErr %v, got %v", customErr, err)
	}

	// 4. Missing gz file
	err = reader.StreamPath(context.Background(), filepath.Join(tmpDir, "missing.gz"), func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		return nil
	})
	if err == nil {
		t.Error("expected error for missing gz file")
	}

	// 5. Corrupt gz file
	corruptGz := filepath.Join(tmpDir, "corrupt.gz")
	_ = os.WriteFile(corruptGz, []byte("invalid gzip content"), 0644)
	err = reader.StreamPath(context.Background(), corruptGz, func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		return nil
	})
	if err == nil {
		t.Error("expected error for corrupt gz file")
	}

	// 6. Empty gz file (0 bytes)
	emptyGz := filepath.Join(tmpDir, "empty.gz")
	_ = os.WriteFile(emptyGz, []byte(""), 0644)
	err = reader.StreamPath(context.Background(), emptyGz, func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		return nil
	})
	if err != nil {
		t.Errorf("unexpected error on empty gz: %v", err)
	}

	// 7. Missing normal file
	err = reader.StreamPath(context.Background(), filepath.Join(tmpDir, "missing.json"), func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		return nil
	})
	if err == nil {
		t.Error("expected error for missing regular file")
	}

	// 8. Zip with corrupt gz entry
	badGzZip := filepath.Join(tmpDir, "bad_gz.zip")
	bgzf, _ := os.Create(badGzZip)
	bgzw := zip.NewWriter(bgzf)
	bw, _ := bgzw.Create("broken.gz")
	_, _ = bw.Write([]byte("not gzip bytes"))
	_ = bgzw.Close()
	_ = bgzf.Close()

	err = reader.StreamPath(context.Background(), badGzZip, func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		return nil
	})
	if err == nil {
		t.Error("expected error for bad gz inside zip")
	}
}

func TestStreamPath_ContextCancelDuringStreaming(t *testing.T) {
	tmpDir := t.TempDir()

	// Cancel during array stream
	arrayFile := filepath.Join(tmpDir, "array.json")
	_ = os.WriteFile(arrayFile, []byte(`[{"timestamp":"1"},{"timestamp":"2"},{"timestamp":"3"}]`), 0644)

	ctxArr, cancelArr := context.WithCancel(context.Background())
	err := reader.StreamPath(ctxArr, arrayFile, func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		if recordIdx == 0 {
			cancelArr()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled during array stream, got %v", err)
	}

	// Cancel during line stream
	lineFile := filepath.Join(tmpDir, "lines.jsonl")
	_ = os.WriteFile(lineFile, []byte("{\"line\":\"1\"}\n{\"line\":\"2\"}\n{\"line\":\"3\"}\n"), 0644)

	ctxLine, cancelLine := context.WithCancel(context.Background())
	err = reader.StreamPath(ctxLine, lineFile, func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		if recordIdx == 0 {
			cancelLine()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled during line stream, got %v", err)
	}
}

func TestStreamPath_VariedFieldTypes(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "varied.jsonl")

	// Lines with float, int, bool, nil, string in labels and ts_ns as string and int
	lines := []string{
		`{"ts_ns":"1730000000","labels":{"is_active":true,"weight":75.5,"score":100,"tag":null,"name":"test"}}`,
		`{"ts":"2026-10-28T12:00:00Z","line":"ts only"}`,
	}
	_ = os.WriteFile(filePath, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	var emitted []contracts.RawScriptRecord
	err := reader.StreamPath(context.Background(), filePath, func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
		emitted = append(emitted, rec)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(emitted) != 2 {
		t.Fatalf("expected 2 emitted records, got %d", len(emitted))
	}
	if emitted[0].Fields["is_active"] != "true" {
		t.Errorf("expected is_active true, got %v", emitted[0].Fields["is_active"])
	}
	if emitted[0].Fields["weight"] != "75.5" {
		t.Errorf("expected weight 75.5, got %v", emitted[0].Fields["weight"])
	}
	if emitted[0].Fields["score"] != "100" {
		t.Errorf("expected score 100, got %v", emitted[0].Fields["score"])
	}
	if emitted[0].Fields["tag"] != "" {
		t.Errorf("expected tag empty, got %v", emitted[0].Fields["tag"])
	}
	if emitted[1].Date != "2026-10-28T12:00:00Z" {
		t.Errorf("expected Date from ts, got %s", emitted[1].Date)
	}
}
