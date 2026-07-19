package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDecreaseTokenQuotaRejectsOverdraft(t *testing.T) {
	previousDB := DB
	previousRedisEnabled := common.RedisEnabled
	previousBatchUpdateEnabled := common.BatchUpdateEnabled
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	common.RedisEnabled = false
	common.BatchUpdateEnabled = true
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(&Token{}))
	t.Cleanup(func() {
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
		DB = previousDB
		common.RedisEnabled = previousRedisEnabled
		common.BatchUpdateEnabled = previousBatchUpdateEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
	})

	token := Token{
		UserId:         1,
		Key:            "quota-guard-key",
		Status:         common.TokenStatusEnabled,
		Name:           "quota guard",
		RemainQuota:    100,
		UnlimitedQuota: false,
	}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, DecreaseTokenQuota(token.Id, token.Key, 80))
	require.Error(t, DecreaseTokenQuota(token.Id, token.Key, 30))
	require.True(t, errors.Is(DecreaseTokenQuota(token.Id, token.Key, 21), ErrInsufficientTokenQuota))

	var stored Token
	require.NoError(t, db.First(&stored, token.Id).Error)
	require.Equal(t, 20, stored.RemainQuota)
	require.Equal(t, 80, stored.UsedQuota)
}

func TestDecreaseUserQuotaRejectsOverdraft(t *testing.T) {
	previousDB := DB
	previousRedisEnabled := common.RedisEnabled
	previousBatchUpdateEnabled := common.BatchUpdateEnabled
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	common.RedisEnabled = false
	common.BatchUpdateEnabled = true
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(&User{}))
	t.Cleanup(func() {
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
		DB = previousDB
		common.RedisEnabled = previousRedisEnabled
		common.BatchUpdateEnabled = previousBatchUpdateEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
	})

	user := User{Username: "quota-owner", Password: "hashed-password", AffCode: "quota-owner-aff", Quota: 100}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, DecreaseUserQuota(user.Id, 80, false))
	require.True(t, errors.Is(DecreaseUserQuota(user.Id, 30, false), ErrInsufficientUserQuota))

	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 20, stored.Quota)
}
