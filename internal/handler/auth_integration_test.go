//go:build integration

package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/mail"
)

func TestIntegration_AuthStartReturnsOTPRequiredAndSendsLoginOTP(t *testing.T) {
	ts, _, sender, _ := setupTestServer(t)

	email := "user@example.com"
	startAuthChallenge(t, ts.URL, email)

	if otp := sender.lastOTP(email, mail.PurposeAuthLogin); otp == "" {
		t.Fatal("expected auth login OTP to be sent")
	}
}

func TestIntegration_AuthVerifyCreatesUserAndReturnsProfile(t *testing.T) {
	ts, pool, sender, _ := setupTestServer(t)

	email := "user@example.com"
	auth := startAndVerifyAuth(t, ts.URL, sender, email)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email) = lower($1)`, email).Scan(&count); err != nil {
		t.Fatalf("counting users: %v", err)
	}
	if count != 1 {
		t.Fatalf("users for %q = %d, want 1", email, count)
	}

	assertAuthEnvelope(t, auth, email)
}

func TestIntegration_AuthVerifyExistingUserSignsIntoSameAccount(t *testing.T) {
	ts, pool, sender, _ := setupTestServer(t)

	email := "repeat@example.com"
	first := startAndVerifyAuth(t, ts.URL, sender, email)
	second := startAndVerifyAuth(t, ts.URL, sender, email)

	if first.User.ID != second.User.ID {
		t.Fatalf("user id changed across passwordless sign-ins: first=%q second=%q", first.User.ID, second.User.ID)
	}
	if first.RefreshToken == second.RefreshToken {
		t.Fatal("expected a fresh refresh token on second sign-in")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email) = lower($1)`, email).Scan(&count); err != nil {
		t.Fatalf("counting users: %v", err)
	}
	if count != 1 {
		t.Fatalf("users for %q = %d, want 1", email, count)
	}
}

func TestIntegration_AuthVerifyInvalidOTPReturnsUnauthorized(t *testing.T) {
	ts, _, sender, _ := setupTestServer(t)

	email := "invalid@example.com"
	startAuthChallenge(t, ts.URL, email)
	if otp := sender.lastOTP(email, mail.PurposeAuthLogin); otp == "" {
		t.Fatal("expected auth login OTP to be sent")
	}

	resp := postJSON(t, ts.URL+"/api/v1/auth/verify", authVerifyRequest{
		Email: email,
		OTP:   "000000",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("verify status = %d, want 401", resp.StatusCode)
	}

	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeUnauthorized {
		t.Fatalf("error.code = %q, want %q", errResp.Error.Code, apperror.CodeUnauthorized)
	}
	if errResp.Error.Message != "Invalid or expired OTP" {
		t.Fatalf("error.message = %q, want %q", errResp.Error.Message, "Invalid or expired OTP")
	}
}

func TestIntegration_AuthRefreshRotatesTokensAndDetectsReuse(t *testing.T) {
	ts, _, sender, _ := setupTestServer(t)

	auth := startAndVerifyAuth(t, ts.URL, sender, "refresh@example.com")

	refreshResp := postJSON(t, ts.URL+"/api/v1/auth/refresh", authRefreshRequest{
		RefreshToken: auth.RefreshToken,
	})
	if refreshResp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d: %s", refreshResp.StatusCode, string(readBody(t, refreshResp)))
	}

	var rotated struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	decodeJSON(t, refreshResp, &rotated)
	if rotated.AccessToken == "" || rotated.RefreshToken == "" {
		t.Fatal("expected rotated tokens")
	}
	if rotated.RefreshToken == auth.RefreshToken {
		t.Fatal("expected refresh token rotation")
	}

	reuseResp := postJSON(t, ts.URL+"/api/v1/auth/refresh", authRefreshRequest{
		RefreshToken: auth.RefreshToken,
	})
	if reuseResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reuse refresh status = %d, want 401", reuseResp.StatusCode)
	}
	discardBody(t, reuseResp)

	revokedResp := postJSON(t, ts.URL+"/api/v1/auth/refresh", authRefreshRequest{
		RefreshToken: rotated.RefreshToken,
	})
	if revokedResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("rotated token after reuse status = %d, want 401", revokedResp.StatusCode)
	}

	var errResp apperror.ErrorResponse
	decodeJSON(t, revokedResp, &errResp)
	if errResp.Error.Code != apperror.CodeUnauthorized {
		t.Fatalf("error.code = %q, want %q", errResp.Error.Code, apperror.CodeUnauthorized)
	}
}

