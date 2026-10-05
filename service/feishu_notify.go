package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const (
	feishuTenantAccessTokenURL = "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal"
	feishuMessageCreateURL     = "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=chat_id"
)

type FeishuChannelErrorAlert struct {
	ChannelID      int
	ChannelName    string
	ChannelType    int
	StatusCode     int
	ErrorType      types.ErrorType
	ErrorCode      types.ErrorCode
	Message        string
	RequestID      string
	UpstreamID     string
	ModelName      string
	UserGroup      string
	TokenName      string
	RequestPath    string
	UseChannels    []string
	IsMultiKey     bool
	MultiKeyIndex  int
	RetryRemaining int
}

type feishuTokenResponse struct {
	Code              int    `json:"code"`
	Msg               string `json:"msg"`
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int    `json:"expire"`
}

type feishuAPIResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

var feishuHTTPClient = &http.Client{Timeout: 15 * time.Second}

func parseFeishuChatIDs(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ';' || r == ' ' || r == '\t'
	})
	chatIDs := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		chatID := strings.TrimSpace(part)
		if chatID == "" {
			continue
		}
		if _, ok := seen[chatID]; ok {
			continue
		}
		seen[chatID] = struct{}{}
		chatIDs = append(chatIDs, chatID)
	}
	return chatIDs
}

func getFeishuTenantAccessToken(ctx context.Context, appID, appSecret string) (string, error) {
	body, err := common.Marshal(map[string]string{
		"app_id":     appID,
		"app_secret": appSecret,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal feishu token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, feishuTenantAccessTokenURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create feishu token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := feishuHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request feishu tenant access token: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("failed to read feishu token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("feishu token request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var tokenResp feishuTokenResponse
	if err := common.Unmarshal(respBody, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode feishu token response: %w", err)
	}
	if tokenResp.Code != 0 {
		return "", fmt.Errorf("feishu token request failed: code=%d, msg=%s", tokenResp.Code, tokenResp.Msg)
	}
	if tokenResp.TenantAccessToken == "" {
		return "", fmt.Errorf("feishu token response missing tenant_access_token")
	}
	return tokenResp.TenantAccessToken, nil
}

func sendFeishuCard(ctx context.Context, token, chatID, content string) error {
	body, err := common.Marshal(map[string]any{
		"receive_id": chatID,
		"msg_type":   "interactive",
		"content":    content,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal feishu message request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, feishuMessageCreateURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create feishu message request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := feishuHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send feishu message: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("failed to read feishu message response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("feishu message request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var apiResp feishuAPIResponse
	if err := common.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("failed to decode feishu message response: %w", err)
	}
	if apiResp.Code != 0 {
		return fmt.Errorf("feishu message request failed: code=%d, msg=%s", apiResp.Code, apiResp.Msg)
	}
	return nil
}

func buildFeishuChannelErrorCard(alert FeishuChannelErrorAlert) (string, error) {
	title := fmt.Sprintf("Channel error #%d", alert.ChannelID)
	if alert.ChannelName != "" {
		title = fmt.Sprintf("Channel error #%d - %s", alert.ChannelID, alert.ChannelName)
	}

	rows := []string{
		fmt.Sprintf("**Channel:** #%d %s", alert.ChannelID, alert.ChannelName),
		fmt.Sprintf("**Status:** %d", alert.StatusCode),
		fmt.Sprintf("**Error:** %s / %s", alert.ErrorType, alert.ErrorCode),
	}
	if alert.ChannelType != 0 {
		rows = append(rows, fmt.Sprintf("**Channel type:** %d", alert.ChannelType))
	}
	if alert.ModelName != "" {
		rows = append(rows, fmt.Sprintf("**Model:** %s", alert.ModelName))
	}
	if alert.UserGroup != "" {
		rows = append(rows, fmt.Sprintf("**Group:** %s", alert.UserGroup))
	}
	if alert.TokenName != "" {
		rows = append(rows, fmt.Sprintf("**Token:** %s", alert.TokenName))
	}
	if alert.RequestPath != "" {
		rows = append(rows, fmt.Sprintf("**Path:** %s", alert.RequestPath))
	}
	if alert.RequestID != "" {
		rows = append(rows, fmt.Sprintf("**Request ID:** %s", alert.RequestID))
	}
	if alert.UpstreamID != "" {
		rows = append(rows, fmt.Sprintf("**Upstream request ID:** %s", alert.UpstreamID))
	}
	if len(alert.UseChannels) > 0 {
		rows = append(rows, fmt.Sprintf("**Use channels:** %s", strings.Join(alert.UseChannels, " -> ")))
	}
	if alert.IsMultiKey {
		rows = append(rows, fmt.Sprintf("**Multi-key index:** %d", alert.MultiKeyIndex))
	}
	rows = append(rows, fmt.Sprintf("**Time:** %s", time.Now().Format(time.RFC3339)))
	if alert.Message != "" {
		rows = append(rows, "", "**Message:**", truncateFeishuMarkdown(alert.Message, 2500))
	}

	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": "red",
			"title": map[string]string{
				"tag":     "plain_text",
				"content": title,
			},
		},
		"elements": []map[string]string{
			{
				"tag":     "markdown",
				"content": strings.Join(rows, "\n"),
			},
		},
	}

	cardBytes, err := common.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("failed to marshal feishu card: %w", err)
	}
	return string(cardBytes), nil
}

func truncateFeishuMarkdown(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	if maxLen <= 20 {
		return text[:maxLen]
	}
	return text[:maxLen-20] + "\n... (truncated)"
}
