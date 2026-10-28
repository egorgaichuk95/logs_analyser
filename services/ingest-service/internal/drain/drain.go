package drain

import (
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

const (
	maxTreeDepth   = 4
	simThreshold   = 0.6
	maxChildren    = 100
	maxTemplateLen = 512
)

var (
	reURLQuery = regexp.MustCompile(`\?[^\s"']+`)
	reUUID     = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reHex      = regexp.MustCompile(`\b(?:0[xX][0-9a-fA-F]+|[0-9a-fA-F]{20,64})\b`)
	reIPv4     = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`)
	reNum      = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
)

type LogCluster struct {
	tokens []string
}

func (c *LogCluster) Template() string {
	if len(c.tokens) == 0 {
		return ""
	}
	tpl := strings.Join(c.tokens, " ")
	return truncateRunes(tpl, maxTemplateLen)
}

type Node struct {
	children map[string]*Node
	clusters []*LogCluster
}

func newNode() *Node {
	return &Node{
		children: make(map[string]*Node),
		clusters: make([]*LogCluster, 0),
	}
}

type Miner struct {
	mu        sync.Mutex
	cache     map[string]*LogCluster
	root      *Node
	templates map[uint64]*contracts.TemplateRecord
}

func NewMiner() *Miner {
	return &Miner{
		cache:     make(map[string]*LogCluster),
		root:      newNode(),
		templates: make(map[uint64]*contracts.TemplateRecord),
	}
}

func truncateRunes(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) > maxRunes {
		return string(r[:maxRunes])
	}
	return s
}

func fnv64a(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func preMask(ev *contracts.NormalizedEvent) string {
	baseText := ev.Message
	if ev.ErrorMsg != "" && !strings.Contains(ev.Message, ev.ErrorMsg) {
		if baseText == "" {
			baseText = ev.ErrorMsg
		} else {
			baseText = baseText + " | " + ev.ErrorMsg
		}
	}
	if baseText == "" {
		return ""
	}

	// 1. Query parameters in URL: \?[^\s"']+ -> ?<query>
	text := reURLQuery.ReplaceAllString(baseText, "?<query>")

	// 2. UUID: [0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12} -> <uuid>
	text = reUUID.ReplaceAllString(text, "<uuid>")

	// 3. Hex strings: (20..64 chars [0-9a-fA-F] containing digit and letter, or 0x[0-9a-fA-F]+) -> <hex>
	text = reHex.ReplaceAllStringFunc(text, func(m string) string {
		if strings.HasPrefix(m, "0x") || strings.HasPrefix(m, "0X") {
			return "<hex>"
		}
		hasDigit := false
		hasLetter := false
		for i := 0; i < len(m); i++ {
			c := m[i]
			if c >= '0' && c <= '9' {
				hasDigit = true
			} else if (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
				hasLetter = true
			}
		}
		if hasDigit && hasLetter {
			return "<hex>"
		}
		return m
	})

	// 4. IPv4: \b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b -> <ip>
	text = reIPv4.ReplaceAllString(text, "<ip>")

	// 5. Numbers: \b\d+(?:\.\d+)?\b -> <num>
	text = reNum.ReplaceAllString(text, "<num>")

	// 6. Truncate template to 512 characters
	return truncateRunes(text, maxTemplateLen)
}

func findBestCluster(clusters []*LogCluster, tokens []string) (*LogCluster, float64) {
	var bestCluster *LogCluster
	bestSim := -1.0
	L := len(tokens)
	if L == 0 {
		return nil, 0.0
	}

	for _, c := range clusters {
		if len(c.tokens) != L {
			continue
		}
		matchCount := 0
		for i := 0; i < L; i++ {
			if c.tokens[i] == tokens[i] || c.tokens[i] == "<*>" {
				matchCount++
			}
		}
		sim := float64(matchCount) / float64(L)
		if sim > bestSim {
			bestSim = sim
			bestCluster = c
		}
	}
	return bestCluster, bestSim
}

func updateCluster(c *LogCluster, tokens []string) {
	for i := 0; i < len(tokens); i++ {
		if c.tokens[i] != tokens[i] {
			c.tokens[i] = "<*>"
		}
	}
}

func (m *Miner) treeInsert(tokens []string) *LogCluster {
	L := len(tokens)
	if L == 0 {
		return &LogCluster{tokens: nil}
	}

	lenKey := strconv.Itoa(L)
	lenNode, ok := m.root.children[lenKey]
	if !ok {
		lenNode = newNode()
		m.root.children[lenKey] = lenNode
	}

	curr := lenNode
	maxSteps := maxTreeDepth - 2
	if L < maxSteps {
		maxSteps = L
	}

	for step := 0; step < maxSteps; step++ {
		token := tokens[step]
		child, exists := curr.children[token]
		if !exists {
			if len(curr.children) < maxChildren {
				child = newNode()
				curr.children[token] = child
			} else {
				child, exists = curr.children["*"]
				if !exists {
					child = newNode()
					curr.children["*"] = child
				}
			}
		}
		curr = child
	}

	// 1. Search in current leaf node
	bestCluster, bestSim := findBestCluster(curr.clusters, tokens)
	if bestCluster != nil && bestSim >= simThreshold {
		updateCluster(bestCluster, tokens)
		return bestCluster
	}

	// 2. Search fallback in all clusters of same length
	bestLenCluster, bestLenSim := findBestCluster(lenNode.clusters, tokens)
	if bestLenCluster != nil && bestLenSim >= simThreshold {
		updateCluster(bestLenCluster, tokens)
		curr.clusters = append(curr.clusters, bestLenCluster)
		return bestLenCluster
	}

	// 3. New cluster
	newCluster := &LogCluster{
		tokens: make([]string, L),
	}
	copy(newCluster.tokens, tokens)

	curr.clusters = append(curr.clusters, newCluster)
	lenNode.clusters = append(lenNode.clusters, newCluster)
	return newCluster
}

// Mine принимает событие ev, вычисляет для него TemplateID и шаблон текста,
// записывает ev.TemplateID и возвращает TemplateRecord для сохранения в справочник templates_dict.
func (m *Miner) Mine(ev *contracts.NormalizedEvent) contracts.TemplateRecord {
	if ev == nil {
		return contracts.TemplateRecord{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	maskedText := preMask(ev)
	var finalTemplate string

	if maskedText == "" {
		finalTemplate = ""
	} else if cluster, ok := m.cache[maskedText]; ok {
		finalTemplate = cluster.Template()
	} else {
		tokens := strings.Fields(maskedText)
		cluster := m.treeInsert(tokens)
		m.cache[maskedText] = cluster
		finalTemplate = cluster.Template()
	}

	finalTemplate = truncateRunes(finalTemplate, maxTemplateLen)

	templateKey := ev.Service + "|" + ev.Level + "|" + ev.SourcePlace + "|" + finalTemplate
	templateID := fnv64a(templateKey)
	ev.TemplateID = templateID

	sampleRedacted := ev.RawRedacted
	if sampleRedacted == "" {
		sampleRedacted = ev.Message
	}
	if sampleRedacted == "" {
		sampleRedacted = ev.ErrorMsg
	}

	ts := ev.Ts
	if ts.IsZero() {
		ts = time.Now()
	}

	rec, exists := m.templates[templateID]
	if !exists {
		newRec := contracts.TemplateRecord{
			CaseID:         ev.CaseID,
			Service:        ev.Service,
			TemplateID:     templateID,
			TemplateText:   finalTemplate,
			Level:          ev.Level,
			ErrorClass:     ev.ErrorClass,
			SourcePlace:    ev.SourcePlace,
			SampleRedacted: sampleRedacted,
			FirstSeen:      ts,
			LastSeen:       ts,
		}
		m.templates[templateID] = &newRec
		return newRec
	}

	if rec.FirstSeen.IsZero() || ts.Before(rec.FirstSeen) {
		rec.FirstSeen = ts
	}
	if ts.After(rec.LastSeen) {
		rec.LastSeen = ts
	}
	if rec.SampleRedacted == "" && sampleRedacted != "" {
		rec.SampleRedacted = sampleRedacted
	}
	rec.TemplateText = finalTemplate
	return *rec
}
