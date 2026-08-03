package interpreter

import (
	"math"
	"testing"
)

// распределение должно гарантировать, что сумма долей == total.
func TestDistribute_SumMatchesTotal(t *testing.T) {
	cases := []struct {
		total   float64
		weights []float64
		scale   int
	}{
		{100, []float64{1, 1, 1}, 2},   // 33.33+33.33+33.34
		{100, []float64{1, 2, 3}, 2},   // 16.67+33.33+50.00
		{10.01, []float64{1, 1, 1}, 2}, // прилипающая копейка
		{1, []float64{1, 1, 1}, 2},     // деление < scale
		{0, []float64{1, 2, 3}, 2},     // ноль
		{100, []float64{0, 1, 0}, 2},   // нули в весах
		{-100, []float64{1, 2, 3}, 2},  // отрицательная сумма
	}
	for _, c := range cases {
		shares := DistributeAmount(c.total, c.weights, c.scale)
		var sum float64
		for _, s := range shares {
			sum += s
		}
		factor := math.Pow(10, float64(c.scale))
		if math.Abs(math.Round(sum*factor)-math.Round(c.total*factor)) > 0.001 {
			t.Errorf("DistributeAmount(%v, %v, %d) = %v, sum=%v ≠ total=%v",
				c.total, c.weights, c.scale, shares, sum, c.total)
		}
	}
}

func TestDistribute_Proportional(t *testing.T) {
	shares := DistributeAmount(100, []float64{1, 2, 3}, 2)
	// 1/6=16.67, 2/6=33.33, 3/6=50.00 → сумма 100
	if shares[0] != 16.67 {
		t.Errorf("shares[0] = %v, want 16.67", shares[0])
	}
	if shares[1] != 33.33 {
		t.Errorf("shares[1] = %v, want 33.33", shares[1])
	}
	if shares[2] != 50.00 {
		t.Errorf("shares[2] = %v, want 50.00", shares[2])
	}
}

// «Прилипшая копейка» — классический сценарий ФИФО.
func TestDistribute_StuckKopeck(t *testing.T) {
	// 10.01 на 3 равные доли. Без компенсации: 3.34+3.34+3.34=10.02 (+0.01),
	// либо 3.34+3.33+3.34=10.01 (зависит от округления). С компенсацией
	// последняя доля поглощает diff.
	shares := DistributeAmount(10.01, []float64{1, 1, 1}, 2)
	var sum float64
	for _, s := range shares {
		sum += s
	}
	if math.Abs(sum-10.01) > 0.001 {
		t.Errorf("сумма = %v, не сошлась с 10.01: %v", sum, shares)
	}
}

func TestDistribute_EmptyWeights(t *testing.T) {
	shares := DistributeAmount(100, nil, 2)
	if len(shares) != 0 {
		t.Errorf("пустой вход → %v, ожидался []", shares)
	}
}

func TestDistribute_ZeroSumWeights(t *testing.T) {
	shares := DistributeAmount(100, []float64{0, 0, 0}, 2)
	for i, s := range shares {
		if s != 0 {
			t.Errorf("shares[%d] = %v, ожидался 0", i, s)
		}
	}
}

// Округление до целых.
func TestDistribute_Integers(t *testing.T) {
	shares := DistributeAmount(10, []float64{1, 1, 1}, 0)
	var sum float64
	for _, s := range shares {
		sum += s
	}
	if sum != 10 {
		t.Errorf("сумма = %v, ожидалось 10: %v", sum, shares)
	}
}
