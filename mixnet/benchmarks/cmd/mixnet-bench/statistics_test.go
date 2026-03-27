package main

import (
	"math"
	"testing"
)

func TestSampleStatistics(t *testing.T) {
	values := []float64{1, 2, 3, 4}

	assertApproxEqual(t, sampleMean(values), 2.5, 1e-9, "sampleMean")
	assertApproxEqual(t, sampleVariance(values), 1.6666666666666667, 1e-9, "sampleVariance")
}

func TestWelchTTest(t *testing.T) {
	sampleA := []float64{10, 11, 9, 10, 10}
	sampleB := []float64{20, 19, 21, 20, 22}

	result := WelchTTest(sampleA, sampleB, 0.05)
	if !result.Valid {
		t.Fatalf("WelchTTest returned invalid result: %+v", result)
	}
	if result.PValue >= 0.05 {
		t.Fatalf("WelchTTest p-value = %.6f, want < 0.05", result.PValue)
	}
	if !result.Significant {
		t.Fatalf("WelchTTest marked insignificant for clearly separated samples: %+v", result)
	}
	if result.TStatistic >= 0 {
		t.Fatalf("WelchTTest t-statistic = %.6f, want negative because sampleA < sampleB", result.TStatistic)
	}
	if result.EffectSizeLabel != "large" {
		t.Fatalf("WelchTTest effect label = %q, want large", result.EffectSizeLabel)
	}
}

func TestWelchTTestIdenticalSamples(t *testing.T) {
	result := WelchTTest([]float64{2, 3, 4}, []float64{2, 3, 4}, 0.05)
	if !result.Valid {
		t.Fatalf("WelchTTest returned invalid result for identical samples: %+v", result)
	}
	if result.PValue != 1 {
		t.Fatalf("WelchTTest identical samples p-value = %.6f, want 1", result.PValue)
	}
	if result.Significant {
		t.Fatalf("WelchTTest identical samples should not be significant: %+v", result)
	}
}

func TestEffectSizeLabel(t *testing.T) {
	cases := map[float64]string{
		0.19: "negligible",
		0.4:  "small",
		0.7:  "medium",
		1.5:  "large",
	}
	for input, want := range cases {
		if got := effectSizeLabel(input); got != want {
			t.Fatalf("effectSizeLabel(%.2f) = %q, want %q", input, got, want)
		}
	}
}

func assertApproxEqual(t *testing.T, got, want, tol float64, name string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %.12f, want %.12f (tol %.12f)", name, got, want, tol)
	}
}
