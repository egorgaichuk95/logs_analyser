package timeseries

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

// TestConstantNoiseCache98 проверяет:
// Ряд cache:98 (190/мин в baseline, 195/мин в инциденте -> IsConstantNoise == true, NoveltyScore == 0.0, OnsetScore == 0.0)
func TestConstantNoiseCache98(t *testing.T) {
	tBaseStart := time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)
	tBaselineEnd := time.Date(2026, 10, 28, 8, 10, 0, 0, time.UTC)

	// 10 минут baseline (190/мин) и 10 минут incident (195/мин)
	var buckets []BucketPoint
	for i := 0; i < 10; i++ {
		buckets = append(buckets, BucketPoint{
			Ts:    tBaseStart.Add(time.Duration(i) * time.Minute),
			Count: 190,
		})
	}
	for i := 0; i < 10; i++ {
		buckets = append(buckets, BucketPoint{
			Ts:    tBaselineEnd.Add(time.Duration(i) * time.Minute),
			Count: 195,
		})
	}

	const tplCache98 = 98
	series := map[uint64][]BucketPoint{
		tplCache98: buckets,
	}
	serviceByTpl := map[uint64]string{
		tplCache98: "chat-com",
	}

	res := AnalyzeSeries(series, serviceByTpl, nil, tBaselineEnd)
	stats, ok := res[tplCache98]
	if !ok {
		t.Fatalf("expected stats for template %d", tplCache98)
	}

	if stats.Service != "chat-com" {
		t.Errorf("expected service 'chat-com', got %s", stats.Service)
	}
	if math.Abs(stats.BaselineRatePerMin-190.0) > 0.1 {
		t.Errorf("expected BaselineRatePerMin ~ 190.0, got %f", stats.BaselineRatePerMin)
	}
	if math.Abs(stats.IncidentRatePerMin-195.0) > 0.1 {
		t.Errorf("expected IncidentRatePerMin ~ 195.0, got %f", stats.IncidentRatePerMin)
	}
	if !stats.IsConstantNoise {
		t.Errorf("expected IsConstantNoise == true, got false (GrowthRatio=%f)", stats.GrowthRatio)
	}
	if stats.NoveltyScore != 0.0 {
		t.Errorf("expected NoveltyScore == 0.0, got %f", stats.NoveltyScore)
	}
	if stats.OnsetScore != 0.0 {
		t.Errorf("expected OnsetScore == 0.0, got %f", stats.OnsetScore)
	}
}

// TestOnsetAndNoveltyDetection проверяет:
// Ряд pool_exhausted (0 в baseline, всплеск в 08:20:00Z -> NoveltyScore == 1.0, OnsetScore == 1.0)
// и ряд upstream_timeout (всплеск на 15с позже в 08:20:15Z -> OnsetScore ~ 0.3678)
func TestOnsetAndNoveltyDetection(t *testing.T) {
	tPool := time.Date(2026, 10, 28, 8, 20, 0, 0, time.UTC)
	tUpstream := time.Date(2026, 10, 28, 8, 20, 15, 0, time.UTC)
	tBaselineEnd := tPool

	const tplPool = 101
	const tplUpstream = 102

	series := map[uint64][]BucketPoint{
		tplPool: {
			{Ts: tPool, Count: 25},
			{Ts: tPool.Add(1 * time.Minute), Count: 30},
		},
		tplUpstream: {
			{Ts: tUpstream, Count: 40},
			{Ts: tUpstream.Add(1 * time.Minute), Count: 45},
		},
	}
	serviceByTpl := map[uint64]string{
		tplPool:     "chat-com",
		tplUpstream: "chat-session",
	}

	res := AnalyzeSeries(series, serviceByTpl, nil, tBaselineEnd)

	poolStats, ok := res[tplPool]
	if !ok {
		t.Fatalf("expected stats for pool_exhausted (%d)", tplPool)
	}
	if poolStats.BaselineRatePerMin != 0.0 {
		t.Errorf("expected pool BaselineRatePerMin == 0.0, got %f", poolStats.BaselineRatePerMin)
	}
	if poolStats.IsConstantNoise {
		t.Errorf("expected pool IsConstantNoise == false, got true")
	}
	if poolStats.NoveltyScore != 1.0 {
		t.Errorf("expected pool NoveltyScore == 1.0, got %f", poolStats.NoveltyScore)
	}
	if poolStats.OnsetScore != 1.0 {
		t.Errorf("expected pool OnsetScore == 1.0, got %f", poolStats.OnsetScore)
	}
	if !poolStats.OnsetTime.Equal(tPool) {
		t.Errorf("expected pool OnsetTime == %v, got %v", tPool, poolStats.OnsetTime)
	}

	upstreamStats, ok := res[tplUpstream]
	if !ok {
		t.Fatalf("expected stats for upstream_timeout (%d)", tplUpstream)
	}
	if upstreamStats.IsConstantNoise {
		t.Errorf("expected upstream IsConstantNoise == false, got true")
	}
	if upstreamStats.NoveltyScore != 1.0 {
		t.Errorf("expected upstream NoveltyScore == 1.0, got %f", upstreamStats.NoveltyScore)
	}
	expectedOnset := math.Exp(-15.0 / 15.0) // exp(-1) ~ 0.36787944
	if math.Abs(upstreamStats.OnsetScore-expectedOnset) > 0.001 {
		t.Errorf("expected upstream OnsetScore ~ %f, got %f", expectedOnset, upstreamStats.OnsetScore)
	}
	if !upstreamStats.OnsetTime.Equal(tUpstream) {
		t.Errorf("expected upstream OnsetTime == %v, got %v", tUpstream, upstreamStats.OnsetTime)
	}
}

