package timeseries

import (
	"math"
	"sort"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

type LeadLagResult struct {
	LinksByTemplate map[uint64][]contracts.LeadLagLink
	ScoreByTemplate map[uint64]float64 // S3 [0..1]
}

// pearsonShifted сравнивает a[0 : N-lagSteps] и b[lagSteps : N] для lagSteps > 0.
// При lagSteps < 0 ряд a отстаёт от b (сравниваются a[-lagSteps : N] и b[0 : N+lagSteps]).
// Содержит критическую проверку ssA <= 1e-12 || ssB <= 1e-12 для предотвращения деления на ноль и NaN.
func pearsonShifted(a, b []float64, lagSteps int) float64 {
	n := len(a)
	if n != len(b) || n < 3 {
		return 0.0
	}

	var sliceA, sliceB []float64
	if lagSteps > 0 {
		if lagSteps >= n {
			return 0.0
		}
		sliceA = a[0 : n-lagSteps]
		sliceB = b[lagSteps : n]
	} else if lagSteps < 0 {
		absLag := -lagSteps
		if absLag >= n {
			return 0.0
		}
		sliceA = a[absLag : n]
		sliceB = b[0 : n-absLag]
	} else {
		sliceA = a
		sliceB = b
	}

	k := len(sliceA)
	if k < 2 {
		return 0.0
	}

	var sumA, sumB float64
	for i := 0; i < k; i++ {
		sumA += sliceA[i]
		sumB += sliceB[i]
	}
	meanA := sumA / float64(k)
	meanB := sumB / float64(k)

	var ssA, ssB, cov float64
	for i := 0; i < k; i++ {
		diffA := sliceA[i] - meanA
		diffB := sliceB[i] - meanB
		ssA += diffA * diffA
		ssB += diffB * diffB
		cov += diffA * diffB
	}

	if ssA <= 1e-12 || ssB <= 1e-12 {
		return 0.0
	}

	denom := math.Sqrt(ssA * ssB)
	if denom <= 1e-12 {
		return 0.0
	}

	r := cov / denom
	if r > 1.0 {
		r = 1.0
	} else if r < -1.0 {
		r = -1.0
	}

	return contracts.SanitizeFloat(r)
}

// ComputeLeadLag вычисляет сдвиговую корреляцию между нешумовыми рядами разных сервисов.
func ComputeLeadLag(stats map[uint64]TemplateStats, stepSeconds int) LeadLagResult {
	result := LeadLagResult{
		LinksByTemplate: make(map[uint64][]contracts.LeadLagLink, len(stats)),
		ScoreByTemplate: make(map[uint64]float64, len(stats)),
	}

	for tpl := range stats {
		result.ScoreByTemplate[tpl] = 0.0
		result.LinksByTemplate[tpl] = make([]contracts.LeadLagLink, 0)
	}

	if len(stats) == 0 {
		return result
	}

	if stepSeconds <= 0 {
		stepSeconds = 1
	}
	stepDur := time.Duration(stepSeconds) * time.Second

	// 1. Поиск глобальных minTs и maxTs для нешумовых рядов (!IsConstantNoise)
	var (
		hasTs bool
		minTs time.Time
		maxTs time.Time
	)

	for _, stat := range stats {
		if stat.IsConstantNoise {
			continue
		}
		for _, pt := range stat.Buckets {
			if !hasTs {
				minTs = pt.Ts
				maxTs = pt.Ts
				hasTs = true
				continue
			}
			if pt.Ts.Before(minTs) {
				minTs = pt.Ts
			}
			if pt.Ts.After(maxTs) {
				maxTs = pt.Ts
			}
		}
	}

	if !hasTs {
		return result
	}

	totalDur := maxTs.Sub(minTs)
	if totalDur < 0 {
		return result
	}

	totalSteps := int(totalDur / stepDur)
	N := totalSteps + 1
	// 2. Если N < 3, вернуть нулевые оценки 0.0
	if N < 3 {
		return result
	}

	// Выравнивание всех нешумовых рядов на единую временную сетку [minTs .. maxTs] с шагом stepSeconds
	nonNoiseIDs := make([]uint64, 0, len(stats))
	vectors := make(map[uint64][]float64)

	for tpl, stat := range stats {
		if stat.IsConstantNoise || stat.Service == "" {
			continue
		}
		nonNoiseIDs = append(nonNoiseIDs, tpl)
		vec := make([]float64, N)
		for _, pt := range stat.Buckets {
			idx := int(pt.Ts.Sub(minTs) / stepDur)
			if idx >= 0 && idx < N {
				vec[idx] += float64(pt.Count)
			}
		}
		vectors[tpl] = vec
	}

	if len(nonNoiseIDs) < 2 {
		return result
	}

	sort.Slice(nonNoiseIDs, func(i, j int) bool {
		return nonNoiseIDs[i] < nonNoiseIDs[j]
	})

	// 4. Для каждой пары шаблонов (A, B) с serviceA != serviceB:
	//    Найти tauStar в [1 .. min(60, N/2)], при котором r(tauStar) максимален.
	maxTau := N / 2
	if maxTau > 60 {
		maxTau = 60
	}

	laggedBy := make(map[uint64][]float64)
	leadingOf := make(map[uint64][]float64)

	for _, tplA := range nonNoiseIDs {
		svcA := stats[tplA].Service
		vecA := vectors[tplA]

		for _, tplB := range nonNoiseIDs {
			if tplA == tplB {
				continue
			}
			svcB := stats[tplB].Service
			if svcA == svcB {
				continue
			}
			vecB := vectors[tplB]

			var (
				bestTau int
				bestR   = -2.0
			)
			for tau := 1; tau <= maxTau; tau++ {
				r := pearsonShifted(vecA, vecB, tau)
				if r > bestR {
					bestR = r
					bestTau = tau
				}
			}

			if bestR >= 0.6 {
				sanitizedR := contracts.SanitizeFloat(bestR)
				link := contracts.LeadLagLink{
					PeerService:  svcB,
					PeerTemplate: tplB,
					LagSeconds:   bestTau * stepSeconds,
					CorrelationR: sanitizedR,
				}
				result.LinksByTemplate[tplA] = append(result.LinksByTemplate[tplA], link)
				laggedBy[tplB] = append(laggedBy[tplB], sanitizedR)
				leadingOf[tplA] = append(leadingOf[tplA], sanitizedR)
			}
		}
	}

	// Сортировка ссылок: по убыванию CorrelationR, затем по возрастанию LagSeconds
	for tpl, links := range result.LinksByTemplate {
		if len(links) > 1 {
			sort.Slice(links, func(i, j int) bool {
				if links[i].CorrelationR != links[j].CorrelationR {
					return links[i].CorrelationR > links[j].CorrelationR
				}
				return links[i].LagSeconds < links[j].LagSeconds
			})
			result.LinksByTemplate[tpl] = links
		}
	}

	// 5. Для каждого шаблона A:
	//    - если он опережает хотя бы один другой сервис с rMax >= 0.6 и сам не отстаёт от других -> Score = rMax;
	//    - если он отстаёт от другого сервиса -> Score = 0.2 * rMax;
	//    - иначе -> 0.0.
	for _, tpl := range nonNoiseIDs {
		leadsList := leadingOf[tpl]
		lagsList := laggedBy[tpl]

		var maxLeadR float64
		for _, r := range leadsList {
			if r > maxLeadR {
				maxLeadR = r
			}
		}

		var maxLagR float64
		for _, r := range lagsList {
			if r > maxLagR {
				maxLagR = r
			}
		}

		leads := len(leadsList) > 0 && maxLeadR >= 0.6
		lags := len(lagsList) > 0 && maxLagR >= 0.6

		if leads && !lags {
			result.ScoreByTemplate[tpl] = contracts.SanitizeFloat(maxLeadR)
		} else if lags {
			rMax := math.Max(maxLeadR, maxLagR)
			result.ScoreByTemplate[tpl] = contracts.SanitizeFloat(0.2 * rMax)
		} else {
			result.ScoreByTemplate[tpl] = 0.0
		}
	}

	return result
}
