package model

import (
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	EnterpriseStatusEnabled  = 1
	EnterpriseStatusDisabled = 2
)

type Enterprise struct {
	Id          int            `json:"id" gorm:"primaryKey"`
	Name        string         `json:"name" gorm:"type:varchar(100);not null;index"`
	OwnerUserId int            `json:"owner_user_id" gorm:"column:owner_user_id;uniqueIndex;not null"`
	Status      int            `json:"status" gorm:"type:int;default:1"`
	CreatedAt   int64          `json:"created_at" gorm:"autoCreateTime;column:created_at"`
	UpdatedAt   int64          `json:"updated_at" gorm:"autoUpdateTime;column:updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index"`
}

func (Enterprise) TableName() string {
	return "enterprises"
}

func EnsureEnterpriseForUser(user *User) (*Enterprise, error) {
	if user == nil || user.Id <= 0 {
		return nil, nil
	}
	name := strings.TrimSpace(user.EnterpriseName)
	if name == "" {
		name = strings.TrimSpace(user.Username)
	}
	if name == "" {
		name = "Enterprise"
	}

	var enterprise Enterprise
	err := DB.Where("owner_user_id = ?", user.Id).First(&enterprise).Error
	if err == nil {
		updates := map[string]interface{}{
			"name":   name,
			"status": EnterpriseStatusEnabled,
		}
		if err := DB.Model(&enterprise).Updates(updates).Error; err != nil {
			return nil, err
		}
		enterprise.Name = name
		enterprise.Status = EnterpriseStatusEnabled
		if user.EnterpriseId != enterprise.Id || user.EnterpriseName != name {
			if err := DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
				"enterprise_id":   enterprise.Id,
				"enterprise_name": name,
			}).Error; err != nil {
				return nil, err
			}
			user.EnterpriseId = enterprise.Id
			user.EnterpriseName = name
		}
		return &enterprise, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	enterprise = Enterprise{
		Name:        name,
		OwnerUserId: user.Id,
		Status:      EnterpriseStatusEnabled,
	}
	if err := DB.Create(&enterprise).Error; err != nil {
		return nil, err
	}
	if err := DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"enterprise_id":   enterprise.Id,
		"enterprise_name": name,
	}).Error; err != nil {
		return nil, err
	}
	user.EnterpriseId = enterprise.Id
	user.EnterpriseName = name
	return &enterprise, nil
}

func MigrateEnterpriseData() error {
	var admins []User
	if err := DB.Where("type = ?", 1).Find(&admins).Error; err != nil {
		return err
	}
	for i := range admins {
		admin := admins[i]
		enterprise, err := EnsureEnterpriseForUser(&admin)
		if err != nil {
			return err
		}
		if enterprise == nil {
			continue
		}
		updates := map[string]interface{}{
			"enterprise_id":   enterprise.Id,
			"enterprise_name": enterprise.Name,
		}
		if err := DB.Model(&User{}).Where("type = ? AND topid = ?", 2, admin.Id).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}

func IsEnterpriseAdminEnabled(user *User) (bool, error) {
	if user == nil || user.Id <= 0 || user.Type != 1 || user.Status != common.UserStatusEnabled || user.EnterpriseId <= 0 {
		return false, nil
	}
	var count int64
	err := DB.Model(&Enterprise{}).
		Where("id = ? AND owner_user_id = ? AND status = ?", user.EnterpriseId, user.Id, EnterpriseStatusEnabled).
		Count(&count).Error
	return count > 0, err
}

func DisableEnterpriseForOwner(ownerUserId int) error {
	if ownerUserId <= 0 {
		return nil
	}
	var childIds []int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var enterprise Enterprise
		enterpriseErr := tx.Where("owner_user_id = ?", ownerUserId).First(&enterprise).Error
		if enterpriseErr != nil && enterpriseErr != gorm.ErrRecordNotFound {
			return enterpriseErr
		}

		children := tx.Model(&User{}).Where("type = ? AND topid = ?", 2, ownerUserId)
		if enterprise.Id > 0 {
			children = tx.Model(&User{}).
				Where("type = ? AND (topid = ? OR enterprise_id = ?)", 2, ownerUserId, enterprise.Id)
		}
		if err := children.Pluck("id", &childIds).Error; err != nil {
			return err
		}
		if len(childIds) > 0 {
			if err := tx.Model(&User{}).Where("id IN ?", childIds).
				Update("status", common.UserStatusDisabled).Error; err != nil {
				return err
			}
		}
		if enterprise.Id == 0 {
			return nil
		}
		return tx.Model(&Enterprise{}).
			Where("id = ?", enterprise.Id).
			Updates(map[string]interface{}{
				"status":     EnterpriseStatusDisabled,
				"updated_at": time.Now().Unix(),
			}).Error
	})
	if err != nil {
		return err
	}
	for _, childId := range childIds {
		if err := InvalidateUserCache(childId); err != nil {
			common.SysLog("failed to invalidate disabled sub-account cache: " + err.Error())
		}
		if err := InvalidateUserTokensCache(childId); err != nil {
			common.SysLog("failed to invalidate disabled sub-account token cache: " + err.Error())
		}
	}
	return nil
}
