package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/google/uuid"
)

type botMonitorSilenceEntry struct {
	expiresAt time.Time
	owner     string
}

var botMonitorSilenceStore = struct {
	sync.Mutex
	entries map[string]botMonitorSilenceEntry
}{entries: make(map[string]botMonitorSilenceEntry)}
var botMonitorWeChatHTTPClient = &http.Client{Timeout: 15 * time.Second}

// SendBotMonitorChannelErrorAlerts dispatches a channel error to every matching
// robot binding. Bindings are intentionally independent, so one channel can
// notify several Feishu and WeChat destinations.
func SendBotMonitorChannelErrorAlerts(ctx context.Context, alert FeishuChannelErrorAlert) error {
	bindings, err := model.ListEnabledBotMonitorBindings()
	if err != nil {
		return fmt.Errorf("failed to list robot monitoring bindings: %w", err)
	}
	intervals := make(map[int]int)
	for _, binding := range bindings {
		ids, decodeErr := model.DecodeBotMonitorChannelIds(binding.ChannelIds)
		if decodeErr != nil || !containsBotMonitorChannelID(ids, alert.ChannelID) {
			continue
		}
		if rules := strings.TrimSpace(binding.TriggerStatusCodes); rules != "" {
			ranges, parseErr := operation_setting.ParseHTTPStatusCodeRanges(rules)
			if parseErr != nil || !operation_setting.MatchHTTPStatusCodeRanges(ranges, alert.StatusCode) {
				continue
			}
		}
		if interval, exists := intervals[binding.RobotId]; !exists || binding.MinIntervalSeconds > interval {
			intervals[binding.RobotId] = binding.MinIntervalSeconds
		}
	}
	var errors []string
	for robotID, interval := range intervals {
		robot, getErr := model.GetBotMonitorRobot(robotID)
		if getErr != nil {
			errors = append(errors, fmt.Sprintf("robot #%d: %v", robotID, getErr))
			continue
		}
		if !robot.Enabled {
			continue
		}
		allowed, release := acquireBotMonitorAlertSilence(ctx, alert, robot.Id, interval)
		if !allowed {
			continue
		}
		var sendErr error
		switch robot.Type {
		case model.BotMonitorTypeFeishu:
			sendErr = sendBotMonitorFeishu(ctx, robot, alert)
		case model.BotMonitorTypeWeChat:
			sendErr = sendBotMonitorWeChat(ctx, robot, buildBotMonitorText(alert))
		default:
			sendErr = fmt.Errorf("unsupported robot type %q", robot.Type)
		}
		if sendErr != nil {
			release()
			_ = model.UpdateBotMonitorRobotDelivery(robot.Id, "failed", sendErr.Error())
			errors = append(errors, fmt.Sprintf("%s: %v", robot.Name, sendErr))
			continue
		}
		_ = model.UpdateBotMonitorRobotDelivery(robot.Id, "success", "")
	}
	if len(errors) > 0 {
		return fmt.Errorf("failed to send one or more robot alerts: %s", strings.Join(errors, "; "))
	}
	return nil
}

func SendBotMonitorTest(ctx context.Context, robot *model.BotMonitorRobot) error {
	if robot == nil {
		return fmt.Errorf("monitor robot is required")
	}
	alert := FeishuChannelErrorAlert{ChannelID: 0, ChannelName: "test", StatusCode: 500, Message: "Huanxing API monitor notification connection is working."}
	switch robot.Type {
	case model.BotMonitorTypeFeishu:
		return sendBotMonitorFeishu(ctx, robot, alert)
	case model.BotMonitorTypeWeChat:
		return sendBotMonitorWeChat(ctx, robot, "[Monitor test]\n"+buildBotMonitorText(alert))
	default:
		return fmt.Errorf("unsupported robot type %q", robot.Type)
	}
}

