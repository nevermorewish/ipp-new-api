package controller

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

type botMonitorRobotRequest struct {
	Name                 string `json:"name"`
	Type                 string `json:"type"`
	Enabled              bool   `json:"enabled"`
	FeishuAppId          string `json:"feishu_app_id"`
	FeishuAppSecret      string `json:"feishu_app_secret"`
	FeishuChatIds        string `json:"feishu_chat_ids"`
	APIURL               string `json:"api_url"`
	ConversationID       string `json:"conversation_id"`
	ConversationNickname string `json:"conversation_nickname"`
}
type botMonitorBindingRequest struct {
	Name               string `json:"name"`
	RobotId            int    `json:"robot_id"`
	ChannelIds         []int  `json:"channel_ids"`
	MinIntervalSeconds int    `json:"min_interval_seconds"`
	TriggerStatusCodes string `json:"trigger_status_codes"`
	Enabled            bool   `json:"enabled"`
}

func botMonitorRobotResponse(robot *model.BotMonitorRobot) gin.H {
	return gin.H{"id": robot.Id, "name": robot.Name, "type": robot.Type, "enabled": robot.Enabled,
		"has_api_url": strings.TrimSpace(robot.APIURL) != "", "conversation_id": robot.ConversationID, "conversation_nickname": robot.ConversationNickname, "has_feishu_secret": strings.TrimSpace(robot.FeishuAppSecret) != "",
		"feishu_chat_count": len(parseBotMonitorChatIDs(robot.FeishuChatIds)), "last_status": robot.LastStatus, "last_error": robot.LastError,
		"last_sent_at": robot.LastSentAt, "created_at": robot.CreatedAt, "updated_at": robot.UpdatedAt}
}

func ListBotMonitorRobots(c *gin.Context) {
	robots, err := model.ListBotMonitorRobots()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items := make([]gin.H, 0, len(robots))
	for _, robot := range robots {
		items = append(items, botMonitorRobotResponse(robot))
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

func CreateBotMonitorRobot(c *gin.Context) {
	var req botMonitorRobotRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiError(c, err)
		return
	}
	robot, err := buildBotMonitorRobot(req, nil)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.CreateBotMonitorRobot(robot); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, botMonitorRobotResponse(robot))
}

func UpdateBotMonitorRobot(c *gin.Context) {
	id, ok := parseBotMonitorID(c, "robot")
	if !ok {
		return
	}
	existing, err := model.GetBotMonitorRobot(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req botMonitorRobotRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiError(c, err)
		return
	}
	robot, err := buildBotMonitorRobot(req, existing)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	robot.Id = id
	robot.LastStatus, robot.LastError, robot.LastSentAt, robot.CreatedAt = existing.LastStatus, existing.LastError, existing.LastSentAt, existing.CreatedAt
	if err := model.UpdateBotMonitorRobot(robot); err != nil {
		common.ApiError(c, err)
		return
	}
	updated, err := model.GetBotMonitorRobot(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, botMonitorRobotResponse(updated))
}