// TestCombinedNoiseAndOnset проверяет совместную работу:
// шумовой ряд cache:98 не должен влиять на вычисление t_min для нешумовых кандидатов.
func TestCombinedNoiseAndOnset(t *testing.T) {
	tBaseStart := time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)
	tBaselineEnd := time.Date(2026, 10, 28, 8, 10, 0, 0, time.UTC)
	tPool := tBaselineEnd
	tUpstream := tBaselineEnd.Add(30 * time.Second)

	const tplNoise = 98
	const tplPool = 101
	const tplUpstream = 102

	var noiseBuckets []BucketPoint
	for i := 0; i < 10; i++ {
		noiseBuckets = append(noiseBuckets, BucketPoint{
			Ts:    tBaseStart.Add(time.Duration(i) * time.Minute),
			Count: 190,
		})
	}
	for i := 0; i < 10; i++ {
		noiseBuckets = append(noiseBuckets, BucketPoint{
			Ts:    tBaselineEnd.Add(time.Duration(i) * time.Minute),
			Count: 195,
		})
	}

	series := map[uint64][]BucketPoint{
		tplNoise: noiseBuckets,
		tplPool: {
			{Ts: tPool, Count: 50},
		},
		tplUpstream: {
			{Ts: tUpstream, Count: 60},
		},
	}
	serviceByTpl := map[uint64]string{
		tplNoise:    "chat-com",
		tplPool:     "chat-com",
		tplUpstream: "chat-session",
	}

	res := AnalyzeSeries(series, serviceByTpl, nil, tBaselineEnd)

	if !res[tplNoise].IsConstantNoise {
		t.Errorf("expected tplNoise to be constant noise")
	}
	if res[tplNoise].OnsetScore != 0.0 {
		t.Errorf("expected tplNoise OnsetScore == 0.0, got %f", res[tplNoise].OnsetScore)
	}
	if res[tplPool].OnsetScore != 1.0 {
		t.Errorf("expected tplPool OnsetScore == 1.0, got %f", res[tplPool].OnsetScore)
	}
	expectedUpstreamOnset := math.Exp(-30.0 / 15.0) // exp(-2) ~ 0.1353
	if math.Abs(res[tplUpstream].OnsetScore-expectedUpstreamOnset) > 0.001 {
		t.Errorf("expected tplUpstream OnsetScore ~ %f, got %f", expectedUpstreamOnset, res[tplUpstream].OnsetScore)
	}
}

