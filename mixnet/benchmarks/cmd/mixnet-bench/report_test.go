package main

import "testing"

func TestComparisonTableHelpers(t *testing.T) {
	baseBySize := map[int]summaryRecord{
		1024: summaryForComparison(1024, 100.0, 10.0),
	}
	headerBySize := map[int]summaryRecord{
		1024: summaryForComparison(1024, 80.0, 12.0),
	}
	fullBySize := map[int]summaryRecord{
		1024: summaryForComparison(1024, 130.0, 8.0),
	}
	sampleLookup := map[string]measurementSamples{
		measurementSampleKey("base", 1024): {
			TotalMS:        []float64{99, 100, 101},
			ThroughputMBps: []float64{9.5, 10.0, 10.5},
		},
		measurementSampleKey("header", 1024): {
			TotalMS:        []float64{79, 80, 81},
			ThroughputMBps: []float64{11.5, 12.0, 12.5},
		},
		measurementSampleKey("full", 1024): {
			TotalMS:        []float64{129, 130, 131},
			ThroughputMBps: []float64{7.5, 8.0, 8.5},
		},
	}

	latency := buildLatencyComparisonTableFromMaps(
		"latency",
		"description",
		baseBySize,
		headerBySize,
		fullBySize,
		sampleLookup,
		0.05,
		"base",
		"header",
		"full",
	)
	if latency.Title != "latency" {
		t.Fatalf("latency.Title = %q, want %q", latency.Title, "latency")
	}
	if len(latency.Headers) != 10 {
		t.Fatalf("latency.Headers length = %d, want 10", len(latency.Headers))
	}
	if len(latency.Rows) != 1 {
		t.Fatalf("latency.Rows length = %d, want 1", len(latency.Rows))
	}
	if got := latency.Rows[0][0]; got != formatBytes(1024) {
		t.Fatalf("latency size cell = %q, want %q", got, formatBytes(1024))
	}
	if got := latency.Rows[0][3]; got != "-20.00%" {
		t.Fatalf("header-only overhead cell = %q, want %q", got, "-20.00%")
	}
	if got := latency.Rows[0][6]; got != "30.00%" {
		t.Fatalf("full-onion overhead cell = %q, want %q", got, "30.00%")
	}
	if got := latency.Rows[0][8]; got != "62.50%" {
		t.Fatalf("full vs header-only cell = %q, want %q", got, "62.50%")
	}
	if got := latency.Rows[0][4]; got == "n/a" {
		t.Fatalf("expected latency significance cell, got %q", got)
	}

	throughput := buildThroughputComparisonTableFromMaps(
		"throughput",
		"description",
		baseBySize,
		headerBySize,
		fullBySize,
		sampleLookup,
		0.05,
		"base",
		"header",
		"full",
	)
	if throughput.Title != "throughput" {
		t.Fatalf("throughput.Title = %q, want %q", throughput.Title, "throughput")
	}
	if len(throughput.Rows) != 1 {
		t.Fatalf("throughput.Rows length = %d, want 1", len(throughput.Rows))
	}
	if got := throughput.Rows[0][1]; got != "10.000" {
		t.Fatalf("direct throughput cell = %q, want %q", got, "10.000")
	}
	if got := throughput.Rows[0][3]; got != "20.00%" {
		t.Fatalf("header-only throughput overhead cell = %q, want %q", got, "20.00%")
	}
	if got := throughput.Rows[0][6]; got != "-20.00%" {
		t.Fatalf("full-onion throughput overhead cell = %q, want %q", got, "-20.00%")
	}
	if got := throughput.Rows[0][4]; got == "n/a" {
		t.Fatalf("expected throughput significance cell, got %q", got)
	}
}

func TestComparisonTablesAreQuickOnly(t *testing.T) {
	if got := buildComparisonTables(suiteOptions{Profile: "full"}, nil, nil); got != nil {
		t.Fatalf("buildComparisonTables(full) = %v, want nil", got)
	}
}

func summaryForComparison(size int, totalMean, throughput float64) summaryRecord {
	return summaryRecord{
		SizeBytes:          size,
		SizeLabel:          formatBytes(size),
		TotalMeanMS:        totalMean,
		ThroughputMeanMBps: throughput,
		Label:              "test",
	}
}
