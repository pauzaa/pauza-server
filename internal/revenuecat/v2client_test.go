package revenuecat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTransformOverview(t *testing.T) {
	t.Parallel()

	raw := rcOverviewResponse{
		Object: "overview_metrics",
		Metrics: []rcOverviewMetric{
			{ID: "mrr", Value: 49.99, Unit: "$"},
			{ID: "arr", Value: 599.88, Unit: "$"},
			{ID: "active_subscribers", Value: 42, Unit: "#"},
			{ID: "active_trials", Value: 7, Unit: "#"},
			{ID: "unknown_future_metric", Value: 123},
		},
	}

	out := transformOverview(raw)

	if out.MRR != 4999 {
		t.Errorf("MRR = %d, want 4999", out.MRR)
	}
	if out.ARR != 59988 {
		t.Errorf("ARR = %d, want 59988", out.ARR)
	}
	if out.ActiveSubscribers != 42 {
		t.Errorf("ActiveSubscribers = %d, want 42", out.ActiveSubscribers)
	}
	if out.ActiveTrials != 7 {
		t.Errorf("ActiveTrials = %d, want 7", out.ActiveTrials)
	}
}

func TestTransformOverview_EmptyMetrics(t *testing.T) {
	t.Parallel()

	out := transformOverview(rcOverviewResponse{})
	if out.MRR != 0 || out.ARR != 0 || out.ActiveSubscribers != 0 || out.ActiveTrials != 0 {
		t.Errorf("expected all zeros, got %+v", out)
	}
}