// TestPoissonPValueApproximation проверяет формулу Пуассона и граничное условие z <= 0 -> 1.0
func TestPoissonPValueApproximation(t *testing.T) {
	tBaseStart := time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)
	tBaselineEnd := time.Date(2026, 10, 28, 8, 5, 0, 0, time.UTC)

	// Сценарий 1: спад частоты (z <= 0) -> PoissonPValue должно быть строго 1.0
	const tplDrop = 201
	seriesDrop := map[uint64][]BucketPoint{
		tplDrop: {
			{Ts: tBaseStart, Count: 100}, // 20/мин
			{Ts: tBaselineEnd, Count: 10}, // 2/мин
		},
	}
	resDrop := AnalyzeSeries(seriesDrop, map[uint64]string{tplDrop: "svc"}, nil, tBaselineEnd)
	if resDrop[tplDrop].PoissonPValue != 1.0 {
		t.Errorf("expected PoissonPValue == 1.0 for z <= 0, got %f", resDrop[tplDrop].PoissonPValue)
	}

	// Сценарий 2: одинаковая частота (lambda_inc == lambda_base, z == 0) -> PoissonPValue == 1.0
	const tplFlat = 202
	seriesFlat := map[uint64][]BucketPoint{
		tplFlat: {
			{Ts: tBaseStart, Count: 50},
			{Ts: tBaselineEnd, Count: 50},
		},
	}
	resFlat := AnalyzeSeries(seriesFlat, map[uint64]string{tplFlat: "svc"}, nil, tBaselineEnd)
	if resFlat[tplFlat].PoissonPValue != 1.0 {
		t.Errorf("expected PoissonPValue == 1.0 for z == 0, got %f", resFlat[tplFlat].PoissonPValue)
	}

	// Сценарий 3: заметный рост частоты (z > 0) -> PValue < 0.05
	const tplSurge = 203
	seriesSurge := map[uint64][]BucketPoint{
		tplSurge: {
			{Ts: tBaseStart, Count: 10}, // baseline ~2/min
			{Ts: tBaselineEnd, Count: 200}, // incident ~40/min
		},
	}
	resSurge := AnalyzeSeries(seriesSurge, map[uint64]string{tplSurge: "svc"}, nil, tBaselineEnd)
	if resSurge[tplSurge].PoissonPValue >= 0.05 {
		t.Errorf("expected PoissonPValue < 0.05 for surge, got %f", resSurge[tplSurge].PoissonPValue)
	}
}

// TestHistoricalBaseRateFallback проверяет historicalBaseRatePerMin при отсутствии точек baseline в текущем кейсе
func TestHistoricalBaseRateFallback(t *testing.T) {
	tBaselineEnd := time.Date(2026, 10, 28, 8, 10, 0, 0, time.UTC)
	tInc := tBaselineEnd.Add(1 * time.Minute)

	const tplWithHist = 301
	const tplWithoutHist = 302

	series := map[uint64][]BucketPoint{
		tplWithHist: {
			{Ts: tInc, Count: 100}, // incident rate ~100/min (or 200/min based on window)
		},
		tplWithoutHist: {
			{Ts: tInc, Count: 100},
		},
	}

	histRates := map[uint64]float64{
		tplWithHist: 10.0, // историческая частота 10/мин
	}

	res := AnalyzeSeries(series, map[uint64]string{
		tplWithHist:    "svc-a",
		tplWithoutHist: "svc-b",
	}, histRates, tBaselineEnd)

	// tplWithHist должен использовать historicalBaseRatePerMin = 10.0
	statsWith, ok := res[tplWithHist]
	if !ok {
		t.Fatalf("expected stats for %d", tplWithHist)
	}
	if statsWith.BaselineRatePerMin != 10.0 {
		t.Errorf("expected BaselineRatePerMin == 10.0, got %f", statsWith.BaselineRatePerMin)
	}
	if statsWith.GrowthRatio <= 1.0 {
		t.Errorf("expected GrowthRatio > 1.0, got %f", statsWith.GrowthRatio)
	}

	// tplWithoutHist должен иметь BaselineRatePerMin == 0.0 и NoveltyScore == 1.0
	statsWithout, ok := res[tplWithoutHist]
	if !ok {
		t.Fatalf("expected stats for %d", tplWithoutHist)
	}
	if statsWithout.BaselineRatePerMin != 0.0 {
		t.Errorf("expected BaselineRatePerMin == 0.0, got %f", statsWithout.BaselineRatePerMin)
	}
	if statsWithout.NoveltyScore != 1.0 {
		t.Errorf("expected NoveltyScore == 1.0, got %f", statsWithout.NoveltyScore)
	}
}

