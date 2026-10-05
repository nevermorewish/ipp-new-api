package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	BotMonitorTypeFeishu                          = "feishu"
	BotMonitorTypeWeChat                          = "wechat"
	DefaultBotMonitorFirstTokenTimeoutSeconds     = 30
	DefaultBotMonitorFirstTokenMinIntervalSeconds = 300
	MinimumBotMonitorFirstTokenTimeoutSeconds     = 1
	MaximumBotMonitorFirstTokenTimeoutSeconds     = 86400
	legacyFeishuBindingMigrationKey               = "BotMonitorLegacyFeishuBindingMigrated"
	defaultBotMonitorTriggerStatusCodes           = "400-499,500-599"
)

type BotMonitorRobot struct {
	Id                   int    `json:"id"`
	Name                 string `json:"name" gorm:"type:varchar(128);not null"`
	Type                 string `json:"type" gorm:"type:varchar(16);not null;index"`
	Enabled              bool   `json:"enabled" gorm:"not null;index"`
	FeishuAppId          string `json:"-" gorm:"column:feishu_app_id;type:text"`
	FeishuAppSecret      string `json:"-" gorm:"column:feishu_app_secret;type:text"`
	FeishuChatIds        string `json:"-" gorm:"column:feishu_chat_ids;type:text"`
	APIURL               string `json:"-" gorm:"column:webhook_url;type:text"`
	ConversationID       string `json:"conversation_id" gorm:"column:conversation_id;type:varchar(255);default:''"`
	ConversationNickname string `json:"conversation_nickname" gorm:"column:conversation_nickname;type:varchar(255);default:''"`
	LastStatus           string `json:"last_status" gorm:"type:varchar(32);default:''"`
	LastError            string `json:"last_error" gorm:"type:varchar(1024);default:''"`
	LastSentAt           int64  `json:"last_sent_at" gorm:"bigint;default:0"`
	CreatedAt            int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt            int64  `json:"updated_at" gorm:"bigint"`
}

type BotMonitorBinding struct {
	Id                 int    `json:"id"`
	Name               string `json:"name" gorm:"type:varchar(128);not null"`
	RobotId            int    `json:"robot_id" gorm:"column:robot_id;not null;index"`
	ChannelIds         string `json:"-" gorm:"column:channel_ids;type:text;not null"`
	MinIntervalSeconds int    `json:"min_interval_seconds" gorm:"column:min_interval_seconds;not null;default:0"`
	TriggerStatusCodes string `json:"trigger_status_codes" gorm:"column:trigger_status_codes;type:varchar(255);not null;default:''"`
	Enabled            bool   `json:"enabled" gorm:"not null;index"`
	CreatedAt          int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt          int64  `json:"updated_at" gorm:"bigint"`
}

// BotMonitorLatencyBinding configures first-token latency alerts for selected
// models. It is intentionally separate from BotMonitorBinding, which handles
// channel HTTP error alerts, so existing channel bindings keep their behavior.
type BotMonitorLatencyBinding struct {
	Id                       int    `json:"id"`
	Name                     string `json:"name" gorm:"type:varchar(128);not null"`
	RobotId                  int    `json:"robot_id" gorm:"column:robot_id;not null;index"`
	ModelNames               string `json:"-" gorm:"column:model_names;type:text;not null"`
	FirstTokenTimeoutSeconds int    `json:"first_token_timeout_seconds" gorm:"column:first_token_timeout_seconds;not null;default:30"`
	MinIntervalSeconds       int    `json:"min_interval_seconds" gorm:"column:min_interval_seconds;not null;default:300"`
	Enabled                  bool   `json:"enabled" gorm:"not null;index"`
	CreatedAt                int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt                int64  `json:"updated_at" gorm:"bigint"`
}

func (r *BotMonitorRobot) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	r.CreatedAt, r.UpdatedAt = now, now
	return nil
}

func (b *BotMonitorBinding) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	b.CreatedAt, b.UpdatedAt = now, now
	return nil
}