func TestIntegration_UploadPhotoStoresFileAndReturnsConfiguredPublicURL(t *testing.T) {
	ts, _, sender, photoDir := setupTestServer(t)

	auth := startAndVerifyAuth(t, ts.URL, sender, "photo@example.com")

	resp := uploadPhoto(t, ts.URL, auth.AccessToken, "avatar.jpg", "image/jpeg", []byte{
		0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00, 0x08, 0x06, 0x06, 0x07, 0x06, 0x05, 0x08, 0x07, 0x07,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d: %s", resp.StatusCode, string(readBody(t, resp)))
	}

	var body struct {
		ProfilePictureURL string `json:"profile_picture_url"`
	}
	decodeJSON(t, resp, &body)

	const wantPrefix = "https://api.test/photos/"
	if len(body.ProfilePictureURL) <= len(wantPrefix) || body.ProfilePictureURL[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("profile_picture_url = %q, want prefix %q", body.ProfilePictureURL, wantPrefix)
	}

	filename := filepath.Base(body.ProfilePictureURL)
	if filename == "." || filename == "/" || filename == "" {
		t.Fatalf("invalid filename from profile_picture_url: %q", body.ProfilePictureURL)
	}
	if _, err := os.Stat(filepath.Join(photoDir, filename)); err != nil {
		t.Fatalf("expected uploaded file on disk: %v", err)
	}
}

func TestIntegration_NotificationPreferencesReadAndUpdate(t *testing.T) {
	ts, pool, sender, _ := setupTestServer(t)

	auth := startAndVerifyAuth(t, ts.URL, sender, "prefs@example.com")

	getReq, err := http.NewRequest(http.MethodGet, tsJoin(ts.URL, "/api/v1/me/notification-preferences"), nil)
	if err != nil {
		t.Fatalf("create get request: %v", err)
	}
	getReq.Header.Set("Authorization", "Bearer "+auth.AccessToken)

	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET notification preferences: %v", err)
	}
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d: %s", getResp.StatusCode, string(readBody(t, getResp)))
	}

	var initial struct {
		PushEnabled bool `json:"push_enabled"`
	}
	decodeJSON(t, getResp, &initial)
	if !initial.PushEnabled {
		t.Fatal("initial push_enabled = false, want true")
	}

	body, err := json.Marshal(struct {
		PushEnabled bool `json:"push_enabled"`
	}{PushEnabled: false})
	if err != nil {
		t.Fatalf("marshal patch body: %v", err)
	}
	patchReq, err := http.NewRequest(http.MethodPatch, tsJoin(ts.URL, "/api/v1/me/notification-preferences"), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create patch request: %v", err)
	}
	patchReq.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	patchReq.Header.Set("Content-Type", "application/json")

	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatalf("PATCH notification preferences: %v", err)
	}
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d: %s", patchResp.StatusCode, string(readBody(t, patchResp)))
	}

	var updated struct {
		PushEnabled bool `json:"push_enabled"`
	}
	decodeJSON(t, patchResp, &updated)
	if updated.PushEnabled {
		t.Fatal("updated push_enabled = true, want false")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stored bool
	if err := pool.QueryRow(ctx, `SELECT push_enabled FROM users WHERE id = $1`, auth.User.ID).Scan(&stored); err != nil {
		t.Fatalf("query push_enabled: %v", err)
	}
	if stored {
		t.Fatal("stored push_enabled = true, want false")
	}
}

func TestIntegration_GetMeIncludesPushEnabled(t *testing.T) {
	ts, pool, sender, _ := setupTestServer(t)

	auth := startAndVerifyAuth(t, ts.URL, sender, "me-push@example.com")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `UPDATE users SET push_enabled = false WHERE id = $1`, auth.User.ID); err != nil {
		t.Fatalf("update push_enabled: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, tsJoin(ts.URL, "/api/v1/me"), nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+auth.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /me: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /me status = %d: %s", resp.StatusCode, string(readBody(t, resp)))
	}

	var body struct {
		PushEnabled bool `json:"push_enabled"`
	}
	decodeJSON(t, resp, &body)
	if body.PushEnabled {
		t.Fatal("push_enabled = true, want false")
	}
}

