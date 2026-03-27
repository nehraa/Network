package main

import (
	"math"

	"gonum.org/v1/gonum/stat/distuv"
)

const defaultSignificanceAlpha = 0.05

// WelchTTestResult captures the pairwise statistical comparison between two samples.
type WelchTTestResult struct {
	Valid            bool    `json:"valid"`
	SampleCountA     int     `json:"sample_count_a"`
	SampleCountB     int     `json:"sample_count_b"`
	MeanA            float64 `json:"mean_a"`
	MeanB            float64 `json:"mean_b"`
	TStatistic       float64 `json:"t_statistic"`
	DegreesOfFreedom float64 `json:"degrees_of_freedom"`
	PValue           float64 `json:"p_value"`
	Significant      bool    `json:"significant"`
	EffectSize       float64 `json:"effect_size"`
	EffectSizeLabel  string  `json:"effect_size_label"`
}

// WelchTTest compares two independent samples using Welch's t-test.
func WelchTTest(sampleA, sampleB []float64, alpha float64) WelchTTestResult {
	result := WelchTTestResult{
		SampleCountA: len(sampleA),
		SampleCountB: len(sampleB),
		MeanA:        sampleMean(sampleA),
		MeanB:        sampleMean(sampleB),
	}
	alpha = normalizeAlpha(alpha)
	if len(sampleA) < 2 || len(sampleB) < 2 {
		return result
	}

	varA := sampleVariance(sampleA)
	varB := sampleVariance(sampleB)
	nA := float64(len(sampleA))
	nB := float64(len(sampleB))
	seSquared := varA/nA + varB/nB
	if seSquared <= 0 {
		return result
	}

	tStatistic := (result.MeanA - result.MeanB) / math.Sqrt(seSquared)
	dfDenominator := math.Pow(varA/nA, 2)/(nA-1) + math.Pow(varB/nB, 2)/(nB-1)
	if dfDenominator <= 0 {
		return result
	}

	degreesOfFreedom := math.Pow(seSquared, 2) / dfDenominator
	if degreesOfFreedom <= 0 || math.IsNaN(degreesOfFreedom) || math.IsInf(degreesOfFreedom, 0) {
		return result
	}

	dist := distuv.StudentsT{Mu: 0, Sigma: 1, Nu: degreesOfFreedom}
	pValue := 2 * (1 - dist.CDF(math.Abs(tStatistic)))
	if pValue < 0 {
		pValue = 0
	}
	if pValue > 1 {
		pValue = 1
	}

	effectSize := cohenD(sampleA, sampleB)
	result.Valid = true
	result.TStatistic = tStatistic
	result.DegreesOfFreedom = degreesOfFreedom
	result.PValue = pValue
	result.Significant = pValue < alpha
	result.EffectSize = effectSize
	result.EffectSizeLabel = effectSizeLabel(effectSize)
	return result
}

func normalizeAlpha(alpha float64) float64 {
	if alpha <= 0 || alpha >= 1 {
		return defaultSignificanceAlpha
	}
	return alpha
}

func sampleMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func sampleVariance(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	mean := sampleMean(values)
	sumSquares := 0.0
	for _, value := range values {
		diff := value - mean
		sumSquares += diff * diff
	}
	return sumSquares / float64(len(values)-1)
}

func cohenD(sampleA, sampleB []float64) float64 {
	pooledVariance := (sampleVariance(sampleA) + sampleVariance(sampleB)) / 2
	if pooledVariance <= 0 {
		return 0
	}
	return (sampleMean(sampleA) - sampleMean(sampleB)) / math.Sqrt(pooledVariance)
}

func effectSizeLabel(effectSize float64) string {
	absolute := math.Abs(effectSize)
	switch {
	case absolute < 0.2:
		return "negligible"
	case absolute < 0.5:
		return "small"
	case absolute < 0.8:
		return "medium"
	default:
		return "large"
	}
}
