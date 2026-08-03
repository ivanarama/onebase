package interpreter

import "math"

// DistributeAmount распределяет total пропорционально weights, гарантируя
// что сумма получившихся долей строго равна total после округления до
// scale знаков. Накопленную ошибку округления отдаёт последней
// ненулевой доле — это устраняет «прилипающие копейки» на остатках
// списанных партий (
//
//	DistributeAmount(100, []float64{1, 2, 3}, 2) → [16.67, 33.33, 50.00]
//
// При нулевой total или сумме весов == 0 возвращает массив нулей той же длины.
func DistributeAmount(total float64, weights []float64, scale int) []float64 {
	out := make([]float64, len(weights))
	if len(weights) == 0 {
		return out
	}

	var sumW float64
	for _, w := range weights {
		sumW += w
	}
	if sumW == 0 || total == 0 {
		return out
	}

	factor := math.Pow(10, float64(scale))
	round := func(v float64) float64 { return math.Round(v*factor) / factor }

	// Сначала распределяем без компенсации.
	var allocated float64
	lastNonZero := -1
	for i, w := range weights {
		out[i] = round(total * w / sumW)
		allocated += out[i]
		if w != 0 {
			lastNonZero = i
		}
	}

	// Компенсируем накопленную ошибку округления в последней ненулевой доле.
	if lastNonZero >= 0 {
		diff := round(total - allocated)
		if diff != 0 {
			out[lastNonZero] = round(out[lastNonZero] + diff)
		}
	}
	return out
}
