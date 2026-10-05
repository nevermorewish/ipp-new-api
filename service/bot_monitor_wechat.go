package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const botMonitorWeChatConversationListType = 11038

type BotMonitorWeChatConversation struct {
	ConversationID string `json:"conversation_id"`
	Nickname       string `json:"nickname"`
	IsExternal     int    `json:"is_external"`
	Total          int    `json:"total"`
}

type botMonitorWeChatConversationListRequest struct {
	Data struct {
		PageNum  int `json:"page_num"`
		PageSize int `json:"page_size"`
	} `json:"data"`
	Type    int  `json:"type"`
	Client  int  `json:"client"`
	GetData bool `json:"getData"`
}

type botMonitorWeChatConversationListResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		PageNum   int                            `json:"page_num"`
		PageSize  int                            `json:"page_size"`
		Total     int                            `json:"total"`
		TotalPage int                            `json:"total_page"`
		RoomList  []BotMonitorWeChatConversation `json:"room_list"`
	} `json:"data"`
}

// ListBotMonitorWeChatConversations queries the configured WeChat gateway's
// group conversation list (ExecCommand type 11038), collecting all pages.
func ListBotMonitorWeChatConversations(ctx context.Context, robot *model.BotMonitorRobot) ([]BotMonitorWeChatConversation, error) {
	if robot == nil {
		return nil, fmt.Errorf("monitor robot is required")
	}
	if robot.Type != model.BotMonitorTypeWeChat {
		return nil, fmt.Errorf("conversation list is only available for WeChat robots")
	}
	apiURL, err := common.DecryptCredential(robot.APIURL)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt WeChat API URL: %w", err)
	}
	return ListBotMonitorWeChatConversationsByURL(ctx, apiURL)
}

// ListBotMonitorWeChatConversationsByURL queries a WeChat gateway URL without
// requiring a saved robot or conversation ID.
func ListBotMonitorWeChatConversationsByURL(ctx context.Context, apiURL string) ([]BotMonitorWeChatConversation, error) {
	if strings.TrimSpace(apiURL) == "" {
		return nil, fmt.Errorf("WeChat API URL is required")
	}

	const pageSize = 10
	all := make([]BotMonitorWeChatConversation, 0)
	for pageNum := 1; pageNum <= 100; pageNum++ {
		payload := botMonitorWeChatConversationListRequest{Type: botMonitorWeChatConversationListType, Client: 1, GetData: true}
		payload.Data.PageNum = pageNum
		payload.Data.PageSize = pageSize
		body, err := common.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal WeChat conversation request: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create WeChat conversation request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		resp, err := botMonitorWeChatHTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to query WeChat conversations: %w", err)
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("failed to read WeChat conversation response: %w", readErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("WeChat API failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
		}

		var result botMonitorWeChatConversationListResponse
		if err := common.Unmarshal(responseBody, &result); err != nil {
			return nil, fmt.Errorf("failed to decode WeChat conversation response: %w", err)
		}
		if result.Code != 200 {
			return nil, fmt.Errorf("WeChat API returned code %d: %s", result.Code, strings.TrimSpace(result.Msg))
		}
		all = append(all, result.Data.RoomList...)
		totalPages := result.Data.TotalPage
		if totalPages <= 0 && result.Data.Total > 0 {
			totalPages = (result.Data.Total + pageSize - 1) / pageSize
		}
		if totalPages <= pageNum || len(result.Data.RoomList) == 0 {
			break
		}
	}
	return all, nil
}
