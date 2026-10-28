package discovery

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

var podHashRegex = regexp.MustCompile(`(-[a-f0-9]{4,10}(-[a-z0-9]{4,6})?|-[0-9a-z]{8,10}-[0-9a-z]{4,6})$`)

type RuntimeClient struct {
	dockerBaseURL string
	k8sAPIURL     string
	httpClient    *http.Client
}

// NewRuntimeClient создаёт клиент к локальному Docker Daemon (через unix-сокет или http URL для тестов) и опциональному K8s API.
func NewRuntimeClient(dockerSocketOrURL string, k8sAPIURL string) *RuntimeClient {
	var baseURL string
	var client *http.Client

	if strings.HasPrefix(dockerSocketOrURL, "http://") || strings.HasPrefix(dockerSocketOrURL, "https://") {
		baseURL = strings.TrimRight(dockerSocketOrURL, "/")
		client = &http.Client{
			Timeout: 30 * time.Second,
		}
	} else {
		socketPath := dockerSocketOrURL
		if socketPath == "" {
			socketPath = "/var/run/docker.sock"
		}
		socketPath = strings.TrimPrefix(socketPath, "unix://")
		baseURL = "http://unix"
		client = &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					var d net.Dialer
					if strings.HasPrefix(addr, "unix:") {
						return d.DialContext(ctx, "unix", socketPath)
					}
					return d.DialContext(ctx, network, addr)
				},
			},
			Timeout: 30 * time.Second,
		}
	}

	return &RuntimeClient{
		dockerBaseURL: baseURL,
		k8sAPIURL:     strings.TrimRight(k8sAPIURL, "/"),
		httpClient:    client,
	}
}

// NormalizeTarget преобразует метаданные контейнера/пода (имя, labels) в contracts.TiltTarget.
// Возвращает (target, true), если это прикладной сервис, или (_, false), если это системный контейнер.
func NormalizeTarget(id string, rawName string, status string, labels map[string]string) (contracts.TiltTarget, bool) {
	cleanName := strings.TrimPrefix(rawName, "/")

	// Filter system containers:
	// 1. Containers starting with "rca-"
	if strings.HasPrefix(cleanName, "rca-") {
		return contracts.TiltTarget{}, false
	}

	// 2. K8s system namespaces and podsandbox
	if labels != nil {
		ns := labels["io.kubernetes.pod.namespace"]
		if ns == "kube-system" || ns == "local-path-storage" || ns == "ingress-nginx" || ns == "metallb-system" {
			return contracts.TiltTarget{}, false
		}
		if labels["io.kubernetes.docker.type"] == "podsandbox" {
			return contracts.TiltTarget{}, false
		}
	}

	// If container name is empty and no relevant labels are present, filter out
	if cleanName == "" && len(labels) == 0 {
		return contracts.TiltTarget{}, false
	}

	// Determine ServiceName:
	// Priority:
	// 1. dev.tilt.resource or tilt.dev/resource
	// 2. app.kubernetes.io/name or app or service_name
	// 3. io.kubernetes.container.name
	// 4. com.docker.compose.service
	// 5. fallback: cleanName with pod hash stripped
	var serviceName string
	if labels != nil {
		if v := labels["dev.tilt.resource"]; v != "" {
			serviceName = v
		} else if v := labels["tilt.dev/resource"]; v != "" {
			serviceName = v
		} else if v := labels["app.kubernetes.io/name"]; v != "" {
			serviceName = v
		} else if v := labels["app"]; v != "" {
			serviceName = v
		} else if v := labels["service_name"]; v != "" {
			serviceName = v
		} else if v := labels["io.kubernetes.container.name"]; v != "" {
			serviceName = v
		} else if v := labels["com.docker.compose.service"]; v != "" {
			serviceName = v
		}
	}

	if serviceName == "" && cleanName != "" {
		fallback := cleanName
		if strings.HasPrefix(fallback, "k8s_") {
			parts := strings.Split(fallback, "_")
			if len(parts) > 1 && parts[1] != "" {
				fallback = parts[1]
			}
		}
		stripped := podHashRegex.ReplaceAllString(fallback, "")
		if stripped != "" {
			serviceName = stripped
		} else {
			serviceName = fallback
		}
	}

	if serviceName == "" {
		return contracts.TiltTarget{}, false
	}

	// Determine PodName:
	// Priority:
	// 1. labels["io.kubernetes.pod.name"]
	// 2. cleanName
	// 3. ServiceName
	var podName string
	if labels != nil {
		podName = labels["io.kubernetes.pod.name"]
	}
	if podName == "" {
		podName = cleanName
	}
	if podName == "" {
		podName = serviceName
	}

	// Determine Namespace:
	// Priority:
	// 1. labels["io.kubernetes.pod.namespace"]
	// 2. labels["com.docker.compose.project"]
	// 3. "tilt-local"
	var namespace string
	if labels != nil {
		if v := labels["io.kubernetes.pod.namespace"]; v != "" {
			namespace = v
		} else if v := labels["com.docker.compose.project"]; v != "" {
			namespace = v
		}
	}
	if namespace == "" {
		namespace = "tilt-local"
	}

	// Determine Runtime
	runtime := contracts.RuntimeDocker
	if labels != nil {
		if labels["io.kubernetes.pod.name"] != "" || labels["io.kubernetes.container.name"] != "" || labels["io.kubernetes.pod.namespace"] != "" {
			runtime = contracts.RuntimeK8s
		}
	}

	targetLabels := make(map[string]string)
	if labels != nil {
		for k, v := range labels {
			targetLabels[k] = v
		}
	}

	return contracts.TiltTarget{
		ID:          id,
		Runtime:     runtime,
		ServiceName: serviceName,
		PodName:     podName,
		Namespace:   namespace,
		Status:      status,
		Labels:      targetLabels,
	}, true
}