func sendBotMonitorTextAlert(ctx context.Context, robot *model.BotMonitorRobot, title, text string) error {
	if robot == nil {
		return fmt.Errorf("monitor robot is required")
	}
	switch robot.Type {
	case model.BotMonitorTypeFeishu:
		return sendBotMonitorFeishuText(ctx, robot, title, text)
	case model.BotMonitorTypeWeChat:
		return sendBotMonitorWeChat(ctx, robot, fmt.Sprintf("# %s\n%s", title, text))
	default:
		return fmt.Errorf("unsupported robot type %q", robot.Type)
	}
}

func sendBotMonitorFeishuText(ctx context.Context, robot *model.BotMonitorRobot, title, text string) error {
	appID, err := common.DecryptCredential(robot.FeishuAppId)
	if err != nil {
		return fmt.Errorf("failed to decrypt Feishu app ID: %w", err)
	}
	secret, err := common.DecryptCredential(robot.FeishuAppSecret)
	if err != nil {
		return fmt.Errorf("failed to decrypt Feishu app secret: %w", err)
	}
	chatIDs := parseFeishuChatIDs(robot.FeishuChatIds)
	if strings.TrimSpace(appID) == "" || strings.TrimSpace(secret) == "" || len(chatIDs) == 0 {
		return fmt.Errorf("Feishu robot configuration is incomplete")
	}
	token, err := getFeishuTenantAccessToken(ctx, appID, secret)
	if err != nil {
		return err
	}
	cardBytes, err := common.Marshal(map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": "orange",
			"title":    map[string]string{"tag": "plain_text", "content": title},
		},
		"elements": []map[string]string{{
			"tag": "markdown", "content": truncateFeishuMarkdown(text, 2800),
		}},
	})
	if err != nil {
		return fmt.Errorf("failed to marshal Feishu monitor card: %w", err)
	}
	var errors []string
	for _, chatID := range chatIDs {
		if err := sendFeishuCard(ctx, token, chatID, string(cardBytes)); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", chatID, err))
		}
	}
	if len(errors) > 0 {
		return fmt.Errorf("failed to send Feishu notifications: %s", strings.Join(errors, "; "))
	}
	return nil
}

func sendBotMonitorFeishu(ctx context.Context, robot *model.BotMonitorRobot, alert FeishuChannelErrorAlert) error {
	appID, err := common.DecryptCredential(robot.FeishuAppId)
	if err != nil {
		return fmt.Errorf("failed to decrypt Feishu app ID: %w", err)
	}
	secret, err := common.DecryptCredential(robot.FeishuAppSecret)
	if err != nil {
		return fmt.Errorf("failed to decrypt Feishu app secret: %w", err)
	}
	chatIDs := parseFeishuChatIDs(robot.FeishuChatIds)
	if strings.TrimSpace(appID) == "" || strings.TrimSpace(secret) == "" || len(chatIDs) == 0 {
		return fmt.Errorf("Feishu robot configuration is incomplete")
	}
	token, err := getFeishuTenantAccessToken(ctx, appID, secret)
	if err != nil {
		return err
	}
	content, err := buildFeishuChannelErrorCard(alert)
	if err != nil {
		return err
	}
	var errors []string
	for _, chatID := range chatIDs {
		if err := sendFeishuCard(ctx, token, chatID, content); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", chatID, err))
		}
	}
	if len(errors) > 0 {
		return fmt.Errorf("failed to send Feishu notifications: %s", strings.Join(errors, "; "))
	}
	return nil
}

type botMonitorWeChatExecCommandRequest struct {
	Data struct {
		ConversationID string `json:"conversation_id"`
		Content        string `json:"content"`
	} `json:"data"`
	Type   int `json:"type"`
	Client int `json:"client"`
}

