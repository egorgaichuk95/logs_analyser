package timeseries

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

// TestLeadLag_TwoImpulsesShiftedByThree проверяет:
// Два идентичных импульса в сервисе A и сервисе B, где импульс в B сдвинут на +3 шага относительно A:
// - LagSeconds == 3
// - CorrelationR > 0.95
// - ScoreByTemplate[tplA] > ScoreByTemplate[tplB]
func TestLeadLag_TwoImpulsesShiftedByThree(t *testing.T) {
	baseTime := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	n := 20

	bucketsA := make([]BucketPoint, n)
	bucketsB := make([]BucketPoint, n)

	for i := 0; i < n; i++ {
		ts := baseTime.Add(time.Duration(i) * time.Second)
		countA := uint64(0)
		countB := uint64(0)

		// Импульс в сервисе A на 5-м шаге
		if i == 5 {
			countA = 100
		}
		// Импульс в сервисе B на 8-м шаге (сдвиг +3)
		if i == 8 {
			countB = 100
		}

		bucketsA[i] = BucketPoint{Ts: ts, Count: countA}
		bucketsB[i] = BucketPoint{Ts: ts, Count: countB}
	}

	tplA := uint64(1001)
	tplB := uint64(2001)

	stats := map[uint64]TemplateStats{
		tplA: {
			Service:         "chat-com",
			TemplateID:      tplA,
			IsConstantNoise: false,
			Buckets:         bucketsA,
		},
		tplB: {
			Service:         "chat-session",
			TemplateID:      tplB,
			IsConstantNoise: false,
			Buckets:         bucketsB,
		},
	}

	res := ComputeLeadLag(stats, 1)

	linksA, okA := res.LinksByTemplate[tplA]
	if !okA || len(linksA) == 0 {
		t.Fatalf("expected at least 1 link for tplA, got %d", len(linksA))
	}

	link := linksA[0]
	if link.PeerService != "chat-session" {
		t.Errorf("expected PeerService 'chat-session', got %q", link.PeerService)
	}
	if link.PeerTemplate != tplB {
		t.Errorf("expected PeerTemplate %d, got %d", tplB, link.PeerTemplate)
	}
	if link.LagSeconds != 3 {
		t.Errorf("expected LagSeconds == 3, got %d", link.LagSeconds)
	}
	if link.CorrelationR <= 0.95 {
		t.Errorf("expected CorrelationR > 0.95, got %f", link.CorrelationR)
	}

	scoreA := res.ScoreByTemplate[tplA]
	scoreB := res.ScoreByTemplate[tplB]
	if scoreA <= scoreB {
		t.Errorf("expected ScoreA (%f) > ScoreB (%f)", scoreA, scoreB)
	}
	if scoreA <= 0.95 {
		t.Errorf("expected ScoreA > 0.95, got %f", scoreA)
	}
	if scoreB <= 0.0 || scoreB > 0.3 {
		t.Errorf("expected ScoreB around 0.2, got %f", scoreB)
	}

	linksB := res.LinksByTemplate[tplB]
	if len(linksB) != 0 {
		t.Errorf("expected tplB to have 0 outgoing links (it lags, does not lead), got %d", len(linksB))
	}
}

