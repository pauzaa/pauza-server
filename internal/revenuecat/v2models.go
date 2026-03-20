package revenuecat

import (
	"encoding/json"
	"fmt"
	"math"
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
type rcChartRawResponse struct {
	Values [][]json.Number `json:"values"`
	Yaxis  string          `json:"yaxis"`
}

// transformChart converts the raw RC chart response into our simplified format.
// Returns an error if the response contained data points but none could be parsed,
// which indicates a format change in the upstream API.
func transformChart(name string, raw rcChartRawResponse) (ChartResponse, error) {
	points := make([]ChartPoint, 0, len(raw.Values))
	for _, pair := range raw.Values {
		if len(pair) < 2 {
			continue
		}
		tsFloat, err := pair[0].Float64()
		if err != nil {
			continue
		}
		valFloat, err := pair[1].Float64()
		if err != nil {
			continue
		}

		date := time.UnixMilli(int64(tsFloat)).UTC().Format("2006-01-02")

		var value int
		if raw.Yaxis == "$" {
			value = dollarsToTruncatedCents(valFloat)
		} else {
			value = int(valFloat)
		}

		points = append(points, ChartPoint{Date: date, Value: value})
	}
	if len(points) == 0 && len(raw.Values) > 0 {
		return ChartResponse{}, fmt.Errorf("all %d data points failed to parse for chart %q", len(raw.Values), name)
	}
	return ChartResponse{Name: name, Data: points}, nil
}
