package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProbeDaemon checks if the gateway daemon is alive and ready by calling GET /ready.
func ProbeDaemon(baseURL string, timeoutMs int) bool {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return false
	}
	if timeoutMs <= 0 {
		timeoutMs = 500
	}
	client := &http.Client{
		Timeout: time.Duration(timeoutMs) * time.Millisecond,
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/ready", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// GatewayClient provides typed access to the Dezuxk Gateway REST APIs.
type GatewayClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewGatewayClient creates a new GatewayClient instance.
func NewGatewayClient(baseURL string, token string) *GatewayClient {
	baseURL = strings.TrimRight(baseURL, "/")
	return &GatewayClient{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *GatewayClient) doJSON(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	reqURL := fmt.Sprintf("%s%s", c.BaseURL, path)
	req, err := http.NewRequestWithContext(ctx, method, reqURL, reader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errObj struct {
			Error any `json:"error"`
		}
		if json.Unmarshal(respBytes, &errObj) == nil && errObj.Error != nil {
			return nil, fmt.Errorf("server error (%d): %v", resp.StatusCode, errObj.Error)
		}
		return nil, fmt.Errorf("server error (%d): %s", resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}

func toMapSlice(val any) []map[string]any {
	var result []map[string]any
	if slice, ok := val.([]any); ok {
		for _, item := range slice {
			if m, ok := item.(map[string]any); ok {
				result = append(result, m)
			}
		}
	} else if slice, ok := val.([]map[string]any); ok {
		return slice
	}
	return result
}

// GetOverview returns server operational status and statistics.
func (c *GatewayClient) GetOverview(ctx context.Context) (map[string]any, error) {
	respBytes, err := c.doJSON(ctx, http.MethodGet, "/v1/admin/overview", nil)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := json.Unmarshal(respBytes, &res); err != nil {
		return nil, fmt.Errorf("failed to parse overview response: %w", err)
	}
	return res, nil
}

// ListProfiles lists active profiles from the gateway.
func (c *GatewayClient) ListProfiles(ctx context.Context) ([]map[string]any, error) {
	respBytes, err := c.doJSON(ctx, http.MethodGet, "/v1/profiles", nil)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := json.Unmarshal(respBytes, &res); err == nil {
		if profiles, exists := res["profiles"]; exists {
			return toMapSlice(profiles), nil
		}
	}
	var list []map[string]any
	if err := json.Unmarshal(respBytes, &list); err == nil {
		return list, nil
	}
	return nil, nil
}

// CreateProfile registers a new profile directory on the gateway.
func (c *GatewayClient) CreateProfile(ctx context.Context, id string) error {
	body := map[string]any{"id": id}
	_, err := c.doJSON(ctx, http.MethodPost, "/v1/profiles", body)
	return err
}

// LaunchChrome opens a dedicated Chrome browser instance for the profile.
func (c *GatewayClient) LaunchChrome(ctx context.Context, id string, cdpPort int, headless bool) error {
	body := map[string]any{
		"cdp_port": cdpPort,
		"headless": headless,
	}
	path := fmt.Sprintf("/v1/profiles/%s/launch", url.PathEscape(id))
	_, err := c.doJSON(ctx, http.MethodPost, path, body)
	return err
}

// SyncCDP synchronizes cookies from active Chrome instance via CDP.
func (c *GatewayClient) SyncCDP(ctx context.Context, id string) error {
	path := fmt.Sprintf("/v1/profiles/%s/sync", url.PathEscape(id))
	_, err := c.doJSON(ctx, http.MethodPost, path, nil)
	return err
}

// IngestCookies imports cookies directly into the profile session.
func (c *GatewayClient) IngestCookies(ctx context.Context, id string, cookies []byte) error {
	var body any
	var temp map[string]any
	if err := json.Unmarshal(cookies, &temp); err == nil {
		if _, hasCookies := temp["cookies"]; hasCookies {
			body = temp
		} else if _, hasCookieStr := temp["cookie_str"]; hasCookieStr {
			body = temp
		} else {
			body = map[string]any{"cookies": json.RawMessage(cookies)}
		}
	} else {
		var arr []any
		if errArr := json.Unmarshal(cookies, &arr); errArr == nil {
			body = map[string]any{"cookies": json.RawMessage(cookies)}
		} else {
			body = map[string]any{"cookie_str": string(cookies)}
		}
	}

	path := fmt.Sprintf("/v1/profiles/%s/ingest", url.PathEscape(id))
	_, err := c.doJSON(ctx, http.MethodPost, path, body)
	return err
}

// SetProxy updates proxy setting for a profile.
func (c *GatewayClient) SetProxy(ctx context.Context, id string, proxyURL string) error {
	body := map[string]any{"proxy": proxyURL}
	path := fmt.Sprintf("/v1/profiles/%s/proxy", url.PathEscape(id))
	_, err := c.doJSON(ctx, http.MethodPut, path, body)
	return err
}

// ListKeys returns all active virtual API keys.
func (c *GatewayClient) ListKeys(ctx context.Context) ([]map[string]any, error) {
	respBytes, err := c.doJSON(ctx, http.MethodGet, "/v1/admin/keys", nil)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := json.Unmarshal(respBytes, &res); err == nil {
		if keys, exists := res["keys"]; exists {
			return toMapSlice(keys), nil
		}
	}
	var list []map[string]any
	if err := json.Unmarshal(respBytes, &list); err == nil {
		return list, nil
	}
	return nil, nil
}

// CreateKey creates a new virtual API key.
func (c *GatewayClient) CreateKey(ctx context.Context, name string, rpm int, isAdmin bool) (map[string]any, error) {
	role := "user"
	if isAdmin {
		role = "admin"
	}
	body := map[string]any{
		"name":           name,
		"rate_limit_rpm": rpm,
		"role":           role,
	}
	respBytes, err := c.doJSON(ctx, http.MethodPost, "/v1/admin/keys", body)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := json.Unmarshal(respBytes, &res); err != nil {
		return nil, fmt.Errorf("failed to parse key creation response: %w", err)
	}
	return res, nil
}

// RevokeKey revokes a virtual API key by id.
func (c *GatewayClient) RevokeKey(ctx context.Context, id string) error {
	path := fmt.Sprintf("/v1/admin/keys/%s", url.PathEscape(id))
	_, err := c.doJSON(ctx, http.MethodDelete, path, nil)
	return err
}

// GetFlowCredits retrieves credit balance for Flow.
func (c *GatewayClient) GetFlowCredits(ctx context.Context, accountID string) (map[string]any, error) {
	path := "/v1/flow/credits"
	if accountID != "" {
		path += "?account_id=" + url.QueryEscape(accountID)
	}
	respBytes, err := c.doJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := json.Unmarshal(respBytes, &res); err != nil {
		return nil, fmt.Errorf("failed to parse credits response: %w", err)
	}
	return res, nil
}

// GetGeminiUsage retrieves usage and tier information for Gemini.
func (c *GatewayClient) GetGeminiUsage(ctx context.Context, accountID string) (map[string]any, error) {
	path := "/v1/gemini/usage"
	if accountID != "" {
		path += "?account_id=" + url.QueryEscape(accountID)
	}
	respBytes, err := c.doJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := json.Unmarshal(respBytes, &res); err != nil {
		return nil, fmt.Errorf("failed to parse usage response: %w", err)
	}
	return res, nil
}

// GetAlerts retrieves system alerts.
func (c *GatewayClient) GetAlerts(ctx context.Context) ([]map[string]any, error) {
	respBytes, err := c.doJSON(ctx, http.MethodGet, "/v1/alerts", nil)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := json.Unmarshal(respBytes, &res); err == nil {
		if alerts, exists := res["alerts"]; exists {
			return toMapSlice(alerts), nil
		}
	}
	var list []map[string]any
	if err := json.Unmarshal(respBytes, &list); err == nil {
		return list, nil
	}
	return nil, nil
}

// ClearAlerts clears system alerts.
func (c *GatewayClient) ClearAlerts(ctx context.Context, accountID string) error {
	path := "/v1/alerts/clear"
	if accountID != "" {
		path += "?account_id=" + url.QueryEscape(accountID)
	}
	_, err := c.doJSON(ctx, http.MethodPost, path, nil)
	return err
}