func DeleteBotMonitorRobot(c *gin.Context) {
	id, ok := parseBotMonitorID(c, "robot")
	if !ok {
		return
	}
	if err := model.DeleteBotMonitorRobot(id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func TestBotMonitorRobot(c *gin.Context) {
	id, ok := parseBotMonitorID(c, "robot")
	if !ok {
		return
	}
	robot, err := model.GetBotMonitorRobot(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	if err := service.SendBotMonitorTest(ctx, robot); err != nil {
		_ = model.UpdateBotMonitorRobotDelivery(id, "failed", err.Error())
		common.ApiError(c, err)
		return
	}
	_ = model.UpdateBotMonitorRobotDelivery(id, "success", "")
	common.ApiSuccess(c, nil)
}

func ListBotMonitorRobotConversations(c *gin.Context) {
	id, ok := parseBotMonitorID(c, "robot")
	if !ok {
		return
	}
	robot, err := model.GetBotMonitorRobot(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	conversations, err := service.ListBotMonitorWeChatConversations(ctx, robot)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": conversations})
}

func QueryBotMonitorRobotConversations(c *gin.Context) {
	var req struct {
		APIURL string `json:"api_url"`
	}
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiError(c, err)
		return
	}
	apiURL := strings.TrimSpace(req.APIURL)
	if apiURL == "" {
		common.ApiError(c, fmt.Errorf("WeChat API URL is required"))
		return
	}
	if err := validateBotMonitorWeChatAPIURL(apiURL); err != nil {
		common.ApiError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	conversations, err := service.ListBotMonitorWeChatConversationsByURL(ctx, apiURL)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": conversations})
}

func ListBotMonitorBindings(c *gin.Context) {
	bindings, err := model.ListBotMonitorBindings()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	robots, err := model.ListBotMonitorRobots()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	names, types := map[int]string{}, map[int]string{}
	for _, robot := range robots {
		names[robot.Id], types[robot.Id] = robot.Name, robot.Type
	}
	items := make([]gin.H, 0, len(bindings))
	for _, binding := range bindings {
		ids, decodeErr := model.DecodeBotMonitorChannelIds(binding.ChannelIds)
		if decodeErr != nil {
			ids = []int{}
		}
		items = append(items, gin.H{"id": binding.Id, "name": binding.Name, "robot_id": binding.RobotId, "robot_name": names[binding.RobotId], "robot_type": types[binding.RobotId], "channel_ids": ids, "min_interval_seconds": binding.MinIntervalSeconds, "trigger_status_codes": binding.TriggerStatusCodes, "enabled": binding.Enabled, "created_at": binding.CreatedAt, "updated_at": binding.UpdatedAt})
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

func CreateBotMonitorBinding(c *gin.Context) {
	var req botMonitorBindingRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiError(c, err)
		return
	}
	binding, err := buildBotMonitorBinding(req, nil)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.CreateBotMonitorBinding(binding); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, binding)
}
func UpdateBotMonitorBinding(c *gin.Context) {
	id, ok := parseBotMonitorID(c, "binding")
	if !ok {
		return
	}
	existing, err := model.GetBotMonitorBinding(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req botMonitorBindingRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiError(c, err)
		return
	}
	binding, err := buildBotMonitorBinding(req, existing)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	binding.Id = id
	binding.CreatedAt = existing.CreatedAt
	if err := model.UpdateBotMonitorBinding(binding); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, binding)
}
func DeleteBotMonitorBinding(c *gin.Context) {
	id, ok := parseBotMonitorID(c, "binding")
	if !ok {
		return
	}
	if err := model.DeleteBotMonitorBinding(id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
func ListBotMonitorChannelOptions(c *gin.Context) {
	options, err := model.ListBotMonitorChannelOptions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": options})
}

func buildBotMonitorRobot(req botMonitorRobotRequest, existing *model.BotMonitorRobot) (*model.BotMonitorRobot, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 128 {
		return nil, fmt.Errorf("robot name is required and must be at most 128 characters")
	}
	robotType := strings.TrimSpace(strings.ToLower(req.Type))
	if robotType != model.BotMonitorTypeFeishu && robotType != model.BotMonitorTypeWeChat {
		return nil, fmt.Errorf("robot type must be feishu or wechat")
	}
	robot := &model.BotMonitorRobot{Name: name, Type: robotType, Enabled: req.Enabled}
	if robotType == model.BotMonitorTypeFeishu {
		appID, secret, chats := strings.TrimSpace(req.FeishuAppId), strings.TrimSpace(req.FeishuAppSecret), strings.TrimSpace(req.FeishuChatIds)
		if existing != nil && existing.Type == robotType {
			if appID == "" {
				appID = existing.FeishuAppId
			}
			if secret == "" {
				secret = existing.FeishuAppSecret
			}
			if chats == "" {
				chats = existing.FeishuChatIds
			}
		}
		if appID == "" || secret == "" || len(parseBotMonitorChatIDs(chats)) == 0 {
			return nil, fmt.Errorf("Feishu App ID, App Secret, and at least one chat ID are required")
		}
		if existing == nil || appID != existing.FeishuAppId {
			encrypted, err := common.EncryptCredential(appID)
			if err != nil {
				return nil, err
			}
			appID = encrypted
		}
		if existing == nil || secret != existing.FeishuAppSecret {
			encrypted, err := common.EncryptCredential(secret)
			if err != nil {
				return nil, err
			}
			secret = encrypted
		}
		robot.FeishuAppId, robot.FeishuAppSecret, robot.FeishuChatIds = appID, secret, chats
	} else {
		apiURL := strings.TrimSpace(req.APIURL)
		conversationID := strings.TrimSpace(req.ConversationID)
		if existing != nil && existing.Type == robotType && apiURL == "" {
			apiURL = existing.APIURL
		}
		if apiURL == "" {
			return nil, fmt.Errorf("WeChat API URL is required")
		}
		if conversationID == "" || len([]rune(conversationID)) > 255 {
			return nil, fmt.Errorf("WeChat conversation ID is required and must be at most 255 characters")
		}
		conversationNickname := strings.TrimSpace(req.ConversationNickname)
		if len([]rune(conversationNickname)) > 255 {
			return nil, fmt.Errorf("WeChat conversation nickname must be at most 255 characters")
		}
		if existing != nil && existing.Type == robotType && conversationNickname == "" && conversationID == existing.ConversationID {
			conversationNickname = existing.ConversationNickname
		}
		if existing == nil || apiURL != existing.APIURL {
			if err := validateBotMonitorWeChatAPIURL(apiURL); err != nil {
				return nil, err
			}
			encrypted, err := common.EncryptCredential(apiURL)
			if err != nil {
				return nil, err
			}
			apiURL = encrypted
		}
		robot.APIURL, robot.ConversationID, robot.ConversationNickname = apiURL, conversationID, conversationNickname
	}
	return robot, nil
}

func buildBotMonitorBinding(req botMonitorBindingRequest, existing *model.BotMonitorBinding) (*model.BotMonitorBinding, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 128 {
		return nil, fmt.Errorf("binding name is required and must be at most 128 characters")
	}
	if _, err := model.GetBotMonitorRobot(req.RobotId); err != nil {
		return nil, fmt.Errorf("robot not found: %w", err)
	}
	if err := model.ValidateBotMonitorChannelIds(req.ChannelIds); err != nil {
		return nil, err
	}
	if req.MinIntervalSeconds < 0 || req.MinIntervalSeconds > 86400 {
		return nil, fmt.Errorf("minimum interval must be between 0 and 86400 seconds")
	}
	if strings.TrimSpace(req.TriggerStatusCodes) != "" {
		if _, err := operation_setting.ParseHTTPStatusCodeRanges(req.TriggerStatusCodes); err != nil {
			return nil, err
		}
	}
	encoded, err := model.EncodeBotMonitorChannelIds(req.ChannelIds)
	if err != nil {
		return nil, err
	}
	return &model.BotMonitorBinding{Name: name, RobotId: req.RobotId, ChannelIds: encoded, MinIntervalSeconds: req.MinIntervalSeconds, TriggerStatusCodes: strings.TrimSpace(req.TriggerStatusCodes), Enabled: req.Enabled}, nil
}

func validateBotMonitorWeChatAPIURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("WeChat API URL must be an absolute HTTP or HTTPS URL without credentials or a fragment")
	}
	return nil
}
func parseBotMonitorChatIDs(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' || r == ';' || r == ' ' || r == '\t' })
	seen := map[string]struct{}{}
	result := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			if _, ok := seen[part]; !ok {
				seen[part] = struct{}{}
				result = append(result, part)
			}
		}
	}
	return result
}
func parseBotMonitorID(c *gin.Context, name string) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if id <= 0 || err != nil {
		common.ApiError(c, fmt.Errorf("invalid %s id", name))
		return 0, false
	}
	return id, true
}