func TestV2Client_GetOverview(t *testing.T) {
	t.Parallel()

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true

		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v2/projects/proj_123/metrics/overview" {
			t.Errorf("path = %s, want /v2/projects/proj_123/metrics/overview", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer sk_test" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer sk_test")
		}

		resp := rcOverviewResponse{
			Object: "overview_metrics",
			Metrics: []rcOverviewMetric{
				{ID: "mrr", Value: 100.50},
				{ID: "active_subscribers", Value: 10},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewV2Client("sk_test", "proj_123", WithV2BaseURL(srv.URL+"/v2"))

	metrics, err := client.GetOverview(context.Background())
	if err != nil {
		t.Fatalf("GetOverview() error = %v", err)
	}
	if !called {
		t.Fatal("server was not called")
	}
	if metrics.MRR != 10050 {
		t.Errorf("MRR = %d, want 10050", metrics.MRR)
	}
	if metrics.ActiveSubscribers != 10 {
		t.Errorf("ActiveSubscribers = %d, want 10", metrics.ActiveSubscribers)
	}

	// Second call should hit cache
	called = false
	metrics2, err := client.GetOverview(context.Background())
	if err != nil {
		t.Fatalf("GetOverview() cached error = %v", err)
	}
	if called {
		t.Error("expected cached response, but server was called again")
	}
	if metrics2.MRR != 10050 {
		t.Errorf("cached MRR = %d, want 10050", metrics2.MRR)
	}
}

func TestV2Client_GetOverview_NonOK(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	client := NewV2Client("sk_bad", "proj_123", WithV2BaseURL(srv.URL+"/v2"))

	_, err := client.GetOverview(context.Background())
	if err == nil {
		t.Fatal("GetOverview() expected error for 403 response")
	}
}

// =========================================================================
// Chart tests
// =========================================================================

func TestTransformChart_Revenue(t *testing.T) {
	t.Parallel()

	raw := rcChartRawResponse{
		Values: [][]json.RawMessage{
			{json.RawMessage(`1709251200`), json.RawMessage(`49.99`)},
			{json.RawMessage(`1709337600`), json.RawMessage(`100.00`)},
		},
		Yaxis: "$",
	}

	out, err := transformChart("revenue", raw)
	if err != nil {
		t.Fatalf("transformChart() error = %v", err)
	}

	if out.Name != "revenue" {
		t.Errorf("Name = %q, want %q", out.Name, "revenue")
	}
	if len(out.Data) != 2 {
		t.Fatalf("len(Data) = %d, want 2", len(out.Data))
	}
	if out.Data[0].Date != "2024-03-01" {
		t.Errorf("Data[0].Date = %q, want %q", out.Data[0].Date, "2024-03-01")
	}
	if out.Data[0].Value != 4999 {
		t.Errorf("Data[0].Value = %d, want 4999 (49.99 * 100)", out.Data[0].Value)
	}
	if out.Data[1].Value != 10000 {
		t.Errorf("Data[1].Value = %d, want 10000", out.Data[1].Value)
	}
}

func TestTransformChart_Count(t *testing.T) {
	t.Parallel()

	raw := rcChartRawResponse{
		Values: [][]json.RawMessage{
			{json.RawMessage(`1709251200`), json.RawMessage(`42`)},
			{json.RawMessage(`1709337600`), json.RawMessage(`7`)},
		},
		Yaxis: "#",
	}

	out, err := transformChart("customers_new", raw)
	if err != nil {
		t.Fatalf("transformChart() error = %v", err)
	}

	if out.Name != "customers_new" {
		t.Errorf("Name = %q, want %q", out.Name, "customers_new")
	}
	if len(out.Data) != 2 {
		t.Fatalf("len(Data) = %d, want 2", len(out.Data))
	}
	if out.Data[0].Value != 42 {
		t.Errorf("Data[0].Value = %d, want 42", out.Data[0].Value)
	}
	if out.Data[1].Value != 7 {
		t.Errorf("Data[1].Value = %d, want 7", out.Data[1].Value)
	}
}

func TestTransformChart_NullValues(t *testing.T) {
	t.Parallel()

	raw := rcChartRawResponse{
		Values: [][]json.RawMessage{
			{json.RawMessage(`1709251200`), json.RawMessage(`49.99`)},
			{json.RawMessage(`1709337600`), json.RawMessage(`null`)},
			{json.RawMessage(`null`), json.RawMessage(`10.00`)},
			{json.RawMessage(`1709510400`), json.RawMessage(`25.00`)},
		},
		Yaxis: "$",
	}

	out, err := transformChart("revenue", raw)
	if err != nil {
		t.Fatalf("transformChart() error = %v", err)
	}
	if len(out.Data) != 2 {
		t.Fatalf("len(Data) = %d, want 2 (null pairs skipped)", len(out.Data))
	}
	if out.Data[0].Value != 4999 {
		t.Errorf("Data[0].Value = %d, want 4999", out.Data[0].Value)
	}
	if out.Data[1].Value != 2500 {
		t.Errorf("Data[1].Value = %d, want 2500", out.Data[1].Value)
	}
}

func TestTransformChart_AllPointsMalformed(t *testing.T) {
	t.Parallel()

	raw := rcChartRawResponse{
		Values: [][]json.RawMessage{
			{json.RawMessage(`"not_a_number"`), json.RawMessage(`42`)},
			{json.RawMessage(`"also_bad"`)},
		},
		Yaxis: "#",
	}

	out, err := transformChart("revenue", raw)
	if err != nil {
		t.Fatalf("transformChart() should not return error, got %v", err)
	}
	if len(out.Data) != 0 {
		t.Errorf("len(Data) = %d, want 0 (all malformed)", len(out.Data))
	}
}

func TestTransformChart_EmptyValues(t *testing.T) {
	t.Parallel()

	raw := rcChartRawResponse{Values: nil, Yaxis: "#"}

	out, err := transformChart("revenue", raw)
	if err != nil {
		t.Fatalf("transformChart() error = %v", err)
	}
	if len(out.Data) != 0 {
		t.Errorf("len(Data) = %d, want 0", len(out.Data))
	}
}

func TestV2Client_GetChart(t *testing.T) {
	t.Parallel()

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true

		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v2/projects/proj_123/charts/revenue" {
			t.Errorf("path = %s, want /v2/projects/proj_123/charts/revenue", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer sk_test" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer sk_test")
		}
		if sd := r.URL.Query().Get("start_date"); sd != "2026-02-18" {
			t.Errorf("start_date = %q, want %q", sd, "2026-02-18")
		}
		if ed := r.URL.Query().Get("end_date"); ed != "2026-03-20" {
			t.Errorf("end_date = %q, want %q", ed, "2026-03-20")
		}
		if rt := r.URL.Query().Get("realtime"); rt != "false" {
			t.Errorf("realtime = %q, want %q", rt, "false")
		}

		resp := rcChartRawResponse{
			Values: [][]json.RawMessage{
				{json.RawMessage(`1709251200000`), json.RawMessage(`49.99`)},
			},
			Yaxis: "$",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewV2Client("sk_test", "proj_123", WithV2BaseURL(srv.URL+"/v2"))

	chart, err := client.GetChart(context.Background(), ChartParams{
		ChartName: "revenue",
		StartDate: "2026-02-18",
		EndDate:   "2026-03-20",
	})
	if err != nil {
		t.Fatalf("GetChart() error = %v", err)
	}
	if !called {
		t.Fatal("server was not called")
	}
	if chart.Name != "revenue" {
		t.Errorf("Name = %q, want %q", chart.Name, "revenue")
	}
	if len(chart.Data) != 1 {
		t.Fatalf("len(Data) = %d, want 1", len(chart.Data))
	}
	if chart.Data[0].Value != 4999 {
		t.Errorf("Data[0].Value = %d, want 4999", chart.Data[0].Value)
	}

	// Second call should hit cache
	called = false
	chart2, err := client.GetChart(context.Background(), ChartParams{
		ChartName: "revenue",
		StartDate: "2026-02-18",
		EndDate:   "2026-03-20",
	})
	if err != nil {
		t.Fatalf("GetChart() cached error = %v", err)
	}
	if called {
		t.Error("expected cached response, but server was called again")
	}
	if chart2.Data[0].Value != 4999 {
		t.Errorf("cached Data[0].Value = %d, want 4999", chart2.Data[0].Value)
	}
}

func TestV2Client_GetChart_NonOK(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	client := NewV2Client("sk_bad", "proj_123", WithV2BaseURL(srv.URL+"/v2"))

	_, err := client.GetChart(context.Background(), ChartParams{
		ChartName: "revenue",
		StartDate: "2026-02-18",
		EndDate:   "2026-03-20",
	})
	if err == nil {
		t.Fatal("GetChart() expected error for 403 response")
	}
}