// TestLeadLag_ZeroVariance_NoNaN проверяет:
// Постоянные ряды (нулевая дисперсия) не вызывают деления на ноль или NaN (возврат 0.0).
func TestLeadLag_ZeroVariance_NoNaN(t *testing.T) {
	// 1. Прямая проверка pearsonShifted на константных векторах
	zeros := []float64{0, 0, 0, 0, 0}
	consts := []float64{7, 7, 7, 7, 7}
	varying := []float64{1, 2, 3, 4, 5}

	r1 := pearsonShifted(zeros, zeros, 1)
	if math.IsNaN(r1) || r1 != 0.0 {
		t.Errorf("expected 0.0 without NaN for zeros, got %f", r1)
	}

	r2 := pearsonShifted(consts, varying, 1)
	if math.IsNaN(r2) || r2 != 0.0 {
		t.Errorf("expected 0.0 without NaN for consts vs varying, got %f", r2)
	}

	r3 := pearsonShifted(varying, consts, 1)
	if math.IsNaN(r3) || r3 != 0.0 {
		t.Errorf("expected 0.0 without NaN for varying vs consts, got %f", r3)
	}

	// 2. Проверка через ComputeLeadLag
	baseTime := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	n := 10
	bucketsZero := make([]BucketPoint, n)
	bucketsConst := make([]BucketPoint, n)

	for i := 0; i < n; i++ {
		ts := baseTime.Add(time.Duration(i) * time.Second)
		bucketsZero[i] = BucketPoint{Ts: ts, Count: 0}
		bucketsConst[i] = BucketPoint{Ts: ts, Count: 10}
	}

	tpl1 := uint64(101)
	tpl2 := uint64(201)

	stats := map[uint64]TemplateStats{
		tpl1: {
			Service:         "svc-a",
			TemplateID:      tpl1,
			IsConstantNoise: false,
			Buckets:         bucketsZero,
		},
		tpl2: {
			Service:         "svc-b",
			TemplateID:      tpl2,
			IsConstantNoise: false,
			Buckets:         bucketsConst,
		},
	}

	res := ComputeLeadLag(stats, 1)
	score1 := res.ScoreByTemplate[tpl1]
	score2 := res.ScoreByTemplate[tpl2]

	if math.IsNaN(score1) || score1 != 0.0 {
		t.Errorf("expected score 0.0 for zero series, got %f", score1)
	}
	if math.IsNaN(score2) || score2 != 0.0 {
		t.Errorf("expected score 0.0 for const series, got %f", score2)
	}
	if len(res.LinksByTemplate[tpl1]) != 0 || len(res.LinksByTemplate[tpl2]) != 0 {
		t.Errorf("expected 0 links for constant series")
	}
}

// TestLeadLag_SameServiceIgnored проверяет:
// Шаблоны из одного сервиса не сравниваются между собой (корреляция только межсервисная).
func TestLeadLag_SameServiceIgnored(t *testing.T) {
	baseTime := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	n := 15

	bucketsA := make([]BucketPoint, n)
	bucketsB := make([]BucketPoint, n)

	for i := 0; i < n; i++ {
		ts := baseTime.Add(time.Duration(i) * time.Second)
		countA := uint64(0)
		countB := uint64(0)
		if i == 3 {
			countA = 50
		}
		if i == 6 {
			countB = 50
		}
		bucketsA[i] = BucketPoint{Ts: ts, Count: countA}
		bucketsB[i] = BucketPoint{Ts: ts, Count: countB}
	}

	tplA := uint64(1001)
	tplB := uint64(1002)

	// Оба шаблона принадлежат ОДНОМУ сервису "chat-com"
	stats := map[uint64]TemplateStats{
		tplA: {
			Service:         "chat-com",
			TemplateID:      tplA,
			IsConstantNoise: false,
			Buckets:         bucketsA,
		},
		tplB: {
			Service:         "chat-com",
			TemplateID:      tplB,
			IsConstantNoise: false,
			Buckets:         bucketsB,
		},
	}

	res := ComputeLeadLag(stats, 1)

	if len(res.LinksByTemplate[tplA]) != 0 {
		t.Errorf("expected 0 links between same-service templates, got %d", len(res.LinksByTemplate[tplA]))
	}
	if len(res.LinksByTemplate[tplB]) != 0 {
		t.Errorf("expected 0 links between same-service templates, got %d", len(res.LinksByTemplate[tplB]))
	}
	if res.ScoreByTemplate[tplA] != 0.0 || res.ScoreByTemplate[tplB] != 0.0 {
		t.Errorf("expected 0.0 scores for same-service templates, got %f, %f",
			res.ScoreByTemplate[tplA], res.ScoreByTemplate[tplB])
	}
}

