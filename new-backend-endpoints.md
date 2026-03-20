# New Backend Endpoints for Pauza Admin Panel

> These endpoints must be added to `pauza-server` to support the admin panel.
> All sit under the existing `/api/v1/admin` route group, protected by
> `AdminJWTAuth` middleware and the `admin` rate limiter.
>
> **RC v2 API shapes need verification:** The `rcOverviewResponse` and `rcChartRawResponse`
> structs are best-effort guesses. The `transformOverview` and `transformChart` functions are
> left unimplemented. Verify the actual response shapes against the live
> [RevenueCat v2 API docs](https://www.revenuecat.com/docs/api-v2) during implementation.
>
> **Typing:** All new code must follow the existing strict typing conventions — explicit
> interface definitions for dependencies, typed input/output structs at each layer
> (handler → service → repository), no `interface{}` or untyped assertions. See
> `internal/handler/admin.go` and `internal/service/admin.go` for the established patterns.

---

## Summary

| # | Method | Path | Description |
|---|--------|------|-------------|
| 1 | GET | `/api/v1/admin/stats/user-growth` | Time-series new user registrations |
| 2 | GET | `/api/v1/admin/stats/active-users` | Time-series distinct active users |
| 3 | GET | `/api/v1/admin/revenuecat/overview` | RevenueCat metrics (MRR, ARR, subs, trials) |
| 4 | GET | `/api/v1/admin/revenuecat/charts/{chart_name}` | RevenueCat time-series chart data |
| 5 | GET | `/api/v1/admin/users/{id}/revenuecat` | Per-user RevenueCat subscriber detail |
| — | — | CORS middleware on `/api/v1/admin` | Cross-origin support for admin panel |

**New config fields:** `REVENUECAT_V2_SECRET_KEY`, `REVENUECAT_PROJECT_ID`, `ADMIN_CORS_ORIGINS` (see [Why These Config Fields?](#why-these-config-fields) for rationale)

**New Go dependency:** `github.com/go-chi/cors`

**New files to create:**
| File | Purpose |
|------|---------|
| `internal/revenuecat/v2client.go` | RevenueCat v2 API client |
| `internal/revenuecat/v2client_test.go` | Tests for v2 client |
| `internal/revenuecat/v2models.go` | Request/response types for v2 API |
| `internal/revenuecat/cache.go` | Simple in-memory TTL cache for RC responses |

**Existing files to modify:**
| File | Changes |
|------|---------|
| `internal/config/config.go` | Add 3 new config fields |
| `internal/handler/admin.go` | Add 5 new handler methods + 2 new service interfaces (`AdminTimeSeriesService`, `AdminRevenueCatService`) + extend composite `AdminService` interface |
| `internal/service/admin.go` | Add 5 new service methods, inject v2 client + v1 client |
| `internal/repository/admin.go` | Add 2 new repository methods (time-series queries) |
| `internal/server/routes.go` | Mount 5 new routes + CORS middleware |
| `internal/server/modules.go` | Construct V2Client, inject into services, wire CORS |
| `go.mod` / `go.sum` | Add `github.com/go-chi/cors` |

---

## Why These Config Fields?

### `REVENUECAT_V2_SECRET_KEY`

The backend already has `REVENUECAT_API_KEY` for the **v1 API** (used by `internal/revenuecat/client.go` to fetch individual subscriber records via `GetSubscriber`). However, the admin panel's Revenue page needs aggregate metrics (MRR, ARR, active subs) and time-series charts — these are only available in RevenueCat's **v2 REST API**, which uses a completely separate authentication key. The v2 secret key has different permissions and scopes than the v1 public API key, so a separate env var is required.

**Where to get it:** RevenueCat Dashboard → Project Settings → API Keys → "REST API v2 secret key". You need a key with `charts_metrics:charts:read` and `charts_metrics:overview:read` permissions.

### `REVENUECAT_PROJECT_ID`

The v1 API scopes everything implicitly through the API key — you don't specify a project. The v2 API is structured differently: endpoints are scoped per-project, e.g., `GET /v2/projects/{project_id}/metrics/overview`. The project ID is a required path parameter for every v2 call, so we need it as config.

**Where to get it:** RevenueCat Dashboard → Project Settings → the project ID is displayed at the top (a string like `proj1a2b3c4d`).

### `ADMIN_CORS_ORIGINS`

The admin panel is a separate web app served from a different origin than the API (e.g., `localhost:5173` in dev, `admin.pauza.app` in prod). Browsers enforce the Same-Origin Policy and block cross-origin `fetch()` requests unless the server responds with `Access-Control-Allow-Origin` headers. The current pauza-server has **no CORS middleware** because the mobile app doesn't need it (native apps aren't subject to browser CORS). This config field tells the new CORS middleware which origins to whitelist. It's configurable (comma-separated) so it works across dev/staging/prod without code changes.

---

## RevenueCat API Reference

The following RevenueCat documentation pages are relevant for implementing the proxy endpoints:

| Endpoint | RC API Version | Documentation URL |
|----------|---------------|-------------------|
| Overview metrics (MRR, ARR, subs, trials) | v2 | https://www.revenuecat.com/docs/api-v2#tag/Project/operation/get-overview-metrics |
| Charts (revenue, new_customers, etc.) | v2 | https://www.revenuecat.com/docs/api-v2#tag/Chart |
| Chart options/filters | v2 | https://www.revenuecat.com/docs/api-v2#tag/Chart/operation/get-chart-options |
| Subscriber lookup (already in backend) | v1 | https://www.revenuecat.com/docs/api-v1#tag/customers/operation/subscribers |
| Authentication & API keys | — | https://www.revenuecat.com/docs/api-v2#section/Authentication |
| Rate limits | — | https://www.revenuecat.com/docs/api-v2#section/Rate-Limits |

**Key rate limits to know:**
- **Overview metrics:** Part of the general v2 rate limit (varies by plan)
- **Charts API:** **15 requests per minute** — this is why the backend caches chart responses for 5 minutes
- **Subscriber lookup (v1):** 480 requests per minute per project

**API key permissions needed for the v2 secret key:**
- `charts_metrics:overview:read` — for the overview metrics endpoint
- `charts_metrics:charts:read` — for the charts endpoint

---

## Endpoint 1: GET /api/v1/admin/stats/user-growth

Returns time-series data of new user registrations grouped by time bucket.

### Query Parameters

| Param | Type | Default | Validation |
|-------|------|---------|------------|
| `range` | string | `30d` | One of: `30d`, `90d`, `1y`, `all`. Invalid values silently default to `30d`. |
| `granularity` | string | auto | Optional. One of: `day`, `week`, `month`. When omitted or invalid, auto-selected based on `range` (see table below). |

### Range → Granularity Auto-Selection

When `granularity` is not provided, the backend auto-selects based on `range`:

| Range | SQL interval | Auto-selected granularity |
|-------|-------------|---------------------------|
| `30d` | `interval '30 days'` | `day` |
| `90d` | `interval '90 days'` | `week` |
| `1y`  | `interval '1 year'`  | `week` |
| `all` | No WHERE clause      | `month` |

The admin panel frontend does not send `granularity` — it relies on auto-selection.

### SQL Query

```sql
SELECT date_trunc($1, created_at AT TIME ZONE 'UTC')::date AS date,
       COUNT(*) AS value
FROM users
WHERE created_at >= (now() - $2::interval)  -- omit WHERE for 'all'
GROUP BY 1
ORDER BY 1 ASC
```

Parameters:
- `$1`: granularity string (`'day'`, `'week'`, `'month'`)
- `$2`: interval string (`'30 days'`, `'90 days'`, `'1 year'`)

For `range=all`, omit the `WHERE` clause entirely.

### Response — 200 OK

```json
{
  "data": [
    { "date": "2025-01-01", "value": 12 },
    { "date": "2025-01-02", "value": 8 },
    { "date": "2025-01-03", "value": 15 }
  ],
  "granularity": "day"
}
```

`data` may be an empty array `[]` if no users exist in the range.

### Errors

| Code | When |
|------|------|
| `UNAUTHORIZED` (401) | Missing or invalid admin JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Database query failure |

### Go Types

```go
// In handler
type timeSeriesParams struct {
	Granularity string // "day", "week", "month"
	Range       string // "30d", "90d", "1y", "all"
}

type timeSeriesPoint struct {
	Date  string `json:"date"`  // YYYY-MM-DD
	Value int    `json:"value"`
}

type timeSeriesResponse struct {
	Data        []timeSeriesPoint `json:"data"`
	Granularity string            `json:"granularity"`
}
```

```go
// In repository interface
type TimeSeriesPoint struct {
	Date  time.Time
	Value int
}

type TimeSeriesParams struct {
	Granularity string    // "day", "week", "month"
	Since       time.Time // zero value means no lower bound (range=all)
}

// On AdminRepository interface:
GetUserGrowth(ctx context.Context, db DBTX, params TimeSeriesParams) ([]TimeSeriesPoint, error)
```

### Handler Method

```go
func (h *AdminHandler) GetUserGrowth(w http.ResponseWriter, r *http.Request) {
	in := parseTimeSeriesInput(r)

	points, err := h.svc.GetUserGrowth(r.Context(), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	resp := timeSeriesResponse{
		Data:        toTimeSeriesPointsJSON(points),
		Granularity: in.Granularity,
	}
	writeJSON(w, h.logger, http.StatusOK, resp, "admin-user-growth")
}
```

### Handler Method: GetActiveUsers

```go
func (h *AdminHandler) GetActiveUsers(w http.ResponseWriter, r *http.Request) {
	in := parseTimeSeriesInput(r)

	points, err := h.svc.GetActiveUsers(r.Context(), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	resp := timeSeriesResponse{
		Data:        toTimeSeriesPointsJSON(points),
		Granularity: in.Granularity,
	}
	writeJSON(w, h.logger, http.StatusOK, resp, "admin-active-users")
}
```

### Handler Method: GetRCOverview

```go
func (h *AdminHandler) GetRCOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := h.svc.GetRCOverview(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, overview, "admin-rc-overview")
}
```

### Shared Helper: parseTimeSeriesInput

```go
func parseTimeSeriesInput(r *http.Request) service.TimeSeriesInput {
	rangeStr := r.URL.Query().Get("range")
	switch rangeStr {
	case "30d", "90d", "1y", "all":
		// valid
	default:
		rangeStr = "30d"
	}

	// Auto-select granularity based on range if not provided
	granularity := r.URL.Query().Get("granularity")
	switch granularity {
	case "day", "week", "month":
		// valid — explicit override
	default:
		switch rangeStr {
		case "1y":
			granularity = "week"
		case "all":
			granularity = "month"
		default:
			granularity = "day"
		}
	}

	return service.TimeSeriesInput{Granularity: granularity, Range: rangeStr}
}
```

### Shared Helper: toTimeSeriesPointsJSON

```go
func toTimeSeriesPointsJSON(points []repository.TimeSeriesPoint) []timeSeriesPoint {
	out := make([]timeSeriesPoint, len(points))
	for i, p := range points {
		out[i] = timeSeriesPoint{
			Date:  p.Date.Format("2006-01-02"),
			Value: p.Value,
		}
	}
	return out
}
```

### Service Types

```go
// In service/admin.go — all new input/output types for the new endpoints.
// Follows the existing pattern of exported service-layer types
// (e.g., LoginInput, ListUsersInput, UserDetailOutput).

type TimeSeriesInput struct {
	Granularity string // "day", "week", "month"
	Range       string // "30d", "90d", "1y", "all"
}

type TimeSeriesOutput struct {
	Points      []repository.TimeSeriesPoint
	Granularity string
}

type RCChartInput struct {
	ChartName string // "revenue", "new_customers", "active_subscriptions", "churn"
	Range     string // "30d", "90d", "1y", "all" — passed through for cache key
	StartDate string // YYYY-MM-DD
	EndDate   string // YYYY-MM-DD
}

type RCSubscriberEntitlement struct {
	EntitlementID          string  `json:"entitlement_id"`
	IsActive               bool    `json:"is_active"`
	ProductIdentifier      string  `json:"product_identifier"`
	PurchaseDate           string  `json:"purchase_date"`            // RFC 3339 — always present (source is non-pointer time.Time)
	ExpiresDate            *string `json:"expires_date"`             // nil for lifetime entitlements
	GracePeriodExpiresDate *string `json:"grace_period_expires_date"`
}

type RCSubscriberOutput struct {
	AppUserID    string                    `json:"app_user_id"`
	Entitlements []RCSubscriberEntitlement `json:"entitlements"`
}
```

### Service Method

```go
func (s *AdminService) GetUserGrowth(ctx context.Context, in TimeSeriesInput) ([]repository.TimeSeriesPoint, error) {
	repoParams := repository.TimeSeriesParams{
		Granularity: in.Granularity,
	}

	switch in.Range {
	case "30d":
		repoParams.Since = time.Now().AddDate(0, 0, -30)
	case "90d":
		repoParams.Since = time.Now().AddDate(0, 0, -90)
	case "1y":
		repoParams.Since = time.Now().AddDate(-1, 0, 0)
	case "all":
		// repoParams.Since remains zero value
	}

	return s.adminRepo.GetUserGrowth(ctx, s.pool, repoParams)
}
```

### Service Method: GetActiveUsers

```go
func (s *AdminService) GetActiveUsers(ctx context.Context, in TimeSeriesInput) ([]repository.TimeSeriesPoint, error) {
	repoParams := repository.TimeSeriesParams{
		Granularity: in.Granularity,
	}

	switch in.Range {
	case "30d":
		repoParams.Since = time.Now().AddDate(0, 0, -30)
	case "90d":
		repoParams.Since = time.Now().AddDate(0, 0, -90)
	case "1y":
		repoParams.Since = time.Now().AddDate(-1, 0, 0)
	case "all":
		// repoParams.Since remains zero value
	}

	return s.adminRepo.GetActiveUsers(ctx, s.pool, repoParams)
}
```

### Repository Method

```go
func (r *PgxAdminRepository) GetUserGrowth(ctx context.Context, db DBTX, params TimeSeriesParams) ([]TimeSeriesPoint, error) {
	var (
		query string
		args  []any
	)

	if params.Since.IsZero() {
		query = `
			SELECT date_trunc($1, created_at AT TIME ZONE 'UTC')::date AS date,
			       COUNT(*) AS value
			FROM users
			GROUP BY 1
			ORDER BY 1 ASC`
		args = []any{params.Granularity}
	} else {
		query = `
			SELECT date_trunc($1, created_at AT TIME ZONE 'UTC')::date AS date,
			       COUNT(*) AS value
			FROM users
			WHERE created_at >= $2
			GROUP BY 1
			ORDER BY 1 ASC`
		args = []any{params.Granularity, params.Since}
	}

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query user growth: %w", err)
	}
	defer rows.Close()

	var points []TimeSeriesPoint
	for rows.Next() {
		var p TimeSeriesPoint
		if err := rows.Scan(&p.Date, &p.Value); err != nil {
			return nil, fmt.Errorf("scan user growth row: %w", err)
		}
		points = append(points, p)
	}
	return points, rows.Err()
}
```

---

## Endpoint 2: GET /api/v1/admin/stats/active-users

Returns time-series data of distinct active users (users with at least one restriction session started in each bucket).

### Query Parameters

Same as endpoint 1: `granularity` and `range`.

### SQL Query

```sql
SELECT date_trunc($1, to_timestamp(started_at / 1000.0) AT TIME ZONE 'UTC')::date AS date,
       COUNT(DISTINCT user_id) AS value
FROM restriction_sessions
WHERE started_at >= $2  -- epoch ms; omit WHERE for 'all'
GROUP BY 1
ORDER BY 1 ASC
```

**Important:** `restriction_sessions.started_at` is stored as **epoch milliseconds** (BIGINT), not a `TIMESTAMPTZ`. The `$2` parameter must also be epoch ms.

For the `WHERE` clause, convert the `Since` time to epoch ms:
```go
sinceMs := params.Since.UnixMilli() // int64
```

For `range=all`, omit the `WHERE` clause.

### Response — 200 OK

Same shape as endpoint 1.

### Errors

Same as endpoint 1.

### Repository Method

```go
func (r *PgxAdminRepository) GetActiveUsers(ctx context.Context, db DBTX, params TimeSeriesParams) ([]TimeSeriesPoint, error) {
	var (
		query string
		args  []any
	)

	if params.Since.IsZero() {
		query = `
			SELECT date_trunc($1, to_timestamp(started_at / 1000.0) AT TIME ZONE 'UTC')::date AS date,
			       COUNT(DISTINCT user_id) AS value
			FROM restriction_sessions
			GROUP BY 1
			ORDER BY 1 ASC`
		args = []any{params.Granularity}
	} else {
		query = `
			SELECT date_trunc($1, to_timestamp(started_at / 1000.0) AT TIME ZONE 'UTC')::date AS date,
			       COUNT(DISTINCT user_id) AS value
			FROM restriction_sessions
			WHERE started_at >= $2
			GROUP BY 1
			ORDER BY 1 ASC`
		args = []any{params.Granularity, params.Since.UnixMilli()}
	}

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query active users: %w", err)
	}
	defer rows.Close()

	var points []TimeSeriesPoint
	for rows.Next() {
		var p TimeSeriesPoint
		if err := rows.Scan(&p.Date, &p.Value); err != nil {
			return nil, fmt.Errorf("scan active users row: %w", err)
		}
		points = append(points, p)
	}
	return points, rows.Err()
}
```

---

## Endpoint 3: GET /api/v1/admin/revenuecat/overview

Proxies the RevenueCat v2 Overview Metrics API and returns simplified metrics.

### Upstream API Call

```
GET https://api.revenuecat.com/v2/projects/{project_id}/metrics/overview
Authorization: Bearer {REVENUECAT_V2_SECRET_KEY}
Content-Type: application/json
```

### Response — 200 OK

```json
{
  "mrr": 42000,
  "arr": 504000,
  "active_subscribers": 134,
  "active_trials": 12
}
```

`mrr` and `arr` are in **cents** (integer) to avoid floating-point precision issues. The backend converts from RevenueCat's dollar amounts if needed.

### Errors

| Code | When |
|------|------|
| `UNAUTHORIZED` (401) | Missing or invalid admin JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | RevenueCat API unreachable or returned an error |

### Caching

The backend caches the overview response **in memory for 5 minutes** to avoid hammering the RC API. Implementation:

```go
// internal/revenuecat/cache.go

type cachedItem[T any] struct {
	value     T
	expiresAt time.Time
}

type Cache[T any] struct {
	mu   sync.Mutex
	item *cachedItem[T]
	ttl  time.Duration
}

func NewCache[T any](ttl time.Duration) *Cache[T] {
	return &Cache[T]{ttl: ttl}
}

func (c *Cache[T]) Get() (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.item != nil && time.Now().Before(c.item.expiresAt) {
		return c.item.value, true
	}
	var zero T
	return zero, false
}

func (c *Cache[T]) Set(value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.item = &cachedItem[T]{
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
}
```

### Go Types

```go
// internal/revenuecat/v2models.go

type OverviewMetrics struct {
	MRR               int `json:"mrr"`                // cents
	ARR               int `json:"arr"`                // cents
	ActiveSubscribers int `json:"active_subscribers"`
	ActiveTrials      int `json:"active_trials"`
}

// Raw RC v2 response — shape depends on actual API; transform in client
type rcOverviewResponse struct {
	Metrics []rcOverviewMetric `json:"metrics"`
}

type rcOverviewMetric struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Value       float64 `json:"value"`
	Description string  `json:"description"`
	Period      string  `json:"period"`
}
```

### V2Client Method

```go
// internal/revenuecat/v2client.go

type V2Client struct {
	secretKey     string
	projectID     string
	baseURL       string
	httpClient    *http.Client
	overviewCache *Cache[OverviewMetrics] // single value cache (no key)
	chartCache    *chartCacheMap          // keyed by "chartName:range" (e.g., "revenue:30d")
}

func NewV2Client(secretKey, projectID string) *V2Client {
	return &V2Client{
		secretKey:     secretKey,
		projectID:     projectID,
		baseURL:       "https://api.revenuecat.com/v2",
		httpClient:    &http.Client{Timeout: 15 * time.Second},
		overviewCache: NewCache[OverviewMetrics](5 * time.Minute),
		chartCache:    newChartCacheMap(5 * time.Minute),
	}
}

func (c *V2Client) GetOverview(ctx context.Context) (*OverviewMetrics, error) {
	// Check cache first
	if cached, ok := c.overviewCache.Get(); ok {
		return &cached, nil
	}

	url := fmt.Sprintf("%s/projects/%s/metrics/overview", c.baseURL, c.projectID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.secretKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("revenuecat v2 overview: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("revenuecat v2 overview: status %d: %s", resp.StatusCode, string(body))
	}

	var raw rcOverviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode overview: %w", err)
	}

	metrics := transformOverview(raw) // extract MRR, ARR, etc. from the raw response
	c.overviewCache.Set(metrics)
	return &metrics, nil
}
```

The `transformOverview` function extracts the relevant metrics from RevenueCat's response structure and converts dollar amounts to cents (multiply by 100 and round).

> **Implementation note:** The `rcOverviewResponse` struct above is a best-effort representation of the RC v2 overview response. The exact metric IDs (e.g., `"mrr"`, `"active_subscribers"`) must be verified against the live [RevenueCat v2 Overview Metrics API](https://www.revenuecat.com/docs/api-v2#tag/Project/operation/get-overview-metrics) during implementation. The `transformOverview` function should map metric IDs to the `OverviewMetrics` struct fields accordingly.

---

## Endpoint 4: GET /api/v1/admin/revenuecat/charts/{chart_name}

Proxies the RevenueCat v2 Charts API.

### Path Parameters

| Param | Type | Required | Validation |
|-------|------|----------|------------|
| `chart_name` | string | yes | Must be one of: `revenue`, `new_customers`, `active_subscriptions`, `churn`. Any other value returns `VALIDATION_ERROR` (422). |

### Query Parameters

| Param | Type | Default | Validation |
|-------|------|---------|------------|
| `range` | string | `30d` | One of: `30d`, `90d`, `1y`, `all`. Invalid values silently default to `30d`. |

### Upstream API Call

```
GET https://api.revenuecat.com/v2/projects/{project_id}/charts/{chart_name}
  ?start_date={YYYY-MM-DD}
  &end_date={YYYY-MM-DD}
Authorization: Bearer {REVENUECAT_V2_SECRET_KEY}
```

The handler converts the `range` parameter to `start_date` / `end_date`:

| Range | start_date | end_date |
|-------|-----------|----------|
| `30d` | today - 30 days | today |
| `90d` | today - 90 days | today |
| `1y` | today - 1 year | today |
| `all` | `2020-01-01` (or omit) | today |

### Allowed Chart Names

| chart_name | Description |
|------------|-------------|
| `revenue` | Revenue over time |
| `new_customers` | New subscriber count over time |
| `active_subscriptions` | Active subscription count over time |
| `churn` | Churn rate over time |

### Response — 200 OK

```json
{
  "name": "revenue",
  "data": [
    { "date": "2025-01-01", "value": 15000 },
    { "date": "2025-01-02", "value": 12000 },
    { "date": "2025-01-03", "value": 18000 }
  ]
}
```

For the `revenue` chart, values are in **cents**. For count-based charts (`new_customers`, `active_subscriptions`), values are integers. For `churn`, values are basis points (0.0-1.0 as percentage).

### Errors

| Code | When |
|------|------|
| `VALIDATION_ERROR` (422) | Invalid chart name |
| `UNAUTHORIZED` (401) | Missing or invalid admin JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | RevenueCat API unreachable or returned an error |

### Rate Limiting Considerations

RevenueCat Charts API allows **15 requests per minute**. Mitigations:
1. **Backend cache:** Each `(chart_name, range)` pair is cached in memory for 5 minutes.
2. **Admin rate limiter:** The existing admin rate limit (30 req/min per user) provides a secondary guard.

### Caching Strategy

Use a map-based cache keyed by `"{chart_name}:{range}"`:

```go
type chartCacheMap struct {
	mu    sync.Mutex
	items map[string]*cachedItem[ChartResponse]
	ttl   time.Duration
}

func newChartCacheMap(ttl time.Duration) *chartCacheMap {
	return &chartCacheMap{
		items: make(map[string]*cachedItem[ChartResponse]),
		ttl:   ttl,
	}
}

func (m *chartCacheMap) Get(key string) (ChartResponse, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[key]
	if !ok || time.Now().After(item.expiresAt) {
		return ChartResponse{}, false
	}
	return item.value, true
}

func (m *chartCacheMap) Set(key string, value ChartResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[key] = &cachedItem[ChartResponse]{
		value:     value,
		expiresAt: time.Now().Add(m.ttl),
	}
}
```

### Go Types

```go
// internal/revenuecat/v2models.go

type ChartResponse struct {
	Name string       `json:"name"`
	Data []ChartPoint `json:"data"`
}

type ChartPoint struct {
	Date  string `json:"date"`  // YYYY-MM-DD
	Value int    `json:"value"` // cents for revenue, count for others
}

type ChartParams struct {
	ChartName string
	Range     string // "30d", "90d", "1y", "all" — used as cache key
	StartDate string // YYYY-MM-DD
	EndDate   string // YYYY-MM-DD
}
```

### Raw RC Chart Response Type

> **Implementation note:** The exact shape of the RC v2 chart response must be verified against the live [RevenueCat v2 Charts API](https://www.revenuecat.com/docs/api-v2#tag/Chart). The struct below is a best-effort representation. The `transformChart` function should extract date/value pairs and convert revenue values from dollars to cents.

```go
// internal/revenuecat/v2models.go

// rcChartRawResponse — verify against RC v2 API docs during implementation.
type rcChartRawResponse struct {
	Values []rcChartRawPoint `json:"values"`
}

type rcChartRawPoint struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}
```

### V2Client Method

> **Note:** Requires `"net/url"` import for `url.PathEscape`.

```go
func (c *V2Client) GetChart(ctx context.Context, params ChartParams) (*ChartResponse, error) {
	cacheKey := params.ChartName + ":" + params.Range
	if cached, ok := c.chartCache.Get(cacheKey); ok {
		return &cached, nil
	}

	reqURL := fmt.Sprintf("%s/projects/%s/charts/%s?start_date=%s&end_date=%s",
		c.baseURL, c.projectID,
		url.PathEscape(params.ChartName),
		params.StartDate, params.EndDate,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create chart request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.secretKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("revenuecat chart %s: %w", params.ChartName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("revenuecat chart %s: status %d: %s", params.ChartName, resp.StatusCode, string(body))
	}

	var raw rcChartRawResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode chart: %w", err)
	}

	chart := transformChart(params.ChartName, raw)
	c.chartCache.Set(cacheKey, chart)
	return &chart, nil
}
```

### Handler Method

```go
var allowedChartNames = map[string]bool{
	"revenue":              true,
	"new_customers":        true,
	"active_subscriptions": true,
	"churn":                true,
}

func (h *AdminHandler) GetRCChart(w http.ResponseWriter, r *http.Request) {
	chartName := chi.URLParam(r, "chart_name")
	if !allowedChartNames[chartName] {
		apperror.ValidationError(w, "Invalid chart name", nil)
		return
	}

	rangeStr := r.URL.Query().Get("range")
	startDate, endDate := rangeToDateStrings(rangeStr) // helper: converts range to YYYY-MM-DD

	chart, err := h.svc.GetRCChart(r.Context(), service.RCChartInput{
		ChartName: chartName,
		Range:     rangeStr,
		StartDate: startDate,
		EndDate:   endDate,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, chart, "admin-rc-chart")
}
```

### Helper: rangeToDateStrings

```go
func rangeToDateStrings(rangeStr string) (startDate, endDate string) {
	now := time.Now().UTC()
	endDate = now.Format("2006-01-02")

	switch rangeStr {
	case "90d":
		startDate = now.AddDate(0, 0, -90).Format("2006-01-02")
	case "1y":
		startDate = now.AddDate(-1, 0, 0).Format("2006-01-02")
	case "all":
		startDate = "2020-01-01"
	default: // "30d" and invalid
		startDate = now.AddDate(0, 0, -30).Format("2006-01-02")
	}
	return
}
```

---

## Endpoint 5: GET /api/v1/admin/users/{id}/revenuecat

Returns the live RevenueCat subscriber record for a specific user.

### Path Parameters

| Param | Type | Required | Validation |
|-------|------|----------|------------|
| `id` | string | yes | Non-empty path segment (user UUID) |

### Flow

1. Look up user by `id` in the database.
2. Query `user_entitlements` for the user's `revenuecat_app_user_id` where `entitlement = 'premium'`.
3. If `revenuecat_app_user_id` is null/empty → return 404 NOT_FOUND.
4. Call the **existing** v1 `rcClient.GetSubscriber(ctx, appUserID)`.
5. Transform the `revenuecat.SubscriberResponse` into a simplified admin-facing format.

### Response — 200 OK

```json
{
  "app_user_id": "$RCAnonymousID:abc123",
  "entitlements": [
    {
      "entitlement_id": "premium",
      "is_active": true,
      "product_identifier": "pauza_premium_monthly",
      "purchase_date": "2025-01-01T00:00:00Z",
      "expires_date": "2026-03-30T00:00:00Z",
      "grace_period_expires_date": null
    }
  ]
}
```

`entitlements` is an array — one entry per entitlement the user has in RevenueCat (currently only "premium" but the response is forward-compatible).

### Errors

| Code | When |
|------|------|
| `UNAUTHORIZED` (401) | Missing or invalid admin JWT |
| `NOT_FOUND` (404) | User not found, or user has no `revenuecat_app_user_id` |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | RevenueCat API unreachable or returned an error |

### Go Types

The response types (`RCSubscriberOutput`, `RCSubscriberEntitlement`) are defined as service-level types (see [Service Types](#service-types) in Endpoint 1). The handler returns these directly — no separate handler-level response types needed since the JSON tags are already on the service types.

### Repository Addition

Add a targeted query to get the RC user ID without loading the full user detail:

```go
// On AdminRepository interface:
GetUserRCAppUserID(ctx context.Context, db DBTX, userID string) (string, error)
```

```go
func (r *PgxAdminRepository) GetUserRCAppUserID(ctx context.Context, db DBTX, userID string) (string, error) {
	var rcAppUserID *string
	err := db.QueryRow(ctx,
		`SELECT revenuecat_app_user_id
		 FROM user_entitlements
		 WHERE user_id = $1 AND entitlement = 'premium'`,
		userID,
	).Scan(&rcAppUserID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get user rc app user id: %w", err)
	}

	if rcAppUserID == nil || *rcAppUserID == "" {
		return "", ErrNotFound
	}

	return *rcAppUserID, nil
}
```

> **Note:** Uses pgx-native `*string` scanning (not `database/sql.NullString`) to match the existing codebase pattern.

### Service Method

```go
func (s *AdminService) GetUserRCSubscription(ctx context.Context, in GetUserDetailInput) (*RCSubscriberOutput, error) {
	// 1. Verify user exists
	_, err := s.adminRepo.GetUserDetail(ctx, s.pool, in.UserID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, NotFoundError("user not found")
		}
		return nil, fmt.Errorf("get user detail: %w", err)
	}

	// 2. Get RC app user ID
	rcAppUserID, err := s.adminRepo.GetUserRCAppUserID(ctx, s.pool, in.UserID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, NotFoundError("User has no RevenueCat subscription")
		}
		return nil, fmt.Errorf("get rc app user id: %w", err)
	}

	// 3. Fetch from RevenueCat v1 API
	subscriber, err := s.rcClient.GetSubscriber(ctx, rcAppUserID)
	if err != nil {
		return nil, fmt.Errorf("revenuecat subscriber lookup: %w", err)
	}

	// 4. Transform
	return transformSubscriberResponse(rcAppUserID, subscriber), nil
}
```

> **Error pattern:** Service methods return `service.NotFoundError(msg)` (creates `*APIError` wrapping `ErrNotFound`) or plain `fmt.Errorf` for internal errors. The handler's `writeServiceError(w, err)` maps these to the correct HTTP status codes. See `internal/service/auth_types.go` for the error constructors and `internal/handler/helpers.go` for the mapping logic.

### Transform Function

> **Reference:** The existing `revenuecat.SubscriberResponse` type is defined in `internal/revenuecat/models.go`. The relevant fields of `EntitlementObj` are: `ExpiresDate *time.Time`, `GracePeriodExpiresDate *time.Time`, `PurchaseDate time.Time`, `ProductIdentifier string`. The entitlements are in `sub.Subscriber.Entitlements` (a `map[string]EntitlementObj`).

```go
func transformSubscriberResponse(appUserID string, sub *revenuecat.SubscriberResponse) *RCSubscriberOutput {
	var entitlements []RCSubscriberEntitlement

	for id, ent := range sub.Subscriber.Entitlements {
		item := RCSubscriberEntitlement{
			EntitlementID:     id,
			ProductIdentifier: ent.ProductIdentifier,
			PurchaseDate:      ent.PurchaseDate.UTC().Format(time.RFC3339),
		}

		// Determine active status
		if ent.ExpiresDate == nil {
			item.IsActive = true // Lifetime
		} else if ent.ExpiresDate.After(time.Now()) {
			item.IsActive = true
		} else if ent.GracePeriodExpiresDate != nil && ent.GracePeriodExpiresDate.After(time.Now()) {
			item.IsActive = true // In grace period
		}

		item.ExpiresDate = timePtrToRFC3339(ent.ExpiresDate)
		item.GracePeriodExpiresDate = timePtrToRFC3339(ent.GracePeriodExpiresDate)

		entitlements = append(entitlements, item)
	}

	return &RCSubscriberOutput{
		AppUserID:    appUserID,
		Entitlements: entitlements,
	}
}

func timeToRFC3339Ptr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func timePtrToRFC3339(t *time.Time) *string {
	if t == nil {
		return nil
	}
	return timeToRFC3339Ptr(*t)
}
```

### Handler Method

```go
func (h *AdminHandler) GetUserRCSubscription(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}

	detail, err := h.svc.GetUserRCSubscription(r.Context(), service.GetUserDetailInput{UserID: userID})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, detail, "admin-user-rc")
}
```

> **Note:** Uses `parseUUIDParam` (from `handler/helpers.go`) instead of manual empty-string check, matching the existing pattern in other admin handlers.

---

## Infrastructure Change: CORS Middleware

### Why

The admin panel (served from a different origin, e.g., `http://localhost:5173` in dev or `https://admin.pauza.app` in prod) needs to make cross-origin requests to the backend API. Without CORS headers, browsers will block these requests.

### New Config Field

Add to `internal/config/config.go`:

```go
type Config struct {
	// ... existing fields ...

	// Admin CORS
	AdminCORSOrigins string `envconfig:"ADMIN_CORS_ORIGINS" default:"http://localhost:5173"`

	// RevenueCat v2
	RevenueCatV2SecretKey string `envconfig:"REVENUECAT_V2_SECRET_KEY"`
	RevenueCatProjectID   string `envconfig:"REVENUECAT_PROJECT_ID"`
}

// ParseAdminCORSOrigins splits the comma-separated origins into a slice.
func (c *Config) ParseAdminCORSOrigins() []string {
	raw := strings.TrimSpace(c.AdminCORSOrigins)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	return origins
}
```

### New Go Dependency

```bash
go get github.com/go-chi/cors
```

### Route Registration

In `internal/server/routes.go`, add CORS middleware to the admin route group:

```go
import "github.com/go-chi/cors"

// Inside the routes setup function, wrap the admin group:
r.Route("/api/v1", func(r chi.Router) {
	// ... existing routes ...

	r.Route("/admin", func(r chi.Router) {
		// CORS middleware — must be BEFORE rate limiting and auth
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   cfg.ParseAdminCORSOrigins(),
			AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Request-Id"},
			AllowCredentials: false,
			MaxAge:           300, // 5 minutes preflight cache
		}))

		// Public admin endpoint
		r.With(/* rate limiter */).Post("/login", deps.adminHandler.Login)

		// Protected admin endpoints
		r.Group(func(r chi.Router) {
			r.Use(deps.adminJWTAuth)
			r.Use(/* admin rate limiter */)

			// Existing routes
			r.Get("/users", deps.adminHandler.ListUsers)
			r.Get("/users/{id}", deps.adminHandler.GetUserDetail)
			r.Get("/stats", deps.adminHandler.GetStats)
			r.Post("/users/{id}/entitlements", deps.adminHandler.ManageEntitlement)
			r.Get("/entitlements", deps.adminHandler.ListEntitlements)

			// NEW: Time-series stats
			r.Get("/stats/user-growth", deps.adminHandler.GetUserGrowth)
			r.Get("/stats/active-users", deps.adminHandler.GetActiveUsers)

			// NEW: RevenueCat proxy
			r.Get("/revenuecat/overview", deps.adminHandler.GetRCOverview)
			r.Get("/revenuecat/charts/{chart_name}", deps.adminHandler.GetRCChart)

			// NEW: Per-user RevenueCat detail
			r.Get("/users/{id}/revenuecat", deps.adminHandler.GetUserRCSubscription)
		})
	})
})
```

### Module Wiring

In `internal/server/modules.go`, construct the V2Client and inject it:

```go
func buildDependencies(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool, mailer mail.Sender, pushSender push.Sender, aiProvider ai.Provider) appDependencies {
	// ... existing code ...

	// RevenueCat v1 client (existing — already created for webhookService).
	// NOTE: rcClient already exists at this point (created earlier for webhookService);
	// we just also pass it to adminService below for per-user subscriber lookups.
	rcClient := revenuecat.NewClient(cfg.RevenueCatAPIKey)

	// RevenueCat v2 client (NEW)
	var rcV2Client *revenuecat.V2Client
	if cfg.RevenueCatV2SecretKey != "" && cfg.RevenueCatProjectID != "" {
		rcV2Client = revenuecat.NewV2Client(cfg.RevenueCatV2SecretKey, cfg.RevenueCatProjectID)
	}

	// Admin service — add rcClient and rcV2Client as positional params
	adminService := service.NewAdminService(
		pool, adminRepo, cfg.JWTSecret, cfg.AdminJWTAccessTokenTTL,
		rcClient, rcV2Client, logger,
	)

	// ... rest of wiring ...
}
```

### AdminService Constructor Changes

Add `rcClient` and `rcV2Client` as positional parameters to the existing constructor (matching the established pattern — no functional options):

```go
type AdminService struct {
	pool          repository.Pool
	adminRepo     repository.AdminRepository
	jwtSecret     string
	adminTokenTTL time.Duration
	rcClient      *revenuecat.Client    // v1 — for subscriber lookups, may be nil
	rcV2Client    *revenuecat.V2Client  // v2 — for metrics/charts, may be nil
	logger        *slog.Logger
}

func NewAdminService(
	pool repository.Pool,
	adminRepo repository.AdminRepository,
	jwtSecret string,
	adminTokenTTL time.Duration,
	rcClient *revenuecat.Client,
	rcV2Client *revenuecat.V2Client,
	logger *slog.Logger,
) *AdminService {
	return &AdminService{
		pool:          pool,
		adminRepo:     adminRepo,
		jwtSecret:     jwtSecret,
		adminTokenTTL: adminTokenTTL,
		rcClient:      rcClient,
		rcV2Client:    rcV2Client,
		logger:        logger,
	}
}
```

The service methods for RC endpoints should check if the respective client is nil and return an appropriate error:
```go
func (s *AdminService) GetRCOverview(ctx context.Context) (*revenuecat.OverviewMetrics, error) {
	if s.rcV2Client == nil {
		return nil, fmt.Errorf("RevenueCat v2 not configured")
	}
	return s.rcV2Client.GetOverview(ctx)
}
```

### Service Method: GetRCChart

```go
func (s *AdminService) GetRCChart(ctx context.Context, in RCChartInput) (*revenuecat.ChartResponse, error) {
	if s.rcV2Client == nil {
		return nil, fmt.Errorf("RevenueCat v2 not configured")
	}
	return s.rcV2Client.GetChart(ctx, revenuecat.ChartParams{
		ChartName: in.ChartName,
		Range:     in.Range,
		StartDate: in.StartDate,
		EndDate:   in.EndDate,
	})
}
```

---

## Environment Variable Summary

### New Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `REVENUECAT_V2_SECRET_KEY` | No (required if RC v2 features used) | — | RevenueCat REST API v2 secret key. Obtain from RevenueCat dashboard → API Keys. |
| `REVENUECAT_PROJECT_ID` | No (required if RC v2 features used) | — | RevenueCat project ID. Found in RevenueCat dashboard → Project Settings. |
| `ADMIN_CORS_ORIGINS` | No | `http://localhost:5173` | Comma-separated list of allowed origins for admin panel CORS. Example: `http://localhost:5173,https://admin.pauza.app` |

### Updated `.env.dev.example`

Add these lines:

```
# Admin Panel CORS
ADMIN_CORS_ORIGINS=http://localhost:5173

# RevenueCat v2 API (optional — required for revenue metrics in admin panel)
REVENUECAT_V2_SECRET_KEY=
REVENUECAT_PROJECT_ID=
```

---

## Updated AdminRepository Interface

Full interface after additions:

```go
type AdminRepository interface {
	// Existing methods
	GetAdminByUsername(ctx context.Context, db DBTX, username string) (AdminRow, error)
	ListUsers(ctx context.Context, db DBTX, params ListUsersParams) ([]AdminUserRow, int, error)
	GetUserDetail(ctx context.Context, db DBTX, userID string) (AdminUserDetailRow, error)
	GetPlatformStats(ctx context.Context, db DBTX) (PlatformStatsRow, error)
	UpsertEntitlementOverride(ctx context.Context, db DBTX, params UpsertOverrideParams) error
	ListEntitlements(ctx context.Context, db DBTX, params ListEntitlementsParams) ([]AdminEntitlementListRow, int, error)
	GetActiveOverride(ctx context.Context, db DBTX, userID string, entitlement Entitlement) (OverrideRow, error)
	DeleteEntitlementOverride(ctx context.Context, db DBTX, userID string, entitlement Entitlement) error
	UserExists(ctx context.Context, db DBTX, userID string) (bool, error)

	// NEW methods
	GetUserGrowth(ctx context.Context, db DBTX, params TimeSeriesParams) ([]TimeSeriesPoint, error)
	GetActiveUsers(ctx context.Context, db DBTX, params TimeSeriesParams) ([]TimeSeriesPoint, error)
	GetUserRCAppUserID(ctx context.Context, db DBTX, userID string) (string, error)
}
```

---

## Updated AdminService Interface (as seen by handler)

The handler uses a composed interface pattern (matching the existing `AdminService` interface in `handler/admin.go`). Add two new sub-interfaces:

```go
// NEW sub-interfaces to add in handler/admin.go

type AdminTimeSeriesService interface {
	GetUserGrowth(ctx context.Context, in service.TimeSeriesInput) ([]repository.TimeSeriesPoint, error)
	GetActiveUsers(ctx context.Context, in service.TimeSeriesInput) ([]repository.TimeSeriesPoint, error)
}

type AdminRCService interface {
	GetRCOverview(ctx context.Context) (*revenuecat.OverviewMetrics, error)
	GetRCChart(ctx context.Context, in service.RCChartInput) (*revenuecat.ChartResponse, error)
	GetUserRCSubscription(ctx context.Context, in service.GetUserDetailInput) (*service.RCSubscriberOutput, error)
}

// Updated composed interface
type AdminService interface {
	AdminLoginService
	AdminUsersService
	AdminStatsService
	AdminEntitlementsService
	AdminTimeSeriesService  // NEW
	AdminRCService          // NEW
}
```

Add corresponding compile-time checks:
```go
var _ AdminTimeSeriesService = (*service.AdminService)(nil)
var _ AdminRCService = (*service.AdminService)(nil)
```

---

## Testing Strategy

### Unit Tests for New Repository Methods

Test `GetUserGrowth` and `GetActiveUsers` with integration tests (tag: `//go:build integration`):
- Test with empty database → returns empty slice
- Test with known data → returns expected time-series
- Test different granularities and ranges

### Unit Tests for V2Client

Use `httptest.NewServer` to mock RevenueCat v2 API responses:
- Test `GetOverview` with valid response
- Test `GetOverview` with cached response (verify no HTTP call on second invocation within TTL)
- Test `GetChart` with valid response
- Test `GetChart` cache keying (different chart names don't share cache)
- Test error handling (non-200 status, network failure, invalid JSON)
- Test cache expiry (after TTL, a new HTTP call is made)

### Unit Tests for New Handler Methods

Follow existing patterns in `internal/handler/admin_test.go`:
- Test validation (invalid chart name, missing params)
- Test success responses
- Test error propagation from service layer

### Unit Tests for New Service Methods

- Test `GetUserRCSubscription` when user has no RC ID → NOT_FOUND
- Test `GetUserRCSubscription` when RC API returns 404 → appropriate error
- Test `GetRCOverview` / `GetRCChart` when V2Client is nil → INTERNAL_ERROR
- Test transform functions for RC subscriber data

---

## Deployment Guide

Your pauza-server uses a fully automated CI/CD pipeline (GitHub Actions → Docker build → SSH deploy). Here's what you need to do to get these new endpoints live.

### What the CI/CD Pipeline Already Handles

Your `.github/workflows/ci.yml` pipeline does this automatically on push to `main`:

1. Runs unit tests (`go test -race -count=1 ./...`)
2. Runs integration tests (with Postgres 16 + Redis 7 containers)
3. Builds Docker image and pushes to GHCR (`ghcr.io/pauzaa/pauza-server:latest`)
4. SSHs into the production server, pulls the new image, runs migrations, restarts the API

**Because these changes involve no database migrations** (no new tables or columns — the time-series queries use existing tables, and the RC proxy endpoints don't touch the DB), the pipeline will handle everything automatically. The new Go code, routes, and dependencies are all baked into the Docker image at build time.

### What You Must Do Manually (One-Time Setup)

**Step 1: Add new environment variables to `.env.prod` on the server**

SSH into your production server and edit the `.env.prod` file at the deploy path:

```bash
ssh <DEPLOY_USER>@<DEPLOY_HOST>
cd <DEPLOY_PATH>   # e.g., /home/deploy/pauza
nano .env.prod      # or vim, whatever you prefer
```

Add these lines:

```bash
# Admin Panel CORS — add your admin panel's production URL
ADMIN_CORS_ORIGINS=https://admin.pauza.dev,http://localhost:5173

# RevenueCat v2 API — required for revenue metrics in admin panel
REVENUECAT_V2_SECRET_KEY=<your-rc-v2-secret-key>
REVENUECAT_PROJECT_ID=<your-rc-project-id>
```

The RC v2 endpoints gracefully return "RevenueCat v2 not configured" errors (not crash) when keys are missing, so this is safe to deploy without them — but the RC admin endpoints won't return real data until configured.

**`ADMIN_CORS_ORIGINS` — skip for now.** The admin panel is not built yet, so no browser will be making CORS requests to the API. The default value (`http://localhost:5173`) is fine — it only allows local dev access. You'll update this when the admin panel is actually deployed to production.

**Step 2: Get the RevenueCat v2 credentials**

1. Go to [RevenueCat Dashboard](https://app.revenuecat.com)
2. Navigate to your project → **Project Settings**
3. Copy the **Project ID** (displayed at the top) → use as `REVENUECAT_PROJECT_ID`
4. Go to **API Keys** → generate or copy the **REST API v2 secret key**
   - Ensure it has permissions: `charts_metrics:overview:read`, `charts_metrics:charts:read`
   - Use this as `REVENUECAT_V2_SECRET_KEY`

**Step 3: Merge to `main`**

Merge the feature branch into `main`. The CI/CD pipeline will automatically:
- Run all tests (existing + new)
- Build the Docker image with the new endpoints compiled in
- Deploy to production
- The server reads the new env vars from `.env.prod` on startup

No database migrations are needed — the time-series queries use existing tables, and the RC proxy endpoints don't touch the DB.

**Step 4: Verify**

After the deploy completes (check GitHub Actions for status), verify the new endpoints are accessible:

```bash
# Get an admin token first
TOKEN=$(curl -s https://api.pauza.dev/api/v1/admin/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"<admin>","password":"<password>"}' | jq -r '.access_token')

# Test time-series endpoint (no RC dependency)
curl -s https://api.pauza.dev/api/v1/admin/stats/user-growth?range=30d \
  -H "Authorization: Bearer $TOKEN" | jq .

# Test RC overview (requires valid RC v2 key)
curl -s https://api.pauza.dev/api/v1/admin/revenuecat/overview \
  -H "Authorization: Bearer $TOKEN" | jq .
```

Skip CORS verification for now — there's no admin panel deployed to test it against.

### Summary Checklist

| Step | What | When | Manual? |
|------|------|------|---------|
| 1 | Add 2 RC env vars to `.env.prod` on server | Before merge | **Yes — SSH** |
| 2 | Get RC v2 secret key + project ID from RC dashboard | Before merge | **Yes — browser** |
| 3 | Merge feature branch to `main` | After steps 1-2 | No — triggers CI/CD |
| 4 | Wait for CI/CD pipeline to complete | ~5-10 minutes | No — automatic |
| 5 | Verify endpoints work | After deploy | **Yes — curl** |

### What About the Admin Panel Hosting? (Future)

The admin panel is a separate static web app (React + Vite). It is not built yet. When ready, the recommended approach is:

**Use Vercel or Cloudflare Pages** (recommended over self-hosting on the VM):
- Zero infra work — connect the repo and it auto-deploys on push
- Free tier is more than enough for an internal admin panel
- No extra load on the production VM (keeps DB, Redis, API isolated)
- Set `VITE_API_BASE_URL=https://api.pauza.dev` as a build env var
- No DNS/TLS setup needed on your VM — the hosting provider handles it

Self-hosting on the same VM (via nginx container + Traefik) is possible but adds unnecessary maintenance for a static SPA that only you/your team uses.

**When the admin panel is deployed, you must:**

1. Update `ADMIN_CORS_ORIGINS` in `.env.prod` on the server to include the panel's production URL:
   ```bash
   ADMIN_CORS_ORIGINS=https://your-admin-panel-url.vercel.app
   ```
2. Restart the API container so it picks up the new value.

The CORS middleware is scoped only to `/api/v1/admin/*` routes (defined in `internal/server/routes.go` inside `r.Route("/admin", ...)`), so it does not affect mobile app traffic or any other services on the VM.
