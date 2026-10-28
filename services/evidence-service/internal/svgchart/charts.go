package svgchart

import (
	"encoding/xml"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

// RenderTimelineSVG генерирует валидный автономный SVG (900x320) таймлайна событий с отметками Onset
func RenderTimelineSVG(points []contracts.TimelinePoint, hypotheses []contracts.Hypothesis) string {
	const (
		svgWidth    = 900
		svgHeight   = 320
		chartLeft   = 60
		chartRight  = 860
		chartTop    = 70
		chartBottom = 260
	)
	plotWidth := float64(chartRight - chartLeft)
	plotHeight := float64(chartBottom - chartTop)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<svg width="%d" height="%d" viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg">`+"\n",
		svgWidth, svgHeight, svgWidth, svgHeight))

	// Embedded styles & background
	sb.WriteString(`  <defs>
    <linearGradient id="bg-grad" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0%" stop-color="#0f172a" />
      <stop offset="100%" stop-color="#1e293b" />
    </linearGradient>
  </defs>
  <rect width="900" height="320" rx="8" fill="url(#bg-grad)" stroke="#334155" stroke-width="1" />
`)

	// Header & title
	sb.WriteString(`  <text x="30" y="32" fill="#f8fafc" font-family="system-ui, -apple-system, sans-serif" font-size="14" font-weight="700">Timeline: Event Frequencies &amp; Failure Onsets</text>
  <text x="30" y="48" fill="#94a3b8" font-family="system-ui, -apple-system, sans-serif" font-size="11">Time-series distribution across microservices</text>
`)

	// Legend
	sb.WriteString(`  <g font-family="system-ui, -apple-system, sans-serif" font-size="10">
    <rect x="560" y="24" width="10" height="10" rx="2" fill="#ef4444" />
    <text x="575" y="33" fill="#cbd5e1">ERROR</text>
    <rect x="630" y="24" width="10" height="10" rx="2" fill="#f59e0b" />
    <text x="645" y="33" fill="#cbd5e1">WARN</text>
    <rect x="695" y="24" width="10" height="10" rx="2" fill="#3b82f6" />
    <text x="710" y="33" fill="#cbd5e1">INFO</text>
    <line x1="755" y1="29" x2="775" y2="29" stroke="#dc2626" stroke-width="2" stroke-dasharray="3,3" />
    <text x="782" y="33" fill="#fca5a5" font-weight="600">ONSET</text>
  </g>
`)

	if len(points) == 0 {
		sb.WriteString(`  <text x="450" y="165" text-anchor="middle" fill="#64748b" font-family="system-ui, -apple-system, sans-serif" font-size="13">No timeline events recorded</text>
`)
		// If hypotheses exist, list them
		if len(hypotheses) > 0 {
			yPos := 195
			for _, h := range hypotheses {
				if yPos > 280 {
					break
				}
				escapedSvc := xmlEscape(h.Service)
				escapedRole := xmlEscape(h.Role)
				sb.WriteString(fmt.Sprintf(`  <text x="450" y="%d" text-anchor="middle" fill="#94a3b8" font-family="system-ui, -apple-system, sans-serif" font-size="11">%s [%s]</text>`+"\n",
					yPos, escapedSvc, escapedRole))
				yPos += 18
			}
		}
		sb.WriteString("</svg>")
		return sb.String()
	}

	// 1. Time range determination
	minTime := points[0].Bucket
	maxTime := points[0].Bucket
	maxEvents := uint64(1)

	for _, p := range points {
		if p.Bucket.Before(minTime) {
			minTime = p.Bucket
		}
		if p.Bucket.After(maxTime) {
			maxTime = p.Bucket
		}
		if p.EventsCount > maxEvents {
			maxEvents = p.EventsCount
		}
	}
	for _, h := range hypotheses {
		if !h.OnsetTime.IsZero() {
			if h.OnsetTime.Before(minTime) {
				minTime = h.OnsetTime
			}
			if h.OnsetTime.After(maxTime) {
				maxTime = h.OnsetTime
			}
		}
	}

	// Pad time range if empty or too small
	totalDur := maxTime.Sub(minTime)
	if totalDur <= time.Minute {
		minTime = minTime.Add(-time.Minute)
		maxTime = maxTime.Add(time.Minute)
		totalDur = maxTime.Sub(minTime)
	}

	// Grid & Y-axis ticks
	sb.WriteString(`  <g stroke="#334155" stroke-width="1" stroke-dasharray="2,2">
`)
	ticks := 4
	for t := 0; t <= ticks; t++ {
		y := chartBottom - (float64(t)/float64(ticks))*plotHeight
		val := uint64(float64(t) / float64(ticks) * float64(maxEvents))
		sb.WriteString(fmt.Sprintf(`    <line x1="%d" y1="%.1f" x2="%d" y2="%.1f" />`+"\n", chartLeft, y, chartRight, y))
		sb.WriteString(fmt.Sprintf(`    <text x="%d" y="%.1f" fill="#64748b" font-family="system-ui, -apple-system, sans-serif" font-size="9" text-anchor="end">%d</text>`+"\n",
			chartLeft-8, y+3, val))
	}
	sb.WriteString(`  </g>
`)

	// X-axis baseline
	sb.WriteString(fmt.Sprintf(`  <line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#475569" stroke-width="1.5" />`+"\n",
		chartLeft, chartBottom, chartRight, chartBottom))

	// X-axis time labels (start and end)
	sb.WriteString(fmt.Sprintf(`  <text x="%d" y="%d" fill="#94a3b8" font-family="system-ui, -apple-system, sans-serif" font-size="9">%s</text>`+"\n",
		chartLeft, chartBottom+18, minTime.UTC().Format("15:04:05")))
	sb.WriteString(fmt.Sprintf(`  <text x="%d" y="%d" fill="#94a3b8" font-family="system-ui, -apple-system, sans-serif" font-size="9" text-anchor="end">%s</text>`+"\n",
		chartRight, chartBottom+18, maxTime.UTC().Format("15:04:05")))

	// Sort points by bucket
	sortedPoints := make([]contracts.TimelinePoint, len(points))
	copy(sortedPoints, points)
	sort.Slice(sortedPoints, func(i, j int) bool {
		return sortedPoints[i].Bucket.Before(sortedPoints[j].Bucket)
	})

	// Render bars for timeline points
	barWidth := math.Max(8.0, math.Min(26.0, plotWidth/float64(len(sortedPoints)+1)))
	for _, p := range sortedPoints {
		frac := float64(p.Bucket.Sub(minTime)) / float64(totalDur)
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		x := float64(chartLeft) + frac*(plotWidth-barWidth)
		barHeight := (float64(p.EventsCount) / float64(maxEvents)) * plotHeight
		if barHeight < 3.0 && p.EventsCount > 0 {
			barHeight = 3.0
		}
		y := float64(chartBottom) - barHeight

		fillColor := "#64748b"
		switch strings.ToUpper(p.Level) {
		case "ERROR", "FATAL":
			fillColor = "#ef4444"
		case "WARN", "WARNING":
			fillColor = "#f59e0b"
		case "INFO":
			fillColor = "#3b82f6"
		}

		escapedSvc := xmlEscape(p.Service)
		sb.WriteString(fmt.Sprintf(`  <rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2" fill="%s" opacity="0.85"><title>%s %s: %d events</title></rect>`+"\n",
			x, y, barWidth, barHeight, fillColor, escapedSvc, p.Level, p.EventsCount))

		// If this is an error and high enough, annotate service
		if p.EventsCount >= maxEvents/2 && p.Service != "" {
			sb.WriteString(fmt.Sprintf(`  <text x="%.1f" y="%.1f" fill="#f8fafc" font-family="system-ui, -apple-system, sans-serif" font-size="9" text-anchor="middle">%s</text>`+"\n",
				x+barWidth/2, math.Max(float64(chartTop)+10, y-4), escapedSvc))
		}
	}

	// Render Onset markers from hypotheses
	for _, h := range hypotheses {
		if h.OnsetTime.IsZero() {
			continue
		}
		frac := float64(h.OnsetTime.Sub(minTime)) / float64(totalDur)
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		ox := float64(chartLeft) + frac*plotWidth

		color := "#dc2626"
		if h.Role != contracts.RoleRootCause {
			color = "#ea580c"
		}

		escapedSvc := xmlEscape(h.Service)
		escapedRole := xmlEscape(h.Role)

		sb.WriteString(fmt.Sprintf(`  <line x1="%.1f" y1="%d" x2="%.1f" y2="%d" stroke="%s" stroke-width="2" stroke-dasharray="4,3" />`+"\n",
			ox, chartTop, ox, chartBottom, color))
		sb.WriteString(fmt.Sprintf(`  <circle cx="%.1f" cy="%d" r="4" fill="%s" />`+"\n",
			ox, chartTop, color))
		sb.WriteString(fmt.Sprintf(`  <text x="%.1f" y="%d" text-anchor="middle" fill="%s" font-family="system-ui, -apple-system, sans-serif" font-size="10" font-weight="700">Onset: %s (%s)</text>`+"\n",
			ox, chartTop-8, color, escapedSvc, escapedRole))
	}

	sb.WriteString("</svg>")
	return sb.String()
}