func (b *BotMonitorLatencyBinding) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	b.CreatedAt, b.UpdatedAt = now, now
	if b.FirstTokenTimeoutSeconds <= 0 {
		b.FirstTokenTimeoutSeconds = DefaultBotMonitorFirstTokenTimeoutSeconds
	}
	if b.MinIntervalSeconds < 0 {
		b.MinIntervalSeconds = DefaultBotMonitorFirstTokenMinIntervalSeconds
	}
	return nil
}

func NormalizeBotMonitorChannelIds(ids []int) []int {
	seen := make(map[int]struct{}, len(ids))
	result := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Ints(result)
	return result
}

func EncodeBotMonitorChannelIds(ids []int) (string, error) {
	b, err := common.Marshal(NormalizeBotMonitorChannelIds(ids))
	return string(b), err
}

func DecodeBotMonitorChannelIds(raw string) ([]int, error) {
	var ids []int
	if err := common.UnmarshalJsonStr(raw, &ids); err != nil {
		return nil, err
	}
	return NormalizeBotMonitorChannelIds(ids), nil
}

func NormalizeBotMonitorModelNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	result := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func EncodeBotMonitorModelNames(names []string) (string, error) {
	b, err := common.Marshal(NormalizeBotMonitorModelNames(names))
	return string(b), err
}

func DecodeBotMonitorModelNames(raw string) ([]string, error) {
	var names []string
	if strings.TrimSpace(raw) == "" {
		return []string{}, nil
	}
	if err := common.UnmarshalJsonStr(raw, &names); err != nil {
		return nil, err
	}
	return NormalizeBotMonitorModelNames(names), nil
}

func ListBotMonitorRobots() ([]*BotMonitorRobot, error) {
	var robots []*BotMonitorRobot
	err := DB.Order("id desc").Find(&robots).Error
	return robots, err
}

func ListEnabledBotMonitorRobots() ([]*BotMonitorRobot, error) {
	var robots []*BotMonitorRobot
	err := DB.Where("enabled = ?", true).Find(&robots).Error
	return robots, err
}

