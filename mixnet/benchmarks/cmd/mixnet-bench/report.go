package main

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type chartSeries struct {
	Name   string
	Color  string
	Points []summaryRecord
	Value  func(summaryRecord) float64
}

type reportData struct {
	Profile     string
	Network     string
	Outlier     string
	Adjustment  string
	Graphs      []reportGraph
	Best        []bestRecord
	Summaries   []summaryRecord
	Comparisons []comparisonTable
}

type reportGraph struct {
	Title string
	File  string
}

type comparisonTable struct {
	Title       string
	Description string
	Headers     []string
	Rows        [][]string
}

type measurementSamples struct {
	TotalMS        []float64
	ThroughputMBps []float64
}

func writeReport(outputDir string, opts suiteOptions, runRecords []*runRecord, summaries []summaryRecord, best []bestRecord) error {
	graphDir := filepath.Join(outputDir, "graphs")
	if err := os.MkdirAll(graphDir, 0o755); err != nil {
		return err
	}

	lookup := make(map[string][]summaryRecord)
	for _, summary := range summaries {
		lookup[summary.ScenarioID] = append(lookup[summary.ScenarioID], summary)
	}
	sampleLookup := buildMeasurementSamples(runRecords)

	graphs := make([]reportGraph, 0, 18)
	addChart := func(title, file, yLabel string, chartData []chartSeries, value func(summaryRecord) float64) error {
		if !hasChartData(chartData) {
			return nil
		}
		if err := writeSVGLineChart(filepath.Join(outputDir, file), title, yLabel, chartData, value); err != nil {
			return err
		}
		graphs = append(graphs, reportGraph{Title: title, File: file})
		return nil
	}

	if opts.Profile == "quick" {
		if err := addChart("Quick routed stream latency (256KB writes, 2 hops, 1 circuit)", "graphs/quick_c1_latency.svg", "Mean total latency (ms)",
			[]chartSeries{
				rawChartSeries("Direct stream", "#1f77b4", lookup, "focused-direct-baseline"),
				rawChartSeries("Header-only routed", "#2ca02c", lookup, "focused-header-only-c1-routed"),
				rawChartSeries("Full onion legacy", "#9467bd", lookup, "focused-full-c1-legacy"),
			},
			func(summary summaryRecord) float64 { return summary.TotalMeanMS },
		); err != nil {
			return err
		}
		if err := addChart("Quick routed stream throughput (256KB writes, 2 hops, 1 circuit)", "graphs/quick_c1_throughput.svg", "Mean throughput (MiB/s)",
			[]chartSeries{
				rawChartSeries("Direct stream", "#1f77b4", lookup, "focused-direct-baseline"),
				rawChartSeries("Header-only routed", "#2ca02c", lookup, "focused-header-only-c1-routed"),
				rawChartSeries("Full onion legacy", "#9467bd", lookup, "focused-full-c1-legacy"),
			},
			func(summary summaryRecord) float64 { return summary.ThroughputMeanMBps },
		); err != nil {
			return err
		}
	}

	adjustmentNote := ""
	if opts.Profile == "full" {
		adjustmentNote = "Full runs do not generate graphs. Use raw_runs.csv and summary.csv for the full data dump, including CES scenarios."
	} else if opts.Profile == "quick" {
		adjustmentNote = "Quick uses fixed 256KB application writes for the main routed stream sweep and also includes bitrate-shaped audio and video stream presets. The base comparison is direct libp2p against header-only mixnet with session routing enabled and full-onion mixnet on the legacy per-frame path at 2 hops and 1 circuit. Media presets use a fixed virtual stream duration and bitrate-derived write sizes; they are not real-time paced."
	}
	adjustments := make([]string, 0, 2)
	if adjustmentNote != "" {
		adjustments = append(adjustments, adjustmentNote)
	}
	adjustments = append(adjustments, fmt.Sprintf("Warmup uses %d unrecorded runs per scenario and size. Statistical comparisons use Welch's t-test on the kept measured runs at alpha %.3f.", opts.WarmupRuns, opts.SignificanceAlpha))

	data := reportData{
		Profile:     opts.Profile,
		Network:     opts.NetworkCondition.Summary(),
		Outlier:     fmt.Sprintf("For each scenario and size, %d raw runs are recorded and the benchmark applies this trim rule before computing averages and standard deviation: %s.", opts.Runs, describeOutlierRuleWithOverrides(opts.Runs, opts.SizeRunOverrides)),
		Adjustment:  strings.Join(adjustments, " "),
		Graphs:      graphs,
		Best:        best,
		Summaries:   compactSummaries(summaries),
		Comparisons: buildComparisonTables(opts, lookup, sampleLookup),
	}

	const reportTemplate = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Mixnet benchmark report</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 24px; color: #111827; background: #f9fafb; }
    h1, h2 { color: #111827; }
    p, li { line-height: 1.5; }
    .card { background: white; border: 1px solid #e5e7eb; border-radius: 12px; padding: 16px; margin-bottom: 20px; box-shadow: 0 1px 2px rgba(0,0,0,0.04); }
    img { max-width: 100%; border: 1px solid #e5e7eb; background: white; }
    table { border-collapse: collapse; width: 100%; background: white; }
    th, td { border: 1px solid #e5e7eb; padding: 8px 10px; text-align: left; font-size: 14px; }
    th { background: #f3f4f6; }
    code { background: #f3f4f6; padding: 2px 4px; border-radius: 4px; }
  </style>
</head>
<body>
  <h1>Mixnet benchmark report</h1>
  <div class="card">
    <p><strong>Profile:</strong> {{.Profile}}</p>
    <p><strong>Network:</strong> {{.Network}}</p>
    <p>{{.Outlier}}</p>
    {{if .Adjustment}}<p>{{.Adjustment}}</p>{{end}}
    <p>Raw files: <code>raw_runs.csv</code>, <code>raw_runs.jsonl</code>. Aggregates: <code>summary.csv</code>, <code>summary.json</code>, <code>best_hops_circuits.csv</code>.</p>
  </div>

  <div class="card">
    <h2>Best hop x circuit combinations</h2>
    <table>
      <thead>
        <tr><th>Mode</th><th>Size</th><th>Scenario</th><th>Hops</th><th>Circuits</th><th>Mean total ms</th><th>Mean MiB/s</th><th>Mean key exchange ms</th></tr>
      </thead>
      <tbody>
      {{range .Best}}
        <tr>
          <td>{{.Mode}}</td>
          <td>{{.SizeLabel}}</td>
          <td>{{.Label}}</td>
          <td>{{.HopCount}}</td>
          <td>{{.CircuitCount}}</td>
          <td>{{printf "%.3f" .TotalMeanMS}}</td>
          <td>{{printf "%.3f" .ThroughputMBps}}</td>
          <td>{{printf "%.3f" .KeyExchangeMean}}</td>
        </tr>
      {{end}}
      </tbody>
    </table>
  </div>

  {{range .Graphs}}
  <div class="card">
    <h2>{{.Title}}</h2>
    <img src="{{.File}}" alt="{{.Title}}">
  </div>
  {{end}}

  {{range .Comparisons}}
  <div class="card">
    <h2>{{.Title}}</h2>
    {{if .Description}}<p>{{.Description}}</p>{{end}}
    <table>
      <thead>
        <tr>{{range .Headers}}<th>{{.}}</th>{{end}}</tr>
      </thead>
      <tbody>
      {{range .Rows}}
        <tr>
          {{range .}}<td>{{.}}</td>{{end}}
        </tr>
      {{end}}
      </tbody>
    </table>
  </div>
  {{end}}

  <div class="card">
    <h2>Summary sample</h2>
    <table>
      <thead>
        <tr><th>Scenario</th><th>Size</th><th>Mean total ms</th><th>Stddev ms</th><th>Mean key exchange ms</th><th>Mean transfer ms</th><th>Mean MiB/s</th></tr>
      </thead>
      <tbody>
      {{range .Summaries}}
        <tr>
          <td>{{.Label}}</td>
          <td>{{.SizeLabel}}</td>
          <td>{{printf "%.3f" .TotalMeanMS}}</td>
          <td>{{printf "%.3f" .TotalStdDevMS}}</td>
          <td>{{printf "%.3f" .KeyExchangeMeanMS}}</td>
          <td>{{printf "%.3f" .TransferMeanMS}}</td>
          <td>{{printf "%.3f" .ThroughputMeanMBps}}</td>
        </tr>
      {{end}}
      </tbody>
    </table>
  </div>
</body>
</html>`

	tmpl, err := template.New("report").Parse(reportTemplate)
	if err != nil {
		return err
	}
	file, err := os.Create(filepath.Join(outputDir, "report.html"))
	if err != nil {
		return err
	}
	defer file.Close()
	return tmpl.Execute(file, data)
}

func hasChartData(items []chartSeries) bool {
	for _, item := range items {
		if len(item.Points) > 0 {
			return true
		}
	}
	return false
}

func sortedPointsByID(lookup map[string][]summaryRecord, id string) []summaryRecord {
	points := append([]summaryRecord(nil), lookup[id]...)
	sort.Slice(points, func(i, j int) bool { return points[i].SizeBytes < points[j].SizeBytes })
	return points
}

func singleSummaryByID(lookup map[string][]summaryRecord, id string) (summaryRecord, bool) {
	points := sortedPointsByID(lookup, id)
	if len(points) == 0 {
		return summaryRecord{}, false
	}
	return points[0], true
}

func summaryMapBySize(points []summaryRecord) map[int]summaryRecord {
	out := make(map[int]summaryRecord, len(points))
	for _, point := range points {
		out[point.SizeBytes] = point
	}
	return out
}

func rawChartSeries(name, color string, lookup map[string][]summaryRecord, scenarioID string) chartSeries {
	return chartSeries{
		Name:   name,
		Color:  color,
		Points: sortedPointsByID(lookup, scenarioID),
	}
}

func compactSummaries(summaries []summaryRecord) []summaryRecord {
	if len(summaries) <= 18 {
		return summaries
	}
	out := make([]summaryRecord, 0, 18)
	step := len(summaries) / 18
	if step < 1 {
		step = 1
	}
	for i := 0; i < len(summaries) && len(out) < 18; i += step {
		out = append(out, summaries[i])
	}
	return out
}

func buildMeasurementSamples(records []*runRecord) map[string]measurementSamples {
	lookup := make(map[string]measurementSamples)
	for _, record := range records {
		if record == nil || record.Error != "" || record.Excluded {
			continue
		}
		key := measurementSampleKey(record.ScenarioID, record.SizeBytes)
		samples := lookup[key]
		samples.TotalMS = append(samples.TotalMS, record.TotalMS)
		samples.ThroughputMBps = append(samples.ThroughputMBps, record.ThroughputMBps)
		lookup[key] = samples
	}
	return lookup
}

func measurementSampleKey(scenarioID string, sizeBytes int) string {
	return scenarioID + "|" + strconv.Itoa(sizeBytes)
}

func compareLatencySamples(sampleLookup map[string]measurementSamples, baselineID, candidateID string, sizeBytes int, alpha float64) WelchTTestResult {
	baseline := sampleLookup[measurementSampleKey(baselineID, sizeBytes)]
	candidate := sampleLookup[measurementSampleKey(candidateID, sizeBytes)]
	return WelchTTest(candidate.TotalMS, baseline.TotalMS, alpha)
}

func compareThroughputSamples(sampleLookup map[string]measurementSamples, baselineID, candidateID string, sizeBytes int, alpha float64) WelchTTestResult {
	baseline := sampleLookup[measurementSampleKey(baselineID, sizeBytes)]
	candidate := sampleLookup[measurementSampleKey(candidateID, sizeBytes)]
	return WelchTTest(candidate.ThroughputMBps, baseline.ThroughputMBps, alpha)
}

func formatSignificance(result WelchTTestResult) string {
	if !result.Valid {
		return "n/a"
	}
	if result.PValue < 0.001 {
		return fmt.Sprintf("<0.001 (%s)", result.EffectSizeLabel)
	}
	return fmt.Sprintf("%.3f (%s)", result.PValue, result.EffectSizeLabel)
}

func buildComparisonTables(opts suiteOptions, lookup map[string][]summaryRecord, sampleLookup map[string]measurementSamples) []comparisonTable {
	if opts.Profile != "quick" {
		return nil
	}
	tables := []comparisonTable{
		buildLatencyComparisonTable(
			lookup,
			sampleLookup,
			opts.SignificanceAlpha,
			"Quick routed stream latency comparison: 2 hops, 1 circuit",
			"Exact mean total latency and percent overhead for header-only routed versus direct and full-onion legacy.",
			"focused-direct-baseline",
			"focused-header-only-c1-routed",
			"focused-full-c1-legacy",
		),
		buildThroughputComparisonTable(
			lookup,
			sampleLookup,
			opts.SignificanceAlpha,
			"Quick routed stream throughput comparison: 2 hops, 1 circuit",
			"Exact mean throughput and percent delta for header-only routed versus direct and full-onion legacy.",
			"focused-direct-baseline",
			"focused-header-only-c1-routed",
			"focused-full-c1-legacy",
		),
	}
	tables = append(tables, buildQuickMediaTables(lookup, sampleLookup, opts.SignificanceAlpha)...)
	return tables
}

func buildQuickMediaTables(lookup map[string][]summaryRecord, sampleLookup map[string]measurementSamples, alpha float64) []comparisonTable {
	return []comparisonTable{
		buildMediaLatencyComparisonTable(lookup, sampleLookup, alpha, "audio"),
		buildMediaThroughputComparisonTable(lookup, sampleLookup, alpha, "audio"),
		buildMediaLatencyComparisonTable(lookup, sampleLookup, alpha, "video"),
		buildMediaThroughputComparisonTable(lookup, sampleLookup, alpha, "video"),
	}
}

func buildLatencyComparisonTable(lookup map[string][]summaryRecord, sampleLookup map[string]measurementSamples, alpha float64, title, description, baselineID, headerID, fullID string) comparisonTable {
	return buildLatencyComparisonTableFromMaps(
		title,
		description,
		summaryMapBySize(sortedPointsByID(lookup, baselineID)),
		summaryMapBySize(sortedPointsByID(lookup, headerID)),
		summaryMapBySize(sortedPointsByID(lookup, fullID)),
		sampleLookup,
		alpha,
		baselineID,
		headerID,
		fullID,
	)
}

func buildLatencyComparisonTableFromMaps(title, description string, baseBySize, headerBySize, fullBySize map[int]summaryRecord, sampleLookup map[string]measurementSamples, alpha float64, baselineID, headerID, fullID string) comparisonTable {
	sizes := make([]int, 0, len(baseBySize))
	for size := range baseBySize {
		if _, ok := headerBySize[size]; !ok {
			continue
		}
		if _, ok := fullBySize[size]; !ok {
			continue
		}
		sizes = append(sizes, size)
	}
	sort.Ints(sizes)

	rows := make([][]string, 0, len(sizes))
	for _, size := range sizes {
		base := baseBySize[size]
		header := headerBySize[size]
		full := fullBySize[size]
		headerStats := compareLatencySamples(sampleLookup, baselineID, headerID, size, alpha)
		fullStats := compareLatencySamples(sampleLookup, baselineID, fullID, size, alpha)
		fullVsHeaderStats := compareLatencySamples(sampleLookup, headerID, fullID, size, alpha)
		rows = append(rows, []string{
			base.SizeLabel,
			fmt.Sprintf("%.3f", base.TotalMeanMS),
			fmt.Sprintf("%.3f", header.TotalMeanMS),
			fmt.Sprintf("%.2f%%", percentDelta(header.TotalMeanMS, base.TotalMeanMS)),
			formatSignificance(headerStats),
			fmt.Sprintf("%.3f", full.TotalMeanMS),
			fmt.Sprintf("%.2f%%", percentDelta(full.TotalMeanMS, base.TotalMeanMS)),
			formatSignificance(fullStats),
			fmt.Sprintf("%.2f%%", percentDelta(full.TotalMeanMS, header.TotalMeanMS)),
			formatSignificance(fullVsHeaderStats),
		})
	}

	return comparisonTable{
		Title:       title,
		Description: description,
		Headers: []string{
			"Size",
			"Direct ms",
			"Header-only ms",
			"Header-only overhead vs direct %",
			"Header-only p vs direct",
			"Full onion ms",
			"Full onion overhead vs direct %",
			"Full onion p vs direct",
			"Full vs header-only %",
			"Full vs header-only p",
		},
		Rows: rows,
	}
}

func buildThroughputComparisonTable(lookup map[string][]summaryRecord, sampleLookup map[string]measurementSamples, alpha float64, title, description, baselineID, headerID, fullID string) comparisonTable {
	return buildThroughputComparisonTableFromMaps(
		title,
		description,
		summaryMapBySize(sortedPointsByID(lookup, baselineID)),
		summaryMapBySize(sortedPointsByID(lookup, headerID)),
		summaryMapBySize(sortedPointsByID(lookup, fullID)),
		sampleLookup,
		alpha,
		baselineID,
		headerID,
		fullID,
	)
}

func buildThroughputComparisonTableFromMaps(title, description string, baseBySize, headerBySize, fullBySize map[int]summaryRecord, sampleLookup map[string]measurementSamples, alpha float64, baselineID, headerID, fullID string) comparisonTable {
	sizes := make([]int, 0, len(baseBySize))
	for size := range baseBySize {
		if _, ok := headerBySize[size]; !ok {
			continue
		}
		if _, ok := fullBySize[size]; !ok {
			continue
		}
		sizes = append(sizes, size)
	}
	sort.Ints(sizes)

	rows := make([][]string, 0, len(sizes))
	for _, size := range sizes {
		base := baseBySize[size]
		header := headerBySize[size]
		full := fullBySize[size]
		headerStats := compareThroughputSamples(sampleLookup, baselineID, headerID, size, alpha)
		fullStats := compareThroughputSamples(sampleLookup, baselineID, fullID, size, alpha)
		fullVsHeaderStats := compareThroughputSamples(sampleLookup, headerID, fullID, size, alpha)
		rows = append(rows, []string{
			base.SizeLabel,
			fmt.Sprintf("%.3f", base.ThroughputMeanMBps),
			fmt.Sprintf("%.3f", header.ThroughputMeanMBps),
			fmt.Sprintf("%.2f%%", percentDelta(header.ThroughputMeanMBps, base.ThroughputMeanMBps)),
			formatSignificance(headerStats),
			fmt.Sprintf("%.3f", full.ThroughputMeanMBps),
			fmt.Sprintf("%.2f%%", percentDelta(full.ThroughputMeanMBps, base.ThroughputMeanMBps)),
			formatSignificance(fullStats),
			fmt.Sprintf("%.2f%%", percentDelta(full.ThroughputMeanMBps, header.ThroughputMeanMBps)),
			formatSignificance(fullVsHeaderStats),
		})
	}

	return comparisonTable{
		Title:       title,
		Description: description,
		Headers: []string{
			"Size",
			"Direct MiB/s",
			"Header-only MiB/s",
			"Header-only vs direct %",
			"Header-only p vs direct",
			"Full onion MiB/s",
			"Full onion vs direct %",
			"Full onion p vs direct",
			"Full vs header-only %",
			"Full vs header-only p",
		},
		Rows: rows,
	}
}

func buildMediaLatencyComparisonTable(lookup map[string][]summaryRecord, sampleLookup map[string]measurementSamples, alpha float64, kind string) comparisonTable {
	rows := make([][]string, 0)
	for _, profile := range quickMediaProfiles {
		if profile.Kind != kind {
			continue
		}
		base, ok := singleSummaryByID(lookup, quickMediaScenarioID(profile, "direct"))
		if !ok {
			continue
		}
		header, ok := singleSummaryByID(lookup, quickMediaScenarioID(profile, "header-routed"))
		if !ok {
			continue
		}
		full, ok := singleSummaryByID(lookup, quickMediaScenarioID(profile, "full-legacy"))
		if !ok {
			continue
		}
		headerStats := compareLatencySamples(sampleLookup, quickMediaScenarioID(profile, "direct"), quickMediaScenarioID(profile, "header-routed"), base.SizeBytes, alpha)
		fullStats := compareLatencySamples(sampleLookup, quickMediaScenarioID(profile, "direct"), quickMediaScenarioID(profile, "full-legacy"), base.SizeBytes, alpha)
		fullVsHeaderStats := compareLatencySamples(sampleLookup, quickMediaScenarioID(profile, "header-routed"), quickMediaScenarioID(profile, "full-legacy"), base.SizeBytes, alpha)
		rows = append(rows, []string{
			profile.Quality,
			strconv.Itoa(profile.BitrateKbps),
			strconv.Itoa(base.StreamSegmentMS),
			formatBytes(base.StreamWriteSizeBytes),
			base.SizeLabel,
			fmt.Sprintf("%.3f", base.TotalMeanMS),
			fmt.Sprintf("%.3f", header.TotalMeanMS),
			fmt.Sprintf("%.2f%%", percentDelta(header.TotalMeanMS, base.TotalMeanMS)),
			formatSignificance(headerStats),
			fmt.Sprintf("%.3f", full.TotalMeanMS),
			fmt.Sprintf("%.2f%%", percentDelta(full.TotalMeanMS, base.TotalMeanMS)),
			formatSignificance(fullStats),
			fmt.Sprintf("%.2f%%", percentDelta(full.TotalMeanMS, header.TotalMeanMS)),
			formatSignificance(fullVsHeaderStats),
		})
	}
	titleKind := displayKind(kind)
	return comparisonTable{
		Title:       fmt.Sprintf("Quick %s stream latency presets", titleKind),
		Description: fmt.Sprintf("%s stream presets use bitrate-shaped write sizes over a fixed %d second virtual stream.", titleKind, mediaDurationForKind(kind)),
		Headers: []string{
			"Quality",
			"Bitrate kbps",
			"Segment ms",
			"Write size",
			"Payload size",
			"Direct ms",
			"Header-only ms",
			"Header-only overhead vs direct %",
			"Header-only p vs direct",
			"Full onion ms",
			"Full onion overhead vs direct %",
			"Full onion p vs direct",
			"Full vs header-only %",
			"Full vs header-only p",
		},
		Rows: rows,
	}
}

func buildMediaThroughputComparisonTable(lookup map[string][]summaryRecord, sampleLookup map[string]measurementSamples, alpha float64, kind string) comparisonTable {
	rows := make([][]string, 0)
	for _, profile := range quickMediaProfiles {
		if profile.Kind != kind {
			continue
		}
		base, ok := singleSummaryByID(lookup, quickMediaScenarioID(profile, "direct"))
		if !ok {
			continue
		}
		header, ok := singleSummaryByID(lookup, quickMediaScenarioID(profile, "header-routed"))
		if !ok {
			continue
		}
		full, ok := singleSummaryByID(lookup, quickMediaScenarioID(profile, "full-legacy"))
		if !ok {
			continue
		}
		headerStats := compareThroughputSamples(sampleLookup, quickMediaScenarioID(profile, "direct"), quickMediaScenarioID(profile, "header-routed"), base.SizeBytes, alpha)
		fullStats := compareThroughputSamples(sampleLookup, quickMediaScenarioID(profile, "direct"), quickMediaScenarioID(profile, "full-legacy"), base.SizeBytes, alpha)
		fullVsHeaderStats := compareThroughputSamples(sampleLookup, quickMediaScenarioID(profile, "header-routed"), quickMediaScenarioID(profile, "full-legacy"), base.SizeBytes, alpha)
		rows = append(rows, []string{
			profile.Quality,
			strconv.Itoa(profile.BitrateKbps),
			strconv.Itoa(base.StreamSegmentMS),
			formatBytes(base.StreamWriteSizeBytes),
			base.SizeLabel,
			fmt.Sprintf("%.3f", base.ThroughputMeanMBps),
			fmt.Sprintf("%.3f", header.ThroughputMeanMBps),
			fmt.Sprintf("%.2f%%", percentDelta(header.ThroughputMeanMBps, base.ThroughputMeanMBps)),
			formatSignificance(headerStats),
			fmt.Sprintf("%.3f", full.ThroughputMeanMBps),
			fmt.Sprintf("%.2f%%", percentDelta(full.ThroughputMeanMBps, base.ThroughputMeanMBps)),
			formatSignificance(fullStats),
			fmt.Sprintf("%.2f%%", percentDelta(full.ThroughputMeanMBps, header.ThroughputMeanMBps)),
			formatSignificance(fullVsHeaderStats),
		})
	}
	titleKind := displayKind(kind)
	return comparisonTable{
		Title:       fmt.Sprintf("Quick %s stream throughput presets", titleKind),
		Description: fmt.Sprintf("%s stream presets use bitrate-shaped write sizes over a fixed %d second virtual stream.", titleKind, mediaDurationForKind(kind)),
		Headers: []string{
			"Quality",
			"Bitrate kbps",
			"Segment ms",
			"Write size",
			"Payload size",
			"Direct MiB/s",
			"Header-only MiB/s",
			"Header-only vs direct %",
			"Header-only p vs direct",
			"Full onion MiB/s",
			"Full onion vs direct %",
			"Full onion p vs direct",
			"Full vs header-only %",
			"Full vs header-only p",
		},
		Rows: rows,
	}
}

func mediaDurationForKind(kind string) int {
	for _, profile := range quickMediaProfiles {
		if profile.Kind == kind {
			return profile.DurationSec
		}
	}
	return 0
}

func displayKind(kind string) string {
	if kind == "" {
		return ""
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}

func percentDelta(value, baseline float64) float64 {
	if baseline == 0 {
		return 0
	}
	return ((value - baseline) / baseline) * 100.0
}

func writeSVGLineChart(path, title, yLabel string, chartData []chartSeries, value func(summaryRecord) float64) error {
	series := make([]chartSeries, 0, len(chartData))
	sizeSet := make(map[int]string)
	minY := 0.0
	maxY := 0.0
	hasValues := false
	for _, item := range chartData {
		if len(item.Points) == 0 {
			continue
		}
		for _, point := range item.Points {
			sizeSet[point.SizeBytes] = point.SizeLabel
			v := chooseValue(item, point, value)
			if !hasValues {
				minY = v
				maxY = v
				hasValues = true
				continue
			}
			if v < minY {
				minY = v
			}
			if v > maxY {
				maxY = v
			}
		}
		series = append(series, item)
	}
	if len(series) == 0 {
		return os.WriteFile(path, []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="960" height="120"><text x="20" y="60" font-family="sans-serif" font-size="18">No data for chart</text></svg>`), 0o644)
	}

	sizes := make([]int, 0, len(sizeSet))
	for size := range sizeSet {
		sizes = append(sizes, size)
	}
	sort.Ints(sizes)
	if !hasValues {
		minY = 0
		maxY = 1
	} else {
		if minY >= 0 {
			minY = 0
		} else {
			minY *= 1.1
		}
		if maxY <= 0 {
			maxY = 1
		} else {
			maxY *= 1.1
		}
		if maxY <= minY {
			maxY = minY + 1
		}
	}

	const (
		width  = 960
		height = 560
		left   = 90
		right  = 30
		top    = 40
		bottom = 90
	)
	plotWidth := float64(width - left - right)
	plotHeight := float64(height - top - bottom)

	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`, width, height, width, height))
	b.WriteString(`<rect width="100%" height="100%" fill="#ffffff"/>`)
	b.WriteString(fmt.Sprintf(`<text x="%d" y="24" font-family="sans-serif" font-size="20" font-weight="600">%s</text>`, left, template.HTMLEscapeString(title)))
	b.WriteString(fmt.Sprintf(`<text x="24" y="%d" font-family="sans-serif" font-size="14" transform="rotate(-90 24,%d)">%s</text>`, top+int(plotHeight/2), top+int(plotHeight/2), template.HTMLEscapeString(yLabel)))

	for i := 0; i <= 5; i++ {
		yValue := maxY - (maxY-minY)*float64(i)/5.0
		y := float64(top) + plotHeight*float64(i)/5.0
		b.WriteString(fmt.Sprintf(`<line x1="%d" y1="%.2f" x2="%d" y2="%.2f" stroke="#e5e7eb" stroke-width="1"/>`, left, y, width-right, y))
		b.WriteString(fmt.Sprintf(`<text x="%d" y="%.2f" font-family="sans-serif" font-size="12" fill="#4b5563">%.1f</text>`, 16, y+4, yValue))
	}
	if minY < 0 && maxY > 0 {
		zeroY := float64(top) + plotHeight*(maxY/(maxY-minY))
		b.WriteString(fmt.Sprintf(`<line x1="%d" y1="%.2f" x2="%d" y2="%.2f" stroke="#9ca3af" stroke-width="1.5"/>`, left, zeroY, width-right, zeroY))
	}

	for idx, size := range sizes {
		x := float64(left)
		if len(sizes) > 1 {
			x += plotWidth * float64(idx) / float64(len(sizes)-1)
		}
		b.WriteString(fmt.Sprintf(`<line x1="%.2f" y1="%d" x2="%.2f" y2="%d" stroke="#f3f4f6" stroke-width="1"/>`, x, top, x, height-bottom))
		b.WriteString(fmt.Sprintf(`<text x="%.2f" y="%d" font-family="sans-serif" font-size="12" fill="#4b5563" text-anchor="middle">%s</text>`, x, height-45, template.HTMLEscapeString(sizeSet[size])))
	}

	legendY := height - 20
	legendX := left
	for _, item := range series {
		var pathData strings.Builder
		for idx, point := range item.Points {
			x := float64(left)
			position := indexOfSize(sizes, point.SizeBytes)
			if len(sizes) > 1 {
				x += plotWidth * float64(position) / float64(len(sizes)-1)
			}
			v := chooseValue(item, point, value)
			y := float64(top) + plotHeight*((maxY-v)/(maxY-minY))
			cmd := "L"
			if idx == 0 {
				cmd = "M"
			}
			pathData.WriteString(fmt.Sprintf("%s %.2f %.2f ", cmd, x, y))
			b.WriteString(fmt.Sprintf(`<circle cx="%.2f" cy="%.2f" r="3.5" fill="%s"/>`, x, y, item.Color))
		}
		b.WriteString(fmt.Sprintf(`<path d="%s" fill="none" stroke="%s" stroke-width="2.5"/>`, strings.TrimSpace(pathData.String()), item.Color))
		b.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="12" height="12" fill="%s"/>`, legendX, legendY-11, item.Color))
		b.WriteString(fmt.Sprintf(`<text x="%d" y="%d" font-family="sans-serif" font-size="12">%s</text>`, legendX+18, legendY, template.HTMLEscapeString(item.Name)))
		legendX += 18 + len(item.Name)*7 + 20
		if legendX > width-180 {
			legendX = left
			legendY -= 18
		}
	}

	b.WriteString(`</svg>`)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func chooseValue(item chartSeries, summary summaryRecord, fallback func(summaryRecord) float64) float64 {
	if item.Value != nil {
		return item.Value(summary)
	}
	return fallback(summary)
}

func indexOfSize(sizes []int, want int) int {
	for i, size := range sizes {
		if size == want {
			return i
		}
	}
	return 0
}
