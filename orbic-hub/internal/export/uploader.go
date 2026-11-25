package export

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

const wigleUploadURL = "https://api.wigle.net/api/v2/file/upload"

// UploadResult summarizes a remote upload attempt.
type UploadResult struct {
	Target     string `json:"target"`
	StatusCode int    `json:"status_code"`
	Rows       int    `json:"rows"`
	Filename   string `json:"filename"`
	Response   string `json:"response"`
}

// UploadWiGLE uploads a WiGLE-1.6 CSV to wigle.net using either the pre-encoded
// API token from wigle.net/account or raw "APIName:APIToken" credentials.
func UploadWiGLE(ctx context.Context, csvData []byte, filename string, rows int, authToken string, donate bool) (*UploadResult, error) {
	authToken = strings.TrimSpace(authToken)
	if authToken == "" {
		return nil, fmt.Errorf("wigle_auth_token is not configured")
	}
	if rows == 0 {
		return nil, fmt.Errorf("no GPS-tagged observations available to upload")
	}

	// If user pasted "APIName:APIToken", base64-encode it automatically.
	encoded := authToken
	if strings.Contains(authToken, ":") {
		encoded = base64.StdEncoding.EncodeToString([]byte(authToken))
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(csvData); err != nil {
		return nil, err
	}
	donateVal := "false"
	if donate {
		donateVal = "true"
	}
	_ = mw.WriteField("donate", donateVal)
	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wigleUploadURL, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Basic "+encoded)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	res := &UploadResult{
		Target:     "WiGLE",
		StatusCode: resp.StatusCode,
		Rows:       rows,
		Filename:   filename,
		Response:   strings.TrimSpace(string(respBytes)),
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return res, fmt.Errorf("WiGLE returned HTTP %d: %s", resp.StatusCode, res.Response)
	}
	return res, nil
}

// UploadWDGWars posts a WiGLE-1.6 CSV to Watch Dogs Go Wars (wdgwars.pl).
func UploadWDGWars(ctx context.Context, csvData []byte, filename string, rows int, endpointURL, token string) (*UploadResult, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("wdgwars_token is not configured")
	}
	if endpointURL == "" {
		endpointURL = "https://wdgwars.pl/api/upload-csv"
	}
	if rows == 0 {
		return nil, fmt.Errorf("no GPS-tagged observations available to upload")
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(csvData); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-API-Key", token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	res := &UploadResult{
		Target:     "WDGWars",
		StatusCode: resp.StatusCode,
		Rows:       rows,
		Filename:   filename,
		Response:   strings.TrimSpace(string(respBytes)),
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return res, fmt.Errorf("WDGWars returned HTTP %d: %s", resp.StatusCode, res.Response)
	}
	return res, nil
}

// UploadWardrift authenticates to wardrift.net using username + password
// (supporting session cookies and/or returned JWT tokens) and uploads a WiGLE-1.6 CSV.
func UploadWardrift(ctx context.Context, csvData []byte, filename string, rows int, baseURL, loginPath, uploadPath, username, password string) (*UploadResult, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, fmt.Errorf("wardrift_username and wardrift_password must be configured")
	}
	if rows == 0 {
		return nil, fmt.Errorf("no GPS-tagged observations available to upload")
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://wardrift.net"
	}
	if loginPath == "" {
		loginPath = "/api/login"
	}
	if uploadPath == "" {
		uploadPath = "/api/upload"
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
	}

	// Step 1: Authenticate (try JSON POST first, fallback to standard HTML form POST).
	loginURL := baseURL + loginPath
	bearerToken, err := wardriftLogin(ctx, client, loginURL, username, password)
	if err != nil {
		return nil, fmt.Errorf("Wardrift login failed: %w", err)
	}

	// Step 2: Upload WiGLE-1.6 CSV multipart payload using session cookies + optional Bearer token.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(csvData); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+uploadPath, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json, text/html")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	res := &UploadResult{
		Target:     "Wardrift",
		StatusCode: resp.StatusCode,
		Rows:       rows,
		Filename:   filename,
		Response:   strings.TrimSpace(string(respBytes)),
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return res, fmt.Errorf("Wardrift upload returned HTTP %d: %s", resp.StatusCode, res.Response)
	}
	return res, nil
}

func wardriftLogin(ctx context.Context, client *http.Client, loginURL, username, password string) (string, error) {
	// Attempt 1: JSON login payload
	jsonBody, _ := json.Marshal(map[string]string{
		"username": username,
		"email":    username,
		"password": password,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err == nil {
		respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return extractBearerToken(respBytes), nil
		}
	}

	// Attempt 2: Form-urlencoded login (standard session cookie web login)
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp2, err := client.Do(req2)
	if err != nil {
		return "", err
	}
	respBytes2, _ := io.ReadAll(io.LimitReader(resp2.Body, 4096))
	resp2.Body.Close()
	if resp2.StatusCode >= 200 && resp2.StatusCode < 400 {
		return extractBearerToken(respBytes2), nil
	}
	return "", fmt.Errorf("HTTP %d: %s", resp2.StatusCode, strings.TrimSpace(string(respBytes2)))
}

func extractBearerToken(body []byte) string {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	for _, k := range []string{"token", "access_token", "jwt", "api_key"} {
		if v, ok := parsed[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