// TestLeadLag_BoundaryAndEdgeCases проверяет:
// N < 3, отсутствие нешумовых рядов, порог r >= 0.6, отрицательные и некорректные шаги, пустые входные данные.
func TestLeadLag_BoundaryAndEdgeCases(t *testing.T) {
	baseTime := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

	// 1. Пустой вход stats
	resEmpty := ComputeLeadLag(nil, 1)
	if len(resEmpty.ScoreByTemplate) != 0 || len(resEmpty.LinksByTemplate) != 0 {
		t.Errorf("expected empty result for nil stats")
	}

	// 2. Все шаблоны помечены IsConstantNoise == true
	statsAllNoise := map[uint64]TemplateStats{
		1: {
			Service:         "svc-a",
			TemplateID:      1,
			IsConstantNoise: true,
			Buckets: []BucketPoint{
				{Ts: baseTime, Count: 10},
				{Ts: baseTime.Add(time.Second), Count: 20},
				{Ts: baseTime.Add(2 * time.Second), Count: 30},
			},
		},
		2: {
			Service:         "svc-b",
			TemplateID:      2,
			IsConstantNoise: true,
			Buckets: []BucketPoint{
				{Ts: baseTime, Count: 10},
				{Ts: baseTime.Add(time.Second), Count: 20},
				{Ts: baseTime.Add(2 * time.Second), Count: 30},
			},
		},
	}
	resNoise := ComputeLeadLag(statsAllNoise, 1)
	if resNoise.ScoreByTemplate[1] != 0.0 || resNoise.ScoreByTemplate[2] != 0.0 {
		t.Errorf("expected 0.0 scores for all-noise stats")
	}
	if len(resNoise.LinksByTemplate[1]) != 0 || len(resNoise.LinksByTemplate[2]) != 0 {
		t.Errorf("expected 0 links for all-noise stats")
	}

	// 3. Сетка с N < 3 (например, только 2 точки)
	statsSmallN := map[uint64]TemplateStats{
		10: {
			Service:         "svc-a",
			TemplateID:      10,
			IsConstantNoise: false,
			Buckets: []BucketPoint{
				{Ts: baseTime, Count: 10},
				{Ts: baseTime.Add(time.Second), Count: 20},
			},
		},
		20: {
			Service:         "svc-b",
			TemplateID:      20,
			IsConstantNoise: false,
			Buckets: []BucketPoint{
				{Ts: baseTime, Count: 15},
				{Ts: baseTime.Add(time.Second), Count: 25},
			},
		},
	}
	resSmallN := ComputeLeadLag(statsSmallN, 1)
	if resSmallN.ScoreByTemplate[10] != 0.0 || resSmallN.ScoreByTemplate[20] != 0.0 {
		t.Errorf("expected 0.0 score when N < 3")
	}

	// 4. Порог r >= 0.6: некоррелирующие или противофазные ряды
	n := 12
	bucketsOrthA := make([]BucketPoint, n)
	bucketsOrthB := make([]BucketPoint, n)
	for i := 0; i < n; i++ {
		ts := baseTime.Add(time.Duration(i) * time.Second)
		cntA := uint64(0)
		cntB := uint64(0)
		// Чередующиеся пики, не дающие корреляцию >= 0.6 при любом сдвиге
		if i%3 == 0 {
			cntA = 100
		}
		if i%4 == 1 {
			cntB = 100
		}
		bucketsOrthA[i] = BucketPoint{Ts: ts, Count: cntA}
		bucketsOrthB[i] = BucketPoint{Ts: ts, Count: cntB}
	}
	statsUncorr := map[uint64]TemplateStats{
		30: {Service: "svc-a", TemplateID: 30, Buckets: bucketsOrthA},
		40: {Service: "svc-b", TemplateID: 40, Buckets: bucketsOrthB},
	}
	resUncorr := ComputeLeadLag(statsUncorr, 1)
	// Должно быть 0 ссылок, если r < 0.6
	for _, links := range resUncorr.LinksByTemplate {
		for _, l := range links {
			if l.CorrelationR < 0.6 {
				t.Errorf("unexpected link with CorrelationR < 0.6: %f", l.CorrelationR)
			}
		}
	}

	// 5. Неположительный stepSeconds (0 или отрицательный) должен безопасно нормализоваться в 1
	resNonPosStep := ComputeLeadLag(statsSmallN, 0)
	if resNonPosStep.ScoreByTemplate[10] != 0.0 {
		t.Errorf("expected 0.0 for stepSeconds <= 0 on small N")
	}

	// 6. Шаблоны с пустыми service ("")
	statsEmptySvc := map[uint64]TemplateStats{
		50: {Service: "", TemplateID: 50, Buckets: bucketsOrthA},
		60: {Service: "svc-b", TemplateID: 60, Buckets: bucketsOrthB},
	}
	resEmptySvc := ComputeLeadLag(statsEmptySvc, 1)
	if len(resEmptySvc.LinksByTemplate[50]) != 0 || len(resEmptySvc.LinksByTemplate[60]) != 0 {
		t.Errorf("expected no links when service name is empty")
	}

	// 7. Шаблоны без бакетов
	statsNoBuckets := map[uint64]TemplateStats{
		70: {Service: "svc-a", TemplateID: 70, Buckets: nil},
		80: {Service: "svc-b", TemplateID: 80, Buckets: []BucketPoint{}},
	}
	resNoBuckets := ComputeLeadLag(statsNoBuckets, 1)
	if resNoBuckets.ScoreByTemplate[70] != 0.0 || resNoBuckets.ScoreByTemplate[80] != 0.0 {
		t.Errorf("expected 0.0 scores for templates with no buckets")
	}
}

