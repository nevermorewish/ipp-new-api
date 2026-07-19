package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

func DecreaseUserAndTokenQuota(userId int, tokenId int, tokenKey string, quota int) error {
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if quota == 0 {
		return nil
	}
	if userId <= 0 || tokenId <= 0 {
		return errors.New("user id and token id are required")
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		userResult := tx.Model(&User{}).
			Where("id = ? AND quota >= ?", userId, quota).
			Update("quota", gorm.Expr("quota - ?", quota))
		if userResult.Error != nil {
			return userResult.Error
		}
		if userResult.RowsAffected == 0 {
			return ErrInsufficientUserQuota
		}

		tokenResult := tx.Model(&Token{}).
			Where("id = ? AND (unlimited_quota = ? OR remain_quota >= ?)", tokenId, true, quota).
			Updates(map[string]interface{}{
				"remain_quota":  gorm.Expr("remain_quota - ?", quota),
				"used_quota":    gorm.Expr("used_quota + ?", quota),
				"accessed_time": common.GetTimestamp(),
			})
		if tokenResult.Error != nil {
			return tokenResult.Error
		}
		if tokenResult.RowsAffected == 0 {
			return ErrInsufficientTokenQuota
		}
		return nil
	})
	if err != nil {
		return err
	}
	invalidateQuotaCaches(userId, tokenKey)
	return nil
}

func IncreaseUserAndTokenQuota(userId int, tokenId int, tokenKey string, quota int) error {
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if quota == 0 {
		return nil
	}
	if userId <= 0 || tokenId <= 0 {
		return errors.New("user id and token id are required")
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		userResult := tx.Model(&User{}).
			Where("id = ?", userId).
			Update("quota", gorm.Expr("quota + ?", quota))
		if userResult.Error != nil {
			return userResult.Error
		}
		if userResult.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		// A token may be revoked after charging. Reconcile its ledger on refund
		// without clearing deleted_at; debit transactions remain scoped.
		tokenResult := tx.Unscoped().Model(&Token{}).
			Where("id = ?", tokenId).
			Updates(map[string]interface{}{
				"remain_quota":  gorm.Expr("remain_quota + ?", quota),
				"used_quota":    gorm.Expr("used_quota - ?", quota),
				"accessed_time": common.GetTimestamp(),
			})
		if tokenResult.Error != nil {
			return tokenResult.Error
		}
		if tokenResult.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		return err
	}
	invalidateQuotaCaches(userId, tokenKey)
	return nil
}

func invalidateQuotaCaches(userId int, tokenKey string) {
	if !common.RedisEnabled {
		return
	}
	if err := invalidateUserCache(userId); err != nil {
		common.SysLog("failed to invalidate user quota cache after atomic adjustment: " + err.Error())
	}
	if tokenKey == "" {
		return
	}
	if err := cacheDeleteToken(tokenKey); err != nil {
		common.SysLog("failed to invalidate token quota cache after atomic adjustment: " + err.Error())
	}
}
