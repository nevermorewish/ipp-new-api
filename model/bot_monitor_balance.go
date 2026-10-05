package model

import (
	"errors"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	// DefaultBotMonitorBalanceThreshold is kept for the legacy raw-quota
	// column. New balance alerts use the currency amount below.
	DefaultBotMonitorBalanceThreshold = 5000
	// DefaultBotMonitorBalanceThresholdAmount is expressed in the configured
	// currency (CNY on the current instance), not internal quota units.
	DefaultBotMonitorBalanceThresholdAmount = 5000.0
	DefaultBotMonitorCheckIntervalHours     = 1.0
	MinimumBotMonitorCheckIntervalHours     = 0.01
	MaximumBotMonitorCheckIntervalHours     = 8760.0
)

type BotMonitorBalanceBinding struct {
	Id                 int     `json:"id"`
	RobotId            int     `json:"robot_id" gorm:"column:robot_id;not null;index"`
	UserIds            string  `json:"-" gorm:"column:user_ids;type:text;not null"`
	Threshold          int     `json:"-" gorm:"type:int;not null"` // legacy raw-quota value
	ThresholdAmount    float64 `json:"threshold_amount" gorm:"column:threshold_amount;not null;default:5000"`
	CheckIntervalHours float64 `json:"check_interval_hours" gorm:"column:check_interval_hours;not null;default:1"`
	LastCheckedAt      int64   `json:"last_checked_at" gorm:"column:last_checked_at;not null;default:0;bigint"`
	Enabled            bool    `json:"enabled" gorm:"not null;index"`
	CreatedAt          int64   `json:"created_at" gorm:"bigint"`
	UpdatedAt          int64   `json:"updated_at" gorm:"bigint"`
}

type BotMonitorUserOption struct {
	Id          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Quota       int    `json:"quota"`
	Status      int    `json:"status"`
}

func (b *BotMonitorBalanceBinding) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	b.CreatedAt, b.UpdatedAt = now, now
	return nil
}

func NormalizeBotMonitorUserIds(ids []int) []int {
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

func EncodeBotMonitorUserIds(ids []int) (string, error) {
	encoded, err := common.Marshal(NormalizeBotMonitorUserIds(ids))
	return string(encoded), err
}

func DecodeBotMonitorUserIds(raw string) ([]int, error) {
	var ids []int
	if err := common.UnmarshalJsonStr(raw, &ids); err != nil {
		return nil, err
	}
	return NormalizeBotMonitorUserIds(ids), nil
}

func ListBotMonitorBalanceBindings() ([]*BotMonitorBalanceBinding, error) {
	var bindings []*BotMonitorBalanceBinding
	err := DB.Order("id desc").Find(&bindings).Error
	return bindings, err
}

func ListEnabledBotMonitorBalanceBindings() ([]*BotMonitorBalanceBinding, error) {
	var bindings []*BotMonitorBalanceBinding
	err := DB.Where("enabled = ?", true).Order("id asc").Find(&bindings).Error
	return bindings, err
}

func GetBotMonitorBalanceBinding(id int) (*BotMonitorBalanceBinding, error) {
	var binding BotMonitorBalanceBinding
	if err := DB.First(&binding, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &binding, nil
}

func CreateBotMonitorBalanceBinding(binding *BotMonitorBalanceBinding) error {
	return DB.Create(binding).Error
}

func UpdateBotMonitorBalanceBinding(binding *BotMonitorBalanceBinding) error {
	return DB.Model(&BotMonitorBalanceBinding{}).Where("id = ?", binding.Id).Updates(map[string]any{
		"robot_id":             binding.RobotId,
		"user_ids":             binding.UserIds,
		"threshold":            binding.Threshold,
		"threshold_amount":     binding.ThresholdAmount,
		"check_interval_hours": binding.CheckIntervalHours,
		"last_checked_at":      0,
		"enabled":              binding.Enabled,
		"updated_at":           common.GetTimestamp(),
	}).Error
}

func UpdateBotMonitorBalanceBindingLastCheckedAt(id int, checkedAt int64) error {
	return DB.Model(&BotMonitorBalanceBinding{}).Where("id = ?", id).Updates(map[string]any{
		"last_checked_at": checkedAt,
		"updated_at":      common.GetTimestamp(),
	}).Error
}

func DeleteBotMonitorBalanceBinding(id int) error {
	return DB.Delete(&BotMonitorBalanceBinding{}, id).Error
}

func ValidateBotMonitorUserIds(ids []int) error {
	ids = NormalizeBotMonitorUserIds(ids)
	if len(ids) == 0 {
		return errors.New("select at least one user")
	}
	var count int64
	if err := DB.Model(&User{}).Where("id IN ?", ids).Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(ids)) {
		return errors.New("one or more selected users do not exist")
	}
	return nil
}

func ListBotMonitorUserOptions() ([]BotMonitorUserOption, error) {
	var users []BotMonitorUserOption
	err := DB.Model(&User{}).
		Select("id", "username", "display_name", "quota", "status").
		Order("id asc").
		Scan(&users).Error
	return users, err
}

func ListBotMonitorUsersByIds(ids []int) ([]BotMonitorUserOption, error) {
	ids = NormalizeBotMonitorUserIds(ids)
	if len(ids) == 0 {
		return []BotMonitorUserOption{}, nil
	}
	var users []BotMonitorUserOption
	err := DB.Model(&User{}).
		Select("id", "username", "display_name", "quota", "status").
		Where("id IN ?", ids).
		Order("id asc").
		Scan(&users).Error
	return users, err
}