// TestLeadLag_PearsonShifted_Comprehensive проверяет:
// Функцию pearsonShifted на всех типах сдвигов (положительные, нулевые, отрицательные, граничные).
func TestLeadLag_PearsonShifted_Comprehensive(t *testing.T) {
	// Исходный ряд
	a := []float64{0, 100, 0, 0, 0, 0, 0, 0, 0, 0}
	b := []float64{0, 0, 0, 0, 100, 0, 0, 0, 0, 0} // сдвиг +3 относительно a

	// Положительный сдвиг: a опережает b на 3 шага
	rPos := pearsonShifted(a, b, 3)
	if rPos <= 0.95 {
		t.Errorf("expected rPos > 0.95 for shifted identical pulse, got %f", rPos)
	}

	// Отрицательный сдвиг: b отстаёт от a на 3 шага (или a отстаёт от b на -3)
	// pearsonShifted(b, a, -3) должно совпадать с pearsonShifted(a, b, 3)
	rNeg := pearsonShifted(b, a, -3)
	if math.Abs(rNeg-rPos) > 1e-6 {
		t.Errorf("expected pearsonShifted(b, a, -3) == %f, got %f", rPos, rNeg)
	}

	// Нулевой сдвиг: идентичные ряды
	rZero := pearsonShifted(a, a, 0)
	if rZero < 0.9999 {
		t.Errorf("expected rZero == 1.0 for self-correlation, got %f", rZero)
	}

	// Граничные случаи: сдвиг >= N или <= -N
	rOut := pearsonShifted(a, b, 10)
	if rOut != 0.0 {
		t.Errorf("expected 0.0 for lag >= N, got %f", rOut)
	}
	rOutNeg := pearsonShifted(a, b, -10)
	if rOutNeg != 0.0 {
		t.Errorf("expected 0.0 for lag <= -N, got %f", rOutNeg)
	}

	// Перекрытие < 2 элементов
	rSmallOverlap := pearsonShifted(a, b, 9)
	if rSmallOverlap != 0.0 {
		t.Errorf("expected 0.0 for overlap < 2 elements, got %f", rSmallOverlap)
	}

	// Разная длина векторов
	rMismatch := pearsonShifted(a, []float64{1, 2, 3}, 1)
	if rMismatch != 0.0 {
		t.Errorf("expected 0.0 for mismatched lengths, got %f", rMismatch)
	}

	// Длина векторов < 3
	rShort := pearsonShifted([]float64{1, 2}, []float64{2, 3}, 0)
	if rShort != 0.0 {
		t.Errorf("expected 0.0 for N < 3, got %f", rShort)
	}
}