func GetBotMonitorRobot(id int) (*BotMonitorRobot, error) {
	var robot BotMonitorRobot
	if err := DB.First(&robot, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &robot, nil
}

func CreateBotMonitorRobot(robot *BotMonitorRobot) error { return DB.Create(robot).Error }

func UpdateBotMonitorRobot(robot *BotMonitorRobot) error {
	return DB.Model(&BotMonitorRobot{}).Where("id = ?", robot.Id).Updates(map[string]any{
		"name": robot.Name, "type": robot.Type, "enabled": robot.Enabled,
		"feishu_app_id": robot.FeishuAppId, "feishu_app_secret": robot.FeishuAppSecret,
		"feishu_chat_ids": robot.FeishuChatIds, "webhook_url": robot.APIURL,
		"conversation_id": robot.ConversationID, "conversation_nickname": robot.ConversationNickname,
		"updated_at": common.GetTimestamp(),
	}).Error
}

// MigrateLegacyWeComMonitorRobots prevents legacy group robot webhook
// records from sending with the incompatible ExecCommand protocol. Existing
// bindings are retained so administrators can reconfigure and re-enable them.
func MigrateLegacyWeComMonitorRobots() error {
	return DB.Model(&BotMonitorRobot{}).Where("type = ?", "wecom").Updates(map[string]any{
		"type":            BotMonitorTypeWeChat,
		"enabled":         false,
		"webhook_url":     "",
		"conversation_id": "",
		"last_status":     "",
		"last_error":      "",
		"updated_at":      common.GetTimestamp(),
	}).Error
}

func UpdateBotMonitorRobotDelivery(id int, status, lastError string) error {
	if len(lastError) > 1024 {
		lastError = lastError[:1024]
	}
	return DB.Model(&BotMonitorRobot{}).Where("id = ?", id).Updates(map[string]any{
		"last_status": status, "last_error": lastError, "last_sent_at": common.GetTimestamp(),
		"updated_at": common.GetTimestamp(),
	}).Error
}

func DeleteBotMonitorRobot(id int) error {
	var channelBindingCount int64
	if err := DB.Model(&BotMonitorBinding{}).Where("robot_id = ?", id).Count(&channelBindingCount).Error; err != nil {
		return err
	}
	var balanceBindingCount int64
	if err := DB.Model(&BotMonitorBalanceBinding{}).Where("robot_id = ?", id).Count(&balanceBindingCount).Error; err != nil {
		return err
	}
	var latencyBindingCount int64
	if err := DB.Model(&BotMonitorLatencyBinding{}).Where("robot_id = ?", id).Count(&latencyBindingCount).Error; err != nil {
		return err
	}
	count := channelBindingCount + balanceBindingCount + latencyBindingCount
	if count > 0 {
		return fmt.Errorf("robot is used by %d monitoring binding(s)", count)
	}
	return DB.Delete(&BotMonitorRobot{}, id).Error
}

func ListBotMonitorLatencyBindings() ([]*BotMonitorLatencyBinding, error) {
	var bindings []*BotMonitorLatencyBinding
	err := DB.Order("id desc").Find(&bindings).Error
	return bindings, err
}

func ListEnabledBotMonitorLatencyBindings() ([]*BotMonitorLatencyBinding, error) {
	var bindings []*BotMonitorLatencyBinding
	err := DB.Where("enabled = ?", true).Find(&bindings).Error
	return bindings, err
}

func GetBotMonitorLatencyBinding(id int) (*BotMonitorLatencyBinding, error) {
	var binding BotMonitorLatencyBinding
	if err := DB.First(&binding, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &binding, nil
}

func CreateBotMonitorLatencyBinding(binding *BotMonitorLatencyBinding) error {
	return DB.Create(binding).Error
}

func UpdateBotMonitorLatencyBinding(binding *BotMonitorLatencyBinding) error {
	return DB.Model(&BotMonitorLatencyBinding{}).Where("id = ?", binding.Id).Updates(map[string]any{
		"name": binding.Name, "robot_id": binding.RobotId, "model_names": binding.ModelNames,
		"first_token_timeout_seconds": binding.FirstTokenTimeoutSeconds,
		"min_interval_seconds":        binding.MinIntervalSeconds, "enabled": binding.Enabled,
		"updated_at": common.GetTimestamp(),
	}).Error
}

func DeleteBotMonitorLatencyBinding(id int) error {
	return DB.Delete(&BotMonitorLatencyBinding{}, id).Error
}

func ListBotMonitorBindings() ([]*BotMonitorBinding, error) {
	var bindings []*BotMonitorBinding
	err := DB.Order("id desc").Find(&bindings).Error
	return bindings, err
}

func ListEnabledBotMonitorBindings() ([]*BotMonitorBinding, error) {
	var bindings []*BotMonitorBinding
	err := DB.Where("enabled = ?", true).Find(&bindings).Error
	return bindings, err
}

func GetBotMonitorBinding(id int) (*BotMonitorBinding, error) {
	var binding BotMonitorBinding
	if err := DB.First(&binding, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &binding, nil
}

func CreateBotMonitorBinding(binding *BotMonitorBinding) error { return DB.Create(binding).Error }

func UpdateBotMonitorBinding(binding *BotMonitorBinding) error {
	return DB.Model(&BotMonitorBinding{}).Where("id = ?", binding.Id).Updates(map[string]any{
		"name": binding.Name, "robot_id": binding.RobotId, "channel_ids": binding.ChannelIds,
		"min_interval_seconds": binding.MinIntervalSeconds, "trigger_status_codes": binding.TriggerStatusCodes,
		"enabled": binding.Enabled, "updated_at": common.GetTimestamp(),
	}).Error
}

func DeleteBotMonitorBinding(id int) error { return DB.Delete(&BotMonitorBinding{}, id).Error }

func ValidateBotMonitorChannelIds(ids []int) error {
	ids = NormalizeBotMonitorChannelIds(ids)
	if len(ids) == 0 {
		return errors.New("select at least one channel")
	}
	var count int64
	if err := DB.Model(&Channel{}).Where("id IN ?", ids).Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(ids)) {
		return errors.New("one or more selected channels do not exist")
	}
	return nil
}

type BotMonitorChannelOption struct {
	Id     int    `json:"id"`
	Name   string `json:"name"`
	Type   int    `json:"type"`
	Status int    `json:"status"`
}

func ListBotMonitorChannelOptions() ([]BotMonitorChannelOption, error) {
	var channels []BotMonitorChannelOption
	err := DB.Model(&Channel{}).Select("id", "name", "type", "status").Order("id asc").Scan(&channels).Error
	return channels, err
}

// MigrateLegacyFeishuMonitorRobot preserves the old global Feishu configuration.
func MigrateLegacyFeishuMonitorRobot() error {
	var options []Option
	if err := DB.Where("key IN ?", []string{"FeishuAppId", "FeishuAppSecret", "FeishuChatIds", "FeishuChannelErrorAlertEnabled", legacyFeishuBindingMigrationKey}).Find(&options).Error; err != nil {
		return err
	}
	values := make(map[string]string, len(options))
	for _, option := range options {
		values[option.Key] = option.Value
	}
	var robot BotMonitorRobot
	robotErr := DB.Where("type = ? AND name = ?", BotMonitorTypeFeishu, "Migrated Feishu bot").First(&robot).Error
	if robotErr != nil && !errors.Is(robotErr, gorm.ErrRecordNotFound) {
		return robotErr
	}
	if errors.Is(robotErr, gorm.ErrRecordNotFound) {
		var count int64
		if err := DB.Model(&BotMonitorRobot{}).Where("type = ?", BotMonitorTypeFeishu).Count(&count).Error; err != nil || count > 0 {
			return err
		}
	}
	appID, appSecret, chats := strings.TrimSpace(values["FeishuAppId"]), strings.TrimSpace(values["FeishuAppSecret"]), strings.TrimSpace(values["FeishuChatIds"])
	if errors.Is(robotErr, gorm.ErrRecordNotFound) && (appID == "" || appSecret == "" || chats == "") {
		return nil
	}
	if errors.Is(robotErr, gorm.ErrRecordNotFound) {
		enabled := strings.EqualFold(values["FeishuChannelErrorAlertEnabled"], "true") || values["FeishuChannelErrorAlertEnabled"] == "1"
		encryptedID, err := common.EncryptCredential(appID)
		if err != nil {
			return err
		}
		encryptedSecret, err := common.EncryptCredential(appSecret)
		if err != nil {
			return err
		}
		robot = BotMonitorRobot{Name: "Migrated Feishu bot", Type: BotMonitorTypeFeishu, Enabled: enabled, FeishuAppId: encryptedID, FeishuAppSecret: encryptedSecret, FeishuChatIds: chats}
		if err := DB.Create(&robot).Error; err != nil {
			return err
		}
	}
	if strings.EqualFold(values[legacyFeishuBindingMigrationKey], "true") {
		return nil
	}
	var bindingCount int64
	if err := DB.Model(&BotMonitorBinding{}).Where("robot_id = ?", robot.Id).Count(&bindingCount).Error; err != nil {
		return err
	}
	if bindingCount == 0 {
		var channelIDs []int
		if err := DB.Model(&Channel{}).Order("id asc").Pluck("id", &channelIDs).Error; err != nil {
			return err
		}
		if len(channelIDs) > 0 {
			encoded, err := EncodeBotMonitorChannelIds(channelIDs)
			if err != nil {
				return err
			}
			enabled := strings.EqualFold(values["FeishuChannelErrorAlertEnabled"], "true") || values["FeishuChannelErrorAlertEnabled"] == "1"
			if err := DB.Create(&BotMonitorBinding{Name: "Migrated Feishu all channels", RobotId: robot.Id, ChannelIds: encoded, MinIntervalSeconds: 300, TriggerStatusCodes: defaultBotMonitorTriggerStatusCodes, Enabled: enabled}).Error; err != nil {
				return err
			}
		}
	}
	marker := Option{Key: legacyFeishuBindingMigrationKey, Value: "true"}
	if err := DB.FirstOrCreate(&marker, Option{Key: legacyFeishuBindingMigrationKey}).Error; err != nil {
		return err
	}
	return DB.Model(&marker).Updates(map[string]any{"value": "true"}).Error
}