// ParseLogLine разбирает одну строку из потока Docker/Kubernetes logs (с RFC3339Nano timestamp) в contracts.RawScriptRecord.
func ParseLogLine(rawLine string, target contracts.TiltTarget) (contracts.RawScriptRecord, bool) {
	// Strip 8-byte Docker stream binary header if present
	if len(rawLine) >= 8 && rawLine[0] <= 2 && rawLine[1] == 0 && rawLine[2] == 0 && rawLine[3] == 0 {
		rawLine = rawLine[8:]
	}

	line := strings.TrimRight(rawLine, "\r\n")
	if strings.TrimSpace(line) == "" {
		return contracts.RawScriptRecord{}, false
	}

	var ts time.Time
	var hasTimestamp bool
	var remainder string

	idx := strings.IndexByte(line, ' ')
	if idx > 0 {
		token := line[:idx]
		if parsed, err := time.Parse(time.RFC3339Nano, token); err == nil {
			ts = parsed.UTC()
			hasTimestamp = true
			remainder = line[idx+1:]
		} else if parsed, err := time.Parse(time.RFC3339, token); err == nil {
			ts = parsed.UTC()
			hasTimestamp = true
			remainder = line[idx+1:]
		}
	}

	if !hasTimestamp {
		ts = time.Now().UTC()
		remainder = line
	}

	// Handle CRI format: stdout/stderr followed by F/P flag
	payload := remainder
	if strings.HasPrefix(payload, "stdout ") || strings.HasPrefix(payload, "stderr ") {
		parts := strings.SplitN(payload, " ", 3)
		if len(parts) >= 3 && (parts[1] == "F" || parts[1] == "P") {
			payload = parts[2]
		} else if len(parts) >= 2 {
			payload = parts[1]
		}
	}

	if strings.TrimSpace(payload) == "" {
		return contracts.RawScriptRecord{}, false
	}

	return contracts.RawScriptRecord{
		Timestamp: strconv.FormatInt(ts.UnixNano(), 10),
		Date:      ts.Format(time.RFC3339Nano),
		Line:      payload,
		Fields: map[string]string{
			"service_name": target.ServiceName,
			"pod":          target.PodName,
			"namespace":    target.Namespace,
			"source":       "tilt",
		},
	}, true
}

// StreamReader читает поток логов контейнера (поддерживая как plain-поток, так и 8-байтовые заголовки Docker attach/logs stream)
// и вызывает onRecord для каждой непустой строки.
func StreamReader(ctx context.Context, r io.Reader, target contracts.TiltTarget, onRecord func(contracts.RawScriptRecord)) error {
	br := bufio.NewReaderSize(r, 64*1024)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		peek, err := br.Peek(8)
		if err != nil {
			if errors.Is(err, io.EOF) || len(peek) > 0 {
				for {
					line, rErr := br.ReadString('\n')
					if rec, ok := ParseLogLine(line, target); ok && onRecord != nil {
						onRecord(rec)
					}
					if rErr != nil {
						if errors.Is(rErr, io.EOF) {
							return nil
						}
						return rErr
					}
				}
			}
			return err
		}

		// Check if peek begins with an 8-byte Docker multiplex header
		isDockerHeader := (peek[0] <= 2) && (peek[1] == 0 && peek[2] == 0 && peek[3] == 0)

		if isDockerHeader {
			var header [8]byte
			if _, err := io.ReadFull(br, header[:]); err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					return nil
				}
				return err
			}

			frameSize := binary.BigEndian.Uint32(header[4:8])
			if frameSize == 0 {
				continue
			}

			frameReader := io.LimitReader(br, int64(frameSize))
			scanner := bufio.NewScanner(frameReader)
			scanBuf := make([]byte, 64*1024)
			scanner.Buffer(scanBuf, 4*1024*1024)

			for scanner.Scan() {
				if err := ctx.Err(); err != nil {
					return err
				}
				line := scanner.Text()
				if rec, ok := ParseLogLine(line, target); ok && onRecord != nil {
					onRecord(rec)
				}
			}

			if err := scanner.Err(); err != nil {
				return err
			}
			// Drain any remaining bytes from this frame if any
			_, _ = io.Copy(io.Discard, frameReader)
		} else {
			// Plain text stream line
			line, err := br.ReadString('\n')
			if rec, ok := ParseLogLine(line, target); ok && onRecord != nil {
				onRecord(rec)
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
		}
	}
}