// TestLeadLag_PropagationChain проверяет:
// Цепочку распространения A -> B -> C:
// Сервис A (корневая причина) опережает B, сервис B опережает C.
// ScoreA должен быть максимальным (~1.0), а ScoreB и ScoreC пенализированы (0.2 * r).
func TestLeadLag_PropagationChain(t *testing.T) {
	baseTime := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	n := 25

	bucketsA := make([]BucketPoint, n)
	bucketsB := make([]BucketPoint, n)
	bucketsC := make([]BucketPoint, n)

	for i := 0; i < n; i++ {
		ts := baseTime.Add(time.Duration(i) * time.Second)
		cA, cB, cC := uint64(0), uint64(0), uint64(0)
		if i == 5 {
			cA = 80
		}
		if i == 7 { // B отстаёт от A на 2 шага
			cB = 80
		}
		if i == 9 { // C отстаёт от B на 2 шага
			cC = 80
		}
		bucketsA[i] = BucketPoint{Ts: ts, Count: cA}
		bucketsB[i] = BucketPoint{Ts: ts, Count: cB}
		bucketsC[i] = BucketPoint{Ts: ts, Count: cC}
	}

	tplA := uint64(10)
	tplB := uint64(20)
	tplC := uint64(30)

	stats := map[uint64]TemplateStats{
		tplA: {Service: "service-a", TemplateID: tplA, Buckets: bucketsA},
		tplB: {Service: "service-b", TemplateID: tplB, Buckets: bucketsB},
		tplC: {Service: "service-c", TemplateID: tplC, Buckets: bucketsC},
	}

	res := ComputeLeadLag(stats, 1)

	scoreA := res.ScoreByTemplate[tplA]
	scoreB := res.ScoreByTemplate[tplB]
	scoreC := res.ScoreByTemplate[tplC]

	// Сервис A должен быть лидером (ScoreA > 0.95)
	if scoreA <= 0.95 {
		t.Errorf("expected scoreA > 0.95 for root cause, got %f", scoreA)
	}

	// Сервисы B и C отстают, поэтому получают 0.2 * rMax
	if scoreB <= 0.0 || scoreB >= scoreA {
		t.Errorf("expected scoreB around 0.2 and < scoreA, got %f (scoreA=%f)", scoreB, scoreA)
	}
	if scoreC <= 0.0 || scoreC >= scoreA {
		t.Errorf("expected scoreC around 0.2 and < scoreA, got %f (scoreA=%f)", scoreC, scoreA)
	}

	// Ссылки: A опережает B (LagSeconds=2) и C (LagSeconds=4)
	linksA := res.LinksByTemplate[tplA]
	if len(linksA) == 0 {
		t.Fatalf("expected links from A, got 0")
	}

	// Проверяем, что есть связь к service-b с лагом 2
	var foundLinkToB bool
	for _, l := range linksA {
		if l.PeerService == "service-b" && l.LagSeconds == 2 {
			foundLinkToB = true
			if l.CorrelationR <= 0.95 {
				t.Errorf("expected CorrelationR > 0.95 to B, got %f", l.CorrelationR)
			}
		}
	}
	if !foundLinkToB {
		t.Errorf("expected link from A to service-b with lag 2")
	}
}

// TestLeadLag_ContractsAndJSONSafety проверяет:
// Корректную сериализацию LeadLagResult и LeadLagLink в JSON без NaN/Inf.
func TestLeadLag_ContractsAndJSONSafety(t *testing.T) {
	res := LeadLagResult{
		LinksByTemplate: map[uint64][]contracts.LeadLagLink{
			1001: {
				{
					PeerService:  "chat-session",
					PeerTemplate: 2001,
					LagSeconds:   3,
					CorrelationR: contracts.SanitizeFloat(0.9876),
				},
			},
		},
		ScoreByTemplate: map[uint64]float64{
			1001: contracts.SanitizeFloat(0.9876),
			2001: contracts.SanitizeFloat(0.1975),
		},
	}

	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var parsed LeadLagResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if len(parsed.LinksByTemplate[1001]) != 1 {
		t.Errorf("expected 1 parsed link, got %d", len(parsed.LinksByTemplate[1001]))
	}
	if math.Abs(parsed.ScoreByTemplate[1001]-0.9876) > 1e-4 {
		t.Errorf("expected score 0.9876, got %f", parsed.ScoreByTemplate[1001])
	}

	// Проверка Hypothesis.Sanitize с LeadLagPeers
	hyp := contracts.Hypothesis{
		ID:         "hyp-1",
		Confidence: math.NaN(),
		LeadLagPeers: []contracts.LeadLagLink{
			{CorrelationR: math.NaN()},
		},
		Signals: contracts.SignalBreakdown{
			LeadLagScore: math.NaN(),
		},
	}
	hyp.Sanitize()

	if math.IsNaN(hyp.Confidence) || hyp.Confidence != 0.0 {
		t.Errorf("expected sanitized confidence 0.0")
	}
	if math.IsNaN(hyp.LeadLagPeers[0].CorrelationR) || hyp.LeadLagPeers[0].CorrelationR != 0.0 {
		t.Errorf("expected sanitized correlation 0.0")
	}
	if math.IsNaN(hyp.Signals.LeadLagScore) || hyp.Signals.LeadLagScore != 0.0 {
		t.Errorf("expected sanitized lead_lag_score 0.0")
	}
}
