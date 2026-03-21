package revenuecat

import (
	"encoding/json"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"
)

// rcOverviewResponse matches the RevenueCat v2 API response for
// GET /v2/projects/{project_id}/metrics/overview.
type rcOverviewResponse struct {
	Object  string             `json:"object"`
	Metrics []rcOverviewMetric `json:"metrics"`
}

type rcOverviewMetric struct {
	Object               string  `json:"object"`
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	Description          string  `json:"description"`
	Unit                 string  `json:"unit"`
	Period               string  `json:"period"`
	Value                float64 `json:"value"`
	LastUpdatedAt        *int64  `json:"last_updated_at"`
	LastUpdatedAtISO8601 *string `json:"last_updated_at_iso8601"`
}

// OverviewMetrics is the simplified output returned by the admin endpoint.
type OverviewMetrics struct {
	MRR               int `json:"mrr"`
	ARR               int `json:"arr"`
	ActiveSubscribers int `json:"active_subscribers"`
	ActiveTrials      int `json:"active_trials"`
}

// transformOverview converts the raw RC response into our simplified metrics.
func transformOverview(raw rcOverviewResponse) OverviewMetrics {
	var out OverviewMetrics
	for _, m := range raw.Metrics {
		switch m.ID {
		case "mrr":
			out.MRR = dollarsToTruncatedCents(m.Value)
		case "arr":
			out.ARR = dollarsToTruncatedCents(m.Value)
		case "active_subscribers":
			out.ActiveSubscribers = int(m.Value)
		case "active_trials":
			out.ActiveTrials = int(m.Value)
		}
	}
	return out
}

func dollarsToTruncatedCents(dollars float64) int {
	return int(math.Round(dollars * 100))
}

// ---------------------------------------------------------------------------
// Chart types
// ---------------------------------------------------------------------------

// ChartParams holds the parameters for a chart request.
type ChartParams struct {
	ChartName string
	StartDate string // YYYY-MM-DD
	EndDate   string // YYYY-MM-DD
}

// ChartPoint is a single data point in a chart time series.
type ChartPoint struct {
	Date  string `json:"date"`
	Value int    `json:"value"`
}

// ChartResponse is the simplified chart output returned by the admin endpoint.
type ChartResponse struct {
	Name string       `json:"name"`
	Data []ChartPoint `json:"data"`
}

// rcChartRawResponse matches the RevenueCat v2 API response for chart endpoints.
// Values may contain null elements, so we use json.RawMessage for flexible parsing.
type rcChartRawResponse struct {
	Values [][]json.RawMessage `json:"values"`
	Yaxis  string              `json:"yaxis"`
}

// parseRawFloat attempts to parse a json.RawMessage as a float64.
// Handles both bare numbers (42, 49.99) and quoted strings ("42", "49.99").
// Returns 0, false for null or unparseable values.
func parseRawFloat(raw json.RawMessage) (float64, bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0, false
	}
	// Try bare number first.
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, true
	}
	// Try quoted string (e.g. "49.99").
	var str string
	if json.Unmarshal(raw, &str) == nil {
		if f, err := strconv.ParseFloat(str, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// transformChart converts the raw RC chart response into our simplified format.
// Null or unparseable data points are skipped. If every point fails, an empty
// chart is returned with a warning log instead of an error.
func transformChart(name string, raw rcChartRawResponse) (ChartResponse, error) {
	points := make([]ChartPoint, 0, len(raw.Values))
	for _, pair := range raw.Values {
		if len(pair) < 2 {
			continue
		}
		tsFloat, ok := parseRawFloat(pair[0])
		if !ok {
			continue
		}
		valFloat, ok := parseRawFloat(pair[1])
		if !ok {
			continue
		}

		date := time.Unix(int64(tsFloat), 0).UTC().Format("2006-01-02")

		var value int
		if raw.Yaxis == "$" {
			value = dollarsToTruncatedCents(valFloat)
		} else {
			value = int(valFloat)
		}

		points = append(points, ChartPoint{Date: date, Value: value})
	}
	if len(points) == 0 && len(raw.Values) > 0 {
		slog.Warn("all data points failed to parse for chart",
			"chart", name, "count", len(raw.Values))
	}
	return ChartResponse{Name: name, Data: points}, nil
}
