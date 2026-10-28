package reader

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/local/logs-analyzer/pkg/contracts"
)

// RecordCallback вызывается для каждой прочитанной записи.
// sourceFile — базовое имя файла, recordIdx — порядковый индекс записи в файле (начиная с 0).
type RecordCallback func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error

type rawInputRecord struct {
	Timestamp string         `json:"timestamp"`
	Date      string         `json:"date"`
	Line      string         `json:"line"`
	Fields    map[string]any `json:"fields"`
	Ts        string         `json:"ts"`
	TsNs      any            `json:"ts_ns"`
	Labels    map[string]any `json:"labels"`
}

func (r rawInputRecord) toRawScriptRecord() contracts.RawScriptRecord {
	rec := contracts.RawScriptRecord{
		Timestamp: r.Timestamp,
		Date:      r.Date,
		Line:      r.Line,
	}

	if rec.Timestamp == "" {
		if r.TsNs != nil {
			switch v := r.TsNs.(type) {
			case json.Number:
				rec.Timestamp = v.String()
			case string:
				rec.Timestamp = v
			case int64:
				rec.Timestamp = strconv.FormatInt(v, 10)
			case float64:
				rec.Timestamp = strconv.FormatInt(int64(v), 10)
			default:
				rec.Timestamp = fmt.Sprint(v)
			}
		} else if r.Ts != "" {
			rec.Timestamp = r.Ts
		}
	}

	if rec.Date == "" && r.Ts != "" {
		rec.Date = r.Ts
	}

	if len(r.Fields) > 0 {
		rec.Fields = convertToStringMap(r.Fields)
	} else if len(r.Labels) > 0 {
		rec.Fields = convertToStringMap(r.Labels)
	}

	return rec
}

func convertToStringMap(m map[string]any) map[string]string {
	if m == nil {
		return nil
	}
	res := make(map[string]string, len(m))
	for k, v := range m {
		switch val := v.(type) {
		case string:
			res[k] = val
		case json.Number:
			res[k] = val.String()
		case int64:
			res[k] = strconv.FormatInt(val, 10)
		case float64:
			if val == float64(int64(val)) {
				res[k] = strconv.FormatInt(int64(val), 10)
			} else {
				res[k] = strconv.FormatFloat(val, 'f', -1, 64)
			}
		case bool:
			res[k] = strconv.FormatBool(val)
		case nil:
			res[k] = ""
		default:
			res[k] = fmt.Sprint(val)
		}
	}
	return res
}

func skipWhitespaceAndPeek(br *bufio.Reader) (byte, error) {
	for {
		b, err := br.ReadByte()
		if err != nil {
			return 0, err
		}
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			if err := br.UnreadByte(); err != nil {
				return 0, err
			}
			return b, nil
		}
	}
}

func isLogFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".json") ||
		strings.HasSuffix(lower, ".jsonl") ||
		strings.HasSuffix(lower, ".ndjson") ||
		strings.HasSuffix(lower, ".gz") ||
		strings.HasSuffix(lower, ".log") ||
		strings.HasSuffix(lower, ".txt")
}