func TestIntegration_PrivacyPreferencesReadAndUpdate(t *testing.T) {
	ts, pool, sender, _ := setupTestServer(t)

	auth := startAndVerifyAuth(t, ts.URL, sender, "privacy@example.com")

	getReq, err := http.NewRequest(http.MethodGet, tsJoin(ts.URL, "/api/v1/me/privacy"), nil)
	if err != nil {
		t.Fatalf("create get request: %v", err)
	}
	getReq.Header.Set("Authorization", "Bearer "+auth.AccessToken)

	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET privacy preferences: %v", err)
	}
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d: %s", getResp.StatusCode, string(readBody(t, getResp)))
	}

	var initial struct {
		LeaderboardVisible bool `json:"leaderboard_visible"`
	}
	decodeJSON(t, getResp, &initial)
	if !initial.LeaderboardVisible {
		t.Fatal("initial leaderboard_visible = false, want true")
	}

	body, err := json.Marshal(struct {
		LeaderboardVisible bool `json:"leaderboard_visible"`
	}{LeaderboardVisible: false})
	if err != nil {
		t.Fatalf("marshal patch body: %v", err)
	}
	patchReq, err := http.NewRequest(http.MethodPatch, tsJoin(ts.URL, "/api/v1/me/privacy"), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create patch request: %v", err)
	}
	patchReq.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	patchReq.Header.Set("Content-Type", "application/json")

	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatalf("PATCH privacy preferences: %v", err)
	}
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d: %s", patchResp.StatusCode, string(readBody(t, patchResp)))
	}

	var updated struct {
		LeaderboardVisible bool `json:"leaderboard_visible"`
	}
	decodeJSON(t, patchResp, &updated)
	if updated.LeaderboardVisible {
		t.Fatal("updated leaderboard_visible = true, want false")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stored bool
	if err := pool.QueryRow(ctx, `SELECT leaderboard_visible FROM users WHERE id = $1`, auth.User.ID).Scan(&stored); err != nil {
		t.Fatalf("query leaderboard_visible: %v", err)
	}
	if stored {
		t.Fatal("stored leaderboard_visible = true, want false")
	}
}

func TestIntegration_UpdateMeRejectsRemovedPreferenceFields(t *testing.T) {
	ts, _, sender, _ := setupTestServer(t)

	auth := startAndVerifyAuth(t, ts.URL, sender, "me-reject@example.com")

	for _, body := range []string{`{"leaderboard_visible":false}`, `{"push_enabled":false}`} {
		req, err := http.NewRequest(http.MethodPatch, tsJoin(ts.URL, "/api/v1/me"), bytes.NewBufferString(body))
		if err != nil {
			t.Fatalf("create patch request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+auth.AccessToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PATCH /me: %v", err)
		}
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422 for body %s", resp.StatusCode, body)
		}
		discardBody(t, resp)
	}
}

func TestIntegration_UploadPhotoAcceptsPNG(t *testing.T) {
	ts, _, sender, _ := setupTestServer(t)

	auth := startAndVerifyAuth(t, ts.URL, sender, "photo-png@example.com")

	resp := uploadPhoto(t, ts.URL, auth.AccessToken, "avatar.png", "image/png", []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d: %s", resp.StatusCode, string(readBody(t, resp)))
	}
	discardBody(t, resp)
}

func TestIntegration_UploadPhotoRejectsInvalidMime(t *testing.T) {
	ts, _, sender, _ := setupTestServer(t)

	auth := startAndVerifyAuth(t, ts.URL, sender, "photo-invalid@example.com")

	resp := uploadPhoto(t, ts.URL, auth.AccessToken, "avatar.txt", "text/plain", []byte("not an image"))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("upload status = %d, want 422: %s", resp.StatusCode, string(readBody(t, resp)))
	}

	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeValidationError {
		t.Fatalf("error.code = %q, want %q", errResp.Error.Code, apperror.CodeValidationError)
	}
}

func TestIntegration_UploadPhotoRejectsOversizeBody(t *testing.T) {
	ts, _, sender, _ := setupTestServer(t)

	auth := startAndVerifyAuth(t, ts.URL, sender, "photo-large@example.com")

	resp := uploadPhoto(t, ts.URL, auth.AccessToken, "avatar.jpg", "image/jpeg", bytes.Repeat([]byte("a"), (1<<20)+1))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("upload status = %d, want 422: %s", resp.StatusCode, string(readBody(t, resp)))
	}

	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeValidationError {
		t.Fatalf("error.code = %q, want %q", errResp.Error.Code, apperror.CodeValidationError)
	}
}

func uploadPhoto(t *testing.T, baseURL, token, filename, contentType string, payload []byte) *http.Response {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreatePart(textprotoMIMEHeader(filename, contentType))
	if err != nil {
		t.Fatalf("create multipart part: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write multipart payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, tsJoin(baseURL, "/api/v1/me/photo"), &body)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload photo: %v", err)
	}
	return resp
}

func textprotoMIMEHeader(filename, contentType string) textproto.MIMEHeader {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="photo"; filename="`+filename+`"`)
	header.Set("Content-Type", contentType)
	return header
}

func tsJoin(baseURL, path string) string {
	return baseURL + path
}