func sendBotMonitorWeChat(ctx context.Context, robot *model.BotMonitorRobot, text string) error {
	apiURL, err := common.DecryptCredential(robot.APIURL)
	if err != nil {
		return fmt.Errorf("failed to decrypt WeChat API URL: %w", err)
	}
	if strings.TrimSpace(apiURL) == "" {
		return fmt.Errorf("WeChat API URL is required")
	}
	conversationID := strings.TrimSpace(robot.ConversationID)
	if conversationID == "" {
		return fmt.Errorf("WeChat conversation ID is required")
	}
	payload := botMonitorWeChatExecCommandRequest{Type: 11029, Client: 1}
	payload.Data.ConversationID = conversationID
	payload.Data.Content = truncateBotMonitorText(text, 3000)
	body, err := common.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal WeChat message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create WeChat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := botMonitorWeChatHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send WeChat message: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("failed to read WeChat response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("WeChat API failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func containsBotMonitorChannelID(ids []int, channelID int) bool {
	for _, id := range ids {
		if id == channelID {
			return true
		}
	}
	return false
}

func acquireBotMonitorAlertSilence(ctx context.Context, alert FeishuChannelErrorAlert, robotID, intervalSeconds int) (bool, func()) {
	if intervalSeconds <= 0 {
		return true, func() {}
	}
	fingerprint := fmt.Sprintf("%d:%d:%d:%s:%s", robotID, alert.ChannelID, alert.StatusCode, alert.ErrorType, alert.ErrorCode)
	hash := sha256.Sum256([]byte(fingerprint))
	key := fmt.Sprintf("huanxing:bot-monitor:silence:v1:%x", hash[:16])
	owner := uuid.NewString()
	ttl := time.Duration(intervalSeconds) * time.Second
	if common.RedisEnabled && common.RDB != nil {
		acquired, err := common.RDB.SetNX(ctx, key, owner, ttl).Result()
		if err == nil {
			if !acquired {
				return false, func() {}
			}
			return true, func() {
				const script = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`
				_ = common.RDB.Eval(context.Background(), script, []string{key}, owner).Err()
			}
		}
	}
	now := time.Now()
	botMonitorSilenceStore.Lock()
	defer botMonitorSilenceStore.Unlock()
	for savedKey, entry := range botMonitorSilenceStore.entries {
		if !entry.expiresAt.After(now) {
			delete(botMonitorSilenceStore.entries, savedKey)
		}
	}
	if entry, exists := botMonitorSilenceStore.entries[key]; exists && entry.expiresAt.After(now) {
		return false, func() {}
	}
	botMonitorSilenceStore.entries[key] = botMonitorSilenceEntry{expiresAt: now.Add(ttl), owner: owner}
	return true, func() {
		botMonitorSilenceStore.Lock()
		defer botMonitorSilenceStore.Unlock()
		if entry, exists := botMonitorSilenceStore.entries[key]; exists && entry.owner == owner {
			delete(botMonitorSilenceStore.entries, key)
		}
	}
}

func buildBotMonitorText(alert FeishuChannelErrorAlert) string {
	lines := []string{fmt.Sprintf("# Channel error #%d %s", alert.ChannelID, alert.ChannelName), fmt.Sprintf("> Status: %d", alert.StatusCode), fmt.Sprintf("> Error: %s / %s", alert.ErrorType, alert.ErrorCode)}
	if alert.ModelName != "" {
		lines = append(lines, fmt.Sprintf("> Model: %s", alert.ModelName))
	}
	if alert.RequestPath != "" {
		lines = append(lines, fmt.Sprintf("> Path: %s", alert.RequestPath))
	}
	if alert.RequestID != "" {
		lines = append(lines, fmt.Sprintf("> Request ID: %s", alert.RequestID))
	}
	if alert.Message != "" {
		lines = append(lines, "", truncateBotMonitorText(alert.Message, 2200))
	}
	lines = append(lines, fmt.Sprintf("> Time: %s", time.Now().Format("2006-01-02 15:04:05")))
	return strings.Join(lines, "\n")
}

func truncateBotMonitorText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}
