package revenuecat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultV2BaseURL = "https://api.revenuecat.com/v2"
const defaultV2Timeout = 15 * time.Second
const defaultV2CacheTTL = 5 * time.Minute

// V2Client calls the RevenueCat REST API (v2) for project-level metrics.
type V2Client struct {
	secretKey     string
	projectID     string
	baseURL       string
	httpClient    *http.Client
	overviewCache *ttlCache[OverviewMetrics]
	chartCache    *ttlCacheMap[ChartResponse]
}

// V2Option configures the V2Client for testing or non-default deployments.
type V2Option func(*V2Client)

// WithV2BaseURL overrides the default RevenueCat v2 API base URL.
func WithV2BaseURL(u string) V2Option {
	return func(c *V2Client) { c.baseURL = u }
}

// WithV2HTTPClient injects a custom *http.Client. Set Timeout on the
// client to override the default 15s timeout.
func WithV2HTTPClient(hc *http.Client) V2Option {
	return func(c *V2Client) { c.httpClient = hc }
}

// WithV2CacheTTL overrides the default cache TTL for overview and chart metrics.
func WithV2CacheTTL(d time.Duration) V2Option {
	return func(c *V2Client) {
		c.overviewCache = newTTLCache[OverviewMetrics](d)
		c.chartCache = newTTLCacheMap[ChartResponse](d)
	}
}

// NewV2Client returns a V2Client configured with the given secret key and project ID.
func NewV2Client(secretKey, projectID string, opts ...V2Option) *V2Client {
	c := &V2Client{
		secretKey: secretKey,
		projectID: projectID,
		baseURL:   defaultV2BaseURL,
		httpClient: &http.Client{
			Timeout: defaultV2Timeout,
		},
		overviewCache: newTTLCache[OverviewMetrics](defaultV2CacheTTL),
		chartCache:    newTTLCacheMap[ChartResponse](defaultV2CacheTTL),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// GetOverview fetches overview metrics from the RC v2 API, using the cache when available.
func (c *V2Client) GetOverview(ctx context.Context) (*OverviewMetrics, error) {
	if cached, ok := c.overviewCache.Get(); ok {
		return &cached, nil
	}

	url := fmt.Sprintf("%s/projects/%s/metrics/overview", c.baseURL, c.projectID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("revenuecat v2: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.secretKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("revenuecat v2: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("revenuecat v2: unexpected status %d: %s", resp.StatusCode, body)
	}

	var raw rcOverviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("revenuecat v2: decoding response: %w", err)
	}

	metrics := transformOverview(raw)
	c.overviewCache.Set(metrics)
	return &metrics, nil
}

// GetChart fetches chart data from the RC v2 API, using the cache when available.
func (c *V2Client) GetChart(ctx context.Context, params ChartParams) (*ChartResponse, error) {
	cacheKey := params.ChartName + ":" + params.StartDate + ":" + params.EndDate
	if cached, ok := c.chartCache.Get(cacheKey); ok {
		return &cached, nil
	}

	url := fmt.Sprintf("%s/projects/%s/charts/%s?start_date=%s&end_date=%s",
		c.baseURL, c.projectID, params.ChartName, params.StartDate, params.EndDate)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("revenuecat v2: building chart request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.secretKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("revenuecat v2: chart request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("revenuecat v2: chart unexpected status %d: %s", resp.StatusCode, body)
	}

	var raw rcChartRawResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("revenuecat v2: decoding chart response: %w", err)
	}

	chart, err := transformChart(params.ChartName, raw)
	if err != nil {
		return nil, fmt.Errorf("revenuecat v2: %w", err)
	}
	c.chartCache.Set(cacheKey, chart)
	return &chart, nil
}