// TestMathSafetyAndNaNProtection проверяет защиту от деления на 0, NaN и Inf через contracts.SanitizeFloat
func TestMathSafetyAndNaNProtection(t *testing.T) {
	tNow := time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)

	// Сценарий 1: пустые ряды
	resEmpty := AnalyzeSeries(map[uint64][]BucketPoint{}, nil, nil, tNow)
	if len(resEmpty) != 0 {
		t.Errorf("expected empty result for empty series")
	}

	// Сценарий 2: нулевые счетчики во всех бакетах
	const tplZero = 401
	seriesZero := map[uint64][]BucketPoint{
		tplZero: {
			{Ts: tNow, Count: 0},
			{Ts: tNow.Add(1 * time.Minute), Count: 0},
		},
	}
	resZero := AnalyzeSeries(seriesZero, map[uint64]string{tplZero: "zero-svc"}, nil, tNow.Add(30*time.Second))
	statsZero := resZero[tplZero]

	assertNotNaNOrInf(t, "BaselineRatePerMin", statsZero.BaselineRatePerMin)
	assertNotNaNOrInf(t, "IncidentRatePerMin", statsZero.IncidentRatePerMin)
	assertNotNaNOrInf(t, "GrowthRatio", statsZero.GrowthRatio)
	assertNotNaNOrInf(t, "PoissonPValue", statsZero.PoissonPValue)
	assertNotNaNOrInf(t, "OnsetScore", statsZero.OnsetScore)
	assertNotNaNOrInf(t, "NoveltyScore", statsZero.NoveltyScore)

	// Сценарий 3: валидность JSON-сериализации (стандартный json.Marshal падает при NaN/Inf)
	jsonData, err := json.Marshal(resZero)
	if err != nil {
		t.Fatalf("json.Marshal failed with NaN/Inf: %v", err)
	}
	if len(jsonData) == 0 {
		t.Errorf("expected non-empty JSON data")
	}

	// Сценарий 4: все точки в один и тот же момент времени (delta == 0)
	const tplSameTs = 402
	seriesSameTs := map[uint64][]BucketPoint{
		tplSameTs: {
			{Ts: tNow, Count: 5},
			{Ts: tNow, Count: 5},
		},
	}
	resSameTs := AnalyzeSeries(seriesSameTs, map[uint64]string{tplSameTs: "same-svc"}, nil, tNow)
	statsSame := resSameTs[tplSameTs]
	assertNotNaNOrInf(t, "GrowthRatio", statsSame.GrowthRatio)
	assertNotNaNOrInf(t, "OnsetScore", statsSame.OnsetScore)
}

// TestBaselineAutoCalculation проверяет автоматический расчёт baselineEnd, если передан time.Time{}
func TestBaselineAutoCalculation(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)
	const tpl = 501

	var buckets []BucketPoint
	// 50 минут: первые 10 минут тишина, затем всплеск
	for i := 0; i < 50; i++ {
		cnt := uint64(0)
		if i >= 10 {
			cnt = 100
		}
		buckets = append(buckets, BucketPoint{
			Ts:    t0.Add(time.Duration(i) * time.Minute),
			Count: cnt,
		})
	}

	series := map[uint64][]BucketPoint{tpl: buckets}
	res := AnalyzeSeries(series, map[uint64]string{tpl: "auto-svc"}, nil, time.Time{})

	stats, ok := res[tpl]
	if !ok {
		t.Fatalf("expected stats for template %d", tpl)
	}

	// Автоматический baseline должен отработать и зафиксировать Onset и Novelty
	if stats.NoveltyScore <= 0.0 {
		t.Errorf("expected NoveltyScore > 0, got %f", stats.NoveltyScore)
	}
	if stats.OnsetScore <= 0.0 {
		t.Errorf("expected OnsetScore > 0, got %f", stats.OnsetScore)
	}
}

// TestBaselineFluctuationsThreshold проверяет расчёт threshold = math.Max(1.0, mean + 3*std)
func TestBaselineFluctuationsThreshold(t *testing.T) {
	tBaseStart := time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)
	tBaselineEnd := time.Date(2026, 10, 28, 8, 5, 0, 0, time.UTC)
	const tpl = 601

	// Базовый шум: mean = 10, колебания от 8 до 12
	buckets := []BucketPoint{
		{Ts: tBaseStart, Count: 8},
		{Ts: tBaseStart.Add(1 * time.Minute), Count: 12},
		{Ts: tBaseStart.Add(2 * time.Minute), Count: 9},
		{Ts: tBaseStart.Add(3 * time.Minute), Count: 11},
		{Ts: tBaseStart.Add(4 * time.Minute), Count: 10},
		// В инциденте: первый бакет = 13 (ниже mean+3*std ~ 14.2), второй = 50 (превысил threshold)
		{Ts: tBaselineEnd, Count: 13},
		{Ts: tBaselineEnd.Add(1 * time.Minute), Count: 50},
	}

	series := map[uint64][]BucketPoint{tpl: buckets}
	res := AnalyzeSeries(series, map[uint64]string{tpl: "fluct-svc"}, nil, tBaselineEnd)
	stats := res[tpl]

	expectedOnsetTime := tBaselineEnd.Add(1 * time.Minute)
	if !stats.OnsetTime.Equal(expectedOnsetTime) {
		t.Errorf("expected OnsetTime == %v (first bucket >= threshold), got %v", expectedOnsetTime, stats.OnsetTime)
	}
}

func assertNotNaNOrInf(t *testing.T, field string, v float64) {
	t.Helper()
	if math.IsNaN(v) {
		t.Errorf("field %s is NaN", field)
	}
	if math.IsInf(v, 0) {
		t.Errorf("field %s is Inf", field)
	}
	if contracts.SanitizeFloat(v) != v {
		t.Errorf("field %s (%f) is not sanitized", field, v)
	}
}