type dockerContainerItem struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
}

// DiscoverTargets опрашивает локальный runtime и возвращает список всех активных сервисов приложения.
func (c *RuntimeClient) DiscoverTargets(ctx context.Context) ([]contracts.TiltTarget, error) {
	if c == nil || c.httpClient == nil {
		return nil, errors.New("runtime client is not initialized")
	}

	url := strings.TrimRight(c.dockerBaseURL, "/") + "/containers/json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create docker containers request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker containers request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker API returned status %d", resp.StatusCode)
	}

	var items []dockerContainerItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode docker containers response: %w", err)
	}

	targets := make([]contracts.TiltTarget, 0, len(items))
	for _, item := range items {
		rawName := ""
		if len(item.Names) > 0 {
			rawName = item.Names[0]
		}
		status := item.State
		if status == "" {
			status = item.Status
		}
		if target, ok := NormalizeTarget(item.ID, rawName, status, item.Labels); ok {
			targets = append(targets, target)
		}
	}

	if c.k8sAPIURL != "" {
		k8sTargets, err := c.discoverK8sPods(ctx)
		if err == nil && len(k8sTargets) > 0 {
			targets = append(targets, k8sTargets...)
		}
	}

	return targets, nil
}

type k8sPodItem struct {
	Metadata struct {
		Name      string            `json:"name"`
		Namespace string            `json:"namespace"`
		UID       string            `json:"uid"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

type k8sPodList struct {
	Items []k8sPodItem `json:"items"`
}

func (c *RuntimeClient) discoverK8sPods(ctx context.Context) ([]contracts.TiltTarget, error) {
	url := strings.TrimRight(c.k8sAPIURL, "/") + "/api/v1/pods"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create k8s pods request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("k8s pods request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("k8s API returned status %d", resp.StatusCode)
	}

	var podList k8sPodList
	if err := json.NewDecoder(resp.Body).Decode(&podList); err != nil {
		return nil, fmt.Errorf("decode k8s pods response: %w", err)
	}

	var targets []contracts.TiltTarget
	for _, pod := range podList.Items {
		labels := pod.Metadata.Labels
		if labels == nil {
			labels = make(map[string]string)
		}
		if labels["io.kubernetes.pod.name"] == "" {
			labels["io.kubernetes.pod.name"] = pod.Metadata.Name
		}
		if labels["io.kubernetes.pod.namespace"] == "" {
			labels["io.kubernetes.pod.namespace"] = pod.Metadata.Namespace
		}
		if target, ok := NormalizeTarget(pod.Metadata.UID, pod.Metadata.Name, pod.Status.Phase, labels); ok {
			targets = append(targets, target)
		}
	}

	return targets, nil
}

// OpenContainerLogs открывает поток логов контейнера (Docker или K8s).
func (c *RuntimeClient) OpenContainerLogs(ctx context.Context, target contracts.TiltTarget, sinceSeconds int) (io.ReadCloser, error) {
	if c == nil || c.httpClient == nil {
		return nil, errors.New("runtime client is not initialized")
	}

	if target.Runtime == contracts.RuntimeK8s && c.k8sAPIURL != "" {
		url := fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/log?follow=true&timestamps=true",
			strings.TrimRight(c.k8sAPIURL, "/"),
			target.Namespace,
			target.PodName,
		)
		if sinceSeconds > 0 {
			url = fmt.Sprintf("%s&sinceSeconds=%d", url, sinceSeconds)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create k8s pod logs request: %w", err)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("k8s pod logs request failed: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("k8s API returned status %d", resp.StatusCode)
		}
		return resp.Body, nil
	}

	url := fmt.Sprintf("%s/containers/%s/logs?follow=1&stdout=1&stderr=1&timestamps=1",
		strings.TrimRight(c.dockerBaseURL, "/"),
		target.ID,
	)
	if sinceSeconds > 0 {
		url = fmt.Sprintf("%s&since=%d", url, sinceSeconds)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create docker logs request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker logs request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("docker API returned status %d", resp.StatusCode)
	}

	return resp.Body, nil
}

// StreamContainerLogs открывает поток логов контейнера и парсит записи через StreamReader.
func (c *RuntimeClient) StreamContainerLogs(ctx context.Context, target contracts.TiltTarget, sinceSeconds int, onRecord func(contracts.RawScriptRecord)) error {
	stream, err := c.OpenContainerLogs(ctx, target, sinceSeconds)
	if err != nil {
		return err
	}
	defer stream.Close()
	return StreamReader(ctx, stream, target, onRecord)
}

