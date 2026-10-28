package timeseries

import (
	"math"
	"sort"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

type BucketPoint struct {
	Ts    time.Time
	Count uint64
}

type TemplateStats struct {
	Service            string
	TemplateID         uint64
	BaselineRatePerMin float64
	IncidentRatePerMin float64
	GrowthRatio        float64
	PoissonPValue      float64
	IsConstantNoise    bool
	OnsetTime          time.Time
	OnsetScore         float64 // S2 [0..1]
	NoveltyScore       float64 // S4 [0..1]
	Buckets            []BucketPoint
}

// AnalyzeSeries рассчитывает S2 и S4 для набора временных рядов шаблонов.
// Если baselineDur <= 0, автоматически берёт первые 20% интервала до первого скачка как baseline.
func AnalyzeSeries(
	series map[uint64][]BucketPoint,
	serviceByTpl map[uint64]string,
	historicalBaseRatePerMin map[uint64]float64,
	baselineEnd time.Time,
) map[uint64]TemplateStats {
	result := make(map[uint64]TemplateStats, len(series))
	if len(series) == 0 {
		return result
	}

	// 1. Поиск глобальных minTs, maxTs и шага бакетов
	var (
		caseMinTs     time.Time
		caseMaxTs     time.Time
		hasTimestamps bool
		uniqueTsMap   = make(map[int64]time.Time)
		tsCountMap    = make(map[int64]uint64)
	)

	for _, pts := range series {
		for _, pt := range pts {
			unixSec := pt.Ts.Unix()
			uniqueTsMap[unixSec] = pt.Ts
			tsCountMap[unixSec] += pt.Count

			if !hasTimestamps {
				caseMinTs = pt.Ts
				caseMaxTs = pt.Ts
				hasTimestamps = true
				continue
			}
			if pt.Ts.Before(caseMinTs) {
				caseMinTs = pt.Ts
			}
			if pt.Ts.After(caseMaxTs) {
				caseMaxTs = pt.Ts
			}
		}
	}

	if !hasTimestamps {
		for tpl := range series {
			var svc string
			if serviceByTpl != nil {
				svc = serviceByTpl[tpl]
			}
			result[tpl] = TemplateStats{
				Service:    svc,
				TemplateID: tpl,
				Buckets:    []BucketPoint{},
			}
		}
		return result
	}

	// Определение шага бакетов minStep
	uniqueTsList := make([]time.Time, 0, len(uniqueTsMap))
	for _, ts := range uniqueTsMap {
		uniqueTsList = append(uniqueTsList, ts)
	}
	sort.Slice(uniqueTsList, func(i, j int) bool {
		return uniqueTsList[i].Before(uniqueTsList[j])
	})

	var minStep time.Duration
	for i := 1; i < len(uniqueTsList); i++ {
		diff := uniqueTsList[i].Sub(uniqueTsList[i-1])
		if diff > 0 && (minStep == 0 || diff < minStep) {
			minStep = diff
		}
	}

	// Если baselineEnd не задан (zero time), вычисляем первые 20% интервала до первого скачка
	if baselineEnd.IsZero() {
		totalSpan := caseMaxTs.Sub(caseMinTs)
		if totalSpan > 0 {
			var totalCounts uint64
			for _, c := range tsCountMap {
				totalCounts += c
			}
			avgCount := float64(totalCounts) / float64(len(tsCountMap))

			var jumpTs time.Time
			foundJump := false
			for _, ts := range uniqueTsList {
				cnt := float64(tsCountMap[ts.Unix()])
				if cnt > math.Max(1.0, avgCount*2.0) {
					jumpTs = ts
					foundJump = true
					break
				}
			}

			if foundJump && jumpTs.After(caseMinTs) {
				spanBeforeJump := jumpTs.Sub(caseMinTs)
				baselineEnd = caseMinTs.Add(time.Duration(float64(spanBeforeJump) * 0.2))
			} else {
				baselineEnd = caseMinTs.Add(time.Duration(float64(totalSpan) * 0.2))
			}
		} else {
			baselineEnd = caseMinTs
		}
	}

	// Вычисление длительности baseline и incident в минутах
	var baseMinutes float64
	if caseMinTs.Before(baselineEnd) {
		baseMinutes = baselineEnd.Sub(caseMinTs).Minutes()
	}
	effectiveBaseMin := math.Max(baseMinutes, 0.5)

	var incMinutes float64
	if !caseMaxTs.Before(baselineEnd) {
		incEnd := caseMaxTs
		if minStep > 0 {
			incEnd = incEnd.Add(minStep)
		}
		if incEnd.After(baselineEnd) {
			incMinutes = incEnd.Sub(baselineEnd).Minutes()
		}
	}
	effectiveIncMin := math.Max(incMinutes, 0.5)

	// Вспомогательная структура для первичного прохода
	type interimCandidate struct {
		tpl             uint64
		svc             string
		lambdaBase      float64
		lambdaInc       float64
		growthRatio     float64
		poissonPValue   float64
		isConstantNoise bool
		noveltyScore    float64
		hasOnset        bool
		onsetTime       time.Time
		sortedBuckets   []BucketPoint
	}

	interim := make([]interimCandidate, 0, len(series))
	var (
		minOnsetTime time.Time
		hasMinOnset  bool
	)

	for tpl, pts := range series {
		var svc string
		if serviceByTpl != nil {
			svc = serviceByTpl[tpl]
		}

		sorted := make([]BucketPoint, len(pts))
		copy(sorted, pts)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Ts.Before(sorted[j].Ts)
		})

		var (
			baseBuckets     []BucketPoint
			incidentBuckets []BucketPoint
			sumBase         uint64
			sumInc          uint64
		)

		for _, b := range sorted {
			if b.Ts.Before(baselineEnd) {
				baseBuckets = append(baseBuckets, b)
				sumBase += b.Count
			} else {
				incidentBuckets = append(incidentBuckets, b)
				sumInc += b.Count
			}
		}

		// 1. Частоты BaselineRatePerMin и IncidentRatePerMin
		var lambdaBase float64
		if len(baseBuckets) > 0 {
			lambdaBase = float64(sumBase) / effectiveBaseMin
		} else if historicalBaseRatePerMin != nil && historicalBaseRatePerMin[tpl] > 0 {
			lambdaBase = historicalBaseRatePerMin[tpl]
		} else {
			lambdaBase = 0.0
		}
		lambdaInc := float64(sumInc) / effectiveIncMin

		// 2. Классификация фонового шума и NoveltyScore
		var (
			growthRatio     float64
			noveltyScore    float64
			isConstantNoise bool
		)

		if lambdaBase == 0.0 && sumInc > 0 {
			growthRatio = contracts.SanitizeFloat(math.Min(lambdaInc*10.0, 100.0))
			noveltyScore = 1.0
			isConstantNoise = false
		} else if lambdaBase > 0.0 {
			growthRatio = contracts.SanitizeFloat(lambdaInc / lambdaBase)
			if lambdaBase >= 5.0 && growthRatio < 1.5 {
				isConstantNoise = true
				noveltyScore = 0.0
			} else {
				isConstantNoise = false
				score := math.Min(1.0, math.Log10(math.Max(1.0, growthRatio))/1.5)
				noveltyScore = contracts.SanitizeFloat(score)
			}
		} else {
			growthRatio = 0.0
			noveltyScore = 0.0
			isConstantNoise = false
		}

		// 3. PoissonPValue
		zDenom := math.Sqrt(math.Max(lambdaBase, 0.1))
		z := (lambdaInc - lambdaBase) / zDenom
		var poissonPValue float64
		if z <= 0.0 {
			poissonPValue = 1.0
		} else {
			poissonPValue = contracts.SanitizeFloat(0.5 * math.Erfc(z/math.Sqrt2))
		}

		// 4. Определение OnsetTime
		var (
			onsetTime time.Time
			hasOnset  bool
		)

		if !isConstantNoise && len(incidentBuckets) > 0 {
			var (
				meanBasePerBucket float64
				stdBasePerBucket  float64
			)
			if len(baseBuckets) > 0 {
				var countSum float64
				for _, b := range baseBuckets {
					countSum += float64(b.Count)
				}
				meanBasePerBucket = countSum / float64(len(baseBuckets))

				var varianceSum float64
				for _, b := range baseBuckets {
					diff := float64(b.Count) - meanBasePerBucket
					varianceSum += diff * diff
				}
				stdBasePerBucket = math.Sqrt(varianceSum / float64(len(baseBuckets)))
			}

			threshold := math.Max(1.0, meanBasePerBucket+3.0*stdBasePerBucket)
			for _, b := range incidentBuckets {
				if float64(b.Count) >= threshold {
					onsetTime = b.Ts
					hasOnset = true
					break
				}
			}

			if hasOnset {
				if !hasMinOnset || onsetTime.Before(minOnsetTime) {
					minOnsetTime = onsetTime
					hasMinOnset = true
				}
			}
		}

		interim = append(interim, interimCandidate{
			tpl:             tpl,
			svc:             svc,
			lambdaBase:      contracts.SanitizeFloat(lambdaBase),
			lambdaInc:       contracts.SanitizeFloat(lambdaInc),
			growthRatio:     contracts.SanitizeFloat(growthRatio),
			poissonPValue:   contracts.SanitizeFloat(poissonPValue),
			isConstantNoise: isConstantNoise,
			noveltyScore:    contracts.SanitizeFloat(noveltyScore),
			hasOnset:        hasOnset,
			onsetTime:       onsetTime,
			sortedBuckets:   sorted,
		})
	}

	// 5. Расчёт OnsetScore (S2)
	for _, cand := range interim {
		var onsetScore float64
		if !cand.isConstantNoise && cand.hasOnset && hasMinOnset {
			deltaSec := math.Max(0.0, cand.onsetTime.Sub(minOnsetTime).Seconds())
			onsetScore = contracts.SanitizeFloat(math.Exp(-deltaSec / 15.0))
		} else {
			onsetScore = 0.0
		}

		result[cand.tpl] = TemplateStats{
			Service:            cand.svc,
			TemplateID:         cand.tpl,
			BaselineRatePerMin: cand.lambdaBase,
			IncidentRatePerMin: cand.lambdaInc,
			GrowthRatio:        cand.growthRatio,
			PoissonPValue:      cand.poissonPValue,
			IsConstantNoise:    cand.isConstantNoise,
			OnsetTime:          cand.onsetTime,
			OnsetScore:         onsetScore,
			NoveltyScore:       cand.noveltyScore,
			Buckets:            cand.sortedBuckets,
		}
	}

	return result
}