type topoNode struct {
	Service string
	Role    string
	X       float64
	Y       float64
	Width   float64
	Height  float64
}

// RenderTopologySVG генерирует валидный автономный SVG (800x280) графа сервисов с подсветкой ROOT_CAUSE
func RenderTopologySVG(edges []contracts.ServiceEdge, hypotheses []contracts.Hypothesis) string {
	const (
		svgWidth  = 800
		svgHeight = 280
	)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<svg width="%d" height="%d" viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg">`+"\n",
		svgWidth, svgHeight, svgWidth, svgHeight))

	// Embedded styles, arrows, markers
	sb.WriteString(`  <defs>
    <linearGradient id="topo-bg" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0%" stop-color="#0f172a" />
      <stop offset="100%" stop-color="#1e293b" />
    </linearGradient>
    <marker id="arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path d="M 0 1 L 10 5 L 0 9 z" fill="#ef4444" />
    </marker>
    <marker id="arrow-neutral" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path d="M 0 1 L 10 5 L 0 9 z" fill="#64748b" />
    </marker>
  </defs>
  <rect width="800" height="280" rx="8" fill="url(#topo-bg)" stroke="#334155" stroke-width="1" />
`)

	// Header
	sb.WriteString(`  <text x="30" y="32" fill="#f8fafc" font-family="system-ui, -apple-system, sans-serif" font-size="14" font-weight="700">Service Call Topology &amp; Failure Propagation</text>
  <text x="30" y="48" fill="#94a3b8" font-family="system-ui, -apple-system, sans-serif" font-size="11">Causal direction and error propagation path</text>
`)

	// Legend
	sb.WriteString(`  <g font-family="system-ui, -apple-system, sans-serif" font-size="10">
    <rect x="530" y="24" width="10" height="10" rx="2" fill="#450a0a" stroke="#ef4444" stroke-width="1.5" />
    <text x="546" y="33" fill="#fca5a5" font-weight="600">ROOT_CAUSE</text>
    <rect x="635" y="24" width="10" height="10" rx="2" fill="#451a03" stroke="#f59e0b" stroke-width="1.5" />
    <text x="651" y="33" fill="#fde68a">PROPAGATION</text>
    <line x1="740" y1="29" x2="760" y2="29" stroke="#ef4444" stroke-width="2" marker-end="url(#arrow)" />
    <text x="768" y="33" fill="#ef4444">Error</text>
  </g>
`)

	// 1. Collect all unique services and their roles
	serviceRoles := make(map[string]string)
	for _, h := range hypotheses {
		if h.Service != "" {
			serviceRoles[h.Service] = h.Role
		}
	}

	serviceSet := make(map[string]struct{})
	for _, e := range edges {
		if e.CallerService != "" {
			serviceSet[e.CallerService] = struct{}{}
		}
		if e.CalleeService != "" {
			serviceSet[e.CalleeService] = struct{}{}
		}
	}
	for s := range serviceRoles {
		serviceSet[s] = struct{}{}
	}

	if len(serviceSet) == 0 {
		sb.WriteString(`  <text x="400" y="145" text-anchor="middle" fill="#64748b" font-family="system-ui, -apple-system, sans-serif" font-size="13">No service topology data</text>
</svg>`)
		return sb.String()
	}

	// 2. Sort services for deterministic layout
	serviceList := make([]string, 0, len(serviceSet))
	for s := range serviceSet {
		serviceList = append(serviceList, s)
	}
	sort.Slice(serviceList, func(i, j int) bool {
		// Priority: SYMPTOM / caller first, PROPAGATION middle, ROOT_CAUSE last (or vice versa)
		rI := serviceRoles[serviceList[i]]
		rJ := serviceRoles[serviceList[j]]
		score := func(r string) int {
			switch r {
			case contracts.RoleSymptom:
				return 1
			case contracts.RolePropagation:
				return 2
			case contracts.RoleRootCause:
				return 3
			default:
				return 0
			}
		}
		if score(rI) != score(rJ) {
			return score(rI) < score(rJ)
		}
		return serviceList[i] < serviceList[j]
	})

	// 3. Layout nodes
	nodeWidth := 140.0
	nodeHeight := 52.0
	numNodes := len(serviceList)
	nodesMap := make(map[string]*topoNode)

	if numNodes <= 4 {
		// Single row layout centered
		totalWidth := float64(numNodes)*nodeWidth + float64(numNodes-1)*50.0
		startX := (float64(svgWidth) - totalWidth) / 2.0
		if startX < 40 {
			startX = 40
		}
		gap := (float64(svgWidth) - 2.0*startX - float64(numNodes)*nodeWidth) / math.Max(1.0, float64(numNodes-1))
		for i, s := range serviceList {
			x := startX + float64(i)*(nodeWidth+gap)
			y := 125.0
			nodesMap[s] = &topoNode{
				Service: s,
				Role:    serviceRoles[s],
				X:       x,
				Y:       y,
				Width:   nodeWidth,
				Height:  nodeHeight,
			}
		}
	} else {
		// Two rows layout
		row1Count := (numNodes + 1) / 2
		row2Count := numNodes - row1Count
		gap1 := (float64(svgWidth) - 80.0 - float64(row1Count)*nodeWidth) / math.Max(1.0, float64(row1Count-1))
		for i := 0; i < row1Count; i++ {
			s := serviceList[i]
			x := 40.0 + float64(i)*(nodeWidth+gap1)
			nodesMap[s] = &topoNode{
				Service: s,
				Role:    serviceRoles[s],
				X:       x,
				Y:       75.0,
				Width:   nodeWidth,
				Height:  nodeHeight,
			}
		}
		gap2 := (float64(svgWidth) - 80.0 - float64(row2Count)*nodeWidth) / math.Max(1.0, float64(row2Count-1))
		for i := 0; i < row2Count; i++ {
			s := serviceList[row1Count+i]
			x := 40.0 + float64(i)*(nodeWidth+gap2)
			nodesMap[s] = &topoNode{
				Service: s,
				Role:    serviceRoles[s],
				X:       x,
				Y:       180.0,
				Width:   nodeWidth,
				Height:  nodeHeight,
			}
		}
	}

	// 4. Render Edges
	for _, e := range edges {
		callerNode := nodesMap[e.CallerService]
		calleeNode := nodesMap[e.CalleeService]
		if callerNode == nil || calleeNode == nil {
			continue
		}

		c1x := callerNode.X + callerNode.Width/2.0
		c1y := callerNode.Y + callerNode.Height/2.0
		c2x := calleeNode.X + calleeNode.Width/2.0
		c2y := calleeNode.Y + calleeNode.Height/2.0

		// Clip line to node borders
		dx := c2x - c1x
		dy := c2y - c1y
		dist := math.Hypot(dx, dy)
		if dist < 1.0 {
			continue
		}
		ux := dx / dist
		uy := dy / dist

		// Start point outside caller node
		sx := c1x + ux*(callerNode.Width/2.2)
		sy := c1y + uy*(callerNode.Height/2.2)
		// End point outside callee node
		ex := c2x - ux*(calleeNode.Width/2.2)
		ey := c2y - uy*(calleeNode.Height/2.2)

		hasErrors := e.ErrorCount > 0
		strokeColor := "#64748b"
		marker := "url(#arrow-neutral)"
		strokeWidth := "1.5"
		dash := `stroke-dasharray="3,3"`
		if hasErrors {
			strokeColor = "#ef4444"
			marker = "url(#arrow)"
			strokeWidth = "2"
			dash = ""
		}

		sb.WriteString(fmt.Sprintf(`  <line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="%s" %s marker-end="%s" />`+"\n",
			sx, sy, ex, ey, strokeColor, strokeWidth, dash, marker))

		// Label near midpoint
		mx := (sx + ex) / 2.0
		my := (sy + ey) / 2.0
		if hasErrors {
			sb.WriteString(fmt.Sprintf(`  <rect x="%.1f" y="%.1f" width="54" height="15" rx="3" fill="#450a0a" stroke="#ef4444" stroke-width="0.8" />`+"\n",
				mx-27, my-8))
			sb.WriteString(fmt.Sprintf(`  <text x="%.1f" y="%.1f" fill="#fca5a5" font-family="system-ui, -apple-system, sans-serif" font-size="9" font-weight="700" text-anchor="middle">%d err</text>`+"\n",
				mx, my+3, e.ErrorCount))
		}
	}

	// 5. Render Nodes
	for _, s := range serviceList {
		n := nodesMap[s]
		if n == nil {
			continue
		}

		fillColor := "#1e293b"
		strokeColor := "#475569"
		strokeWidth := "1.5"
		textColor := "#f8fafc"
		badgeFill := ""
		badgeText := ""
		badgeTextColor := "#ffffff"

		switch n.Role {
		case contracts.RoleRootCause:
			fillColor = "#450a0a"
			strokeColor = "#ef4444"
			strokeWidth = "2.5"
			textColor = "#fef2f2"
			badgeFill = "#dc2626"
			badgeText = contracts.RoleRootCause
			badgeTextColor = "#ffffff"
		case contracts.RolePropagation:
			fillColor = "#451a03"
			strokeColor = "#f59e0b"
			strokeWidth = "2"
			textColor = "#fffbeb"
			badgeFill = "#d97706"
			badgeText = contracts.RolePropagation
			badgeTextColor = "#ffffff"
		case contracts.RoleSymptom:
			fillColor = "#1e1b4b"
			strokeColor = "#818cf8"
			strokeWidth = "1.5"
			textColor = "#e0e7ff"
			badgeFill = "#6366f1"
			badgeText = contracts.RoleSymptom
			badgeTextColor = "#ffffff"
		}

		escapedSvc := xmlEscape(n.Service)
		centerX := n.X + n.Width/2.0
		textY := n.Y + 22.0
		if badgeText != "" {
			textY = n.Y + 19.0
		}

		sb.WriteString(fmt.Sprintf(`  <g>
    <rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="6" fill="%s" stroke="%s" stroke-width="%s" />
    <text x="%.1f" y="%.1f" fill="%s" font-family="system-ui, -apple-system, sans-serif" font-size="12" font-weight="600" text-anchor="middle">%s</text>
`, n.X, n.Y, n.Width, n.Height, fillColor, strokeColor, strokeWidth, centerX, textY, textColor, escapedSvc))

		if badgeText != "" {
			badgeWidth := float64(len(badgeText)*6 + 16)
			bx := centerX - badgeWidth/2.0
			by := n.Y + 29.0
			sb.WriteString(fmt.Sprintf(`    <rect x="%.1f" y="%.1f" width="%.1f" height="15" rx="3" fill="%s" />
    <text x="%.1f" y="%.1f" fill="%s" font-family="system-ui, -apple-system, sans-serif" font-size="9" font-weight="700" text-anchor="middle">%s</text>
`, bx, by, badgeWidth, badgeFill, centerX, by+11.0, badgeTextColor, xmlEscape(badgeText)))
		}

		sb.WriteString(`  </g>
`)
	}

	sb.WriteString("</svg>")
	return sb.String()
}

func xmlEscape(s string) string {
	var buf strings.Builder
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}