// ComputeFileSHA256 потоково считает SHA-256 файла (64 hex символа) и его размер в байтах через io.Copy.
func ComputeFileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open file for sha256: %w", err)
	}
	defer f.Close()

	hasher := sha256.New()
	size, err := io.Copy(hasher, f)
	if err != nil {
		return "", 0, fmt.Errorf("hashing file %s: %w", path, err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

func streamFromReader(ctx context.Context, r io.Reader, sourceFile string, emit RecordCallback) error {
	br := bufio.NewReaderSize(r, 64*1024)
	firstByte, err := skipWhitespaceAndPeek(br)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("peek stream %s: %w", sourceFile, err)
	}

	if firstByte == '[' {
		dec := json.NewDecoder(br)
		dec.UseNumber()

		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("reading open bracket in %s: %w", sourceFile, err)
		}
		delim, ok := tok.(json.Delim)
		if !ok || delim != '[' {
			return fmt.Errorf("expected '[', got %v in %s", tok, sourceFile)
		}

		var recordIdx uint32
		for dec.More() {
			if err := ctx.Err(); err != nil {
				return err
			}
			var raw rawInputRecord
			if err := dec.Decode(&raw); err != nil {
				return fmt.Errorf("decode record %d in %s: %w", recordIdx, sourceFile, err)
			}
			rec := raw.toRawScriptRecord()
			if err := emit(rec, sourceFile, recordIdx); err != nil {
				return err
			}
			recordIdx++
		}

		tok, err = dec.Token()
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("reading close bracket in %s: %w", sourceFile, err)
		}
		return nil
	}

	// Line-by-line reading for jsonl, ndjson, or plain text
	scanner := bufio.NewScanner(br)
	buf := make([]byte, 64*1024)
	const maxLineSize = 4 * 1024 * 1024
	scanner.Buffer(buf, maxLineSize)

	var recordIdx uint32
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		if line[0] == '{' {
			var raw rawInputRecord
			dec := json.NewDecoder(bytes.NewReader(line))
			dec.UseNumber()
			if err := dec.Decode(&raw); err != nil {
				return fmt.Errorf("decode json line %d in %s: %w", recordIdx, sourceFile, err)
			}
			rec := raw.toRawScriptRecord()
			if rec.Line == "" {
				rec.Line = string(line)
			}
			if err := emit(rec, sourceFile, recordIdx); err != nil {
				return err
			}
			recordIdx++
		} else {
			rec := contracts.RawScriptRecord{
				Line: string(line),
			}
			if err := emit(rec, sourceFile, recordIdx); err != nil {
				return err
			}
			recordIdx++
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error in %s: %w", sourceFile, err)
	}

	return nil
}

// StreamPath потоково читает файл по пути path (.json, .jsonl, .gz, .zip) и вызывает emit для каждой записи.
func StreamPath(ctx context.Context, path string, emit RecordCallback) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	lowerPath := strings.ToLower(path)

	if strings.HasSuffix(lowerPath, ".zip") {
		zr, err := zip.OpenReader(path)
		if err != nil {
			return fmt.Errorf("open zip %s: %w", path, err)
		}
		defer zr.Close()

		for _, zf := range zr.File {
			if err := ctx.Err(); err != nil {
				return err
			}
			if zf.FileInfo().IsDir() {
				continue
			}
			base := filepath.Base(filepath.FromSlash(zf.Name))
			if strings.HasPrefix(base, ".") || strings.HasPrefix(zf.Name, "__MACOSX/") {
				continue
			}
			lowerName := strings.ToLower(zf.Name)
			if !isLogFile(lowerName) {
				continue
			}

			rc, err := zf.Open()
			if err != nil {
				return fmt.Errorf("open zip entry %s: %w", zf.Name, err)
			}

			var r io.Reader = rc
			var gr *gzip.Reader
			if strings.HasSuffix(lowerName, ".gz") {
				gr, err = gzip.NewReader(rc)
				if err != nil {
					_ = rc.Close()
					if errors.Is(err, io.EOF) {
						continue
					}
					return fmt.Errorf("open gzip in zip entry %s: %w", zf.Name, err)
				}
				r = gr
			}

			streamErr := streamFromReader(ctx, r, base, emit)
			if gr != nil {
				_ = gr.Close()
			}
			_ = rc.Close()
			if streamErr != nil {
				return fmt.Errorf("streaming zip entry %s: %w", zf.Name, streamErr)
			}
		}
		return nil
	}

	if strings.HasSuffix(lowerPath, ".gz") {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open gz file %s: %w", path, err)
		}
		defer f.Close()

		gr, err := gzip.NewReader(f)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read gzip header %s: %w", path, err)
		}
		defer gr.Close()

		return streamFromReader(ctx, gr, filepath.Base(path), emit)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open file %s: %w", path, err)
	}
	defer f.Close()

	return streamFromReader(ctx, f, filepath.Base(path), emit)
}
