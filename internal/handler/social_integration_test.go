//go:build integration

package handler_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestDevicesRegisterAndUnregister(t *testing.T) {
	baseURL, pool, sender, _ := setupTestServer(t)

	auth := startAndVerifyAuth(t, baseURL.URL, sender, "devices@example.com")

	reqBody := `{"fcm_token":"token-1","platform":"ios"}`
	req, err := http.NewRequest(http.MethodPost, baseURL.URL+"/api/v1/devices", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("creating register request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/devices: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d: %s", resp.StatusCode, string(readBody(t, resp)))
	}
	discardBody(t, resp)

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM device_tokens WHERE user_id = $1 AND fcm_token = $2`,
		auth.User.ID, "token-1",
	).Scan(&count); err != nil {
		t.Fatalf("counting registered device token: %v", err)
	}
	if count != 1 {
		t.Fatalf("registered device token count = %d, want 1", count)
	}

	unregisterReq, err := http.NewRequest(http.MethodPost, baseURL.URL+"/api/v1/devices/unregister", strings.NewReader(`{"fcm_token":"token-1"}`))
	if err != nil {
		t.Fatalf("creating unregister request: %v", err)
	}
	unregisterReq.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	unregisterReq.Header.Set("Content-Type", "application/json")

	unregisterResp, err := http.DefaultClient.Do(unregisterReq)
	if err != nil {
		t.Fatalf("POST /api/v1/devices/unregister: %v", err)
	}
	if unregisterResp.StatusCode != http.StatusOK {
		t.Fatalf("unregister status = %d: %s", unregisterResp.StatusCode, string(readBody(t, unregisterResp)))
	}
	discardBody(t, unregisterResp)

	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM device_tokens WHERE user_id = $1 AND fcm_token = $2`,
		auth.User.ID, "token-1",
	).Scan(&count); err != nil {
		t.Fatalf("counting device token after unregister: %v", err)
	}
	if count != 0 {
		t.Fatalf("device token count after unregister = %d, want 0", count)
	}
}
