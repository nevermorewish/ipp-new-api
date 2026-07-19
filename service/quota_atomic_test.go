package service

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAtomicWalletTokenTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	previousDB := model.DB
	previousRedisEnabled := common.RedisEnabled
	previousBatchUpdateEnabled := common.BatchUpdateEnabled
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()

	dbPath := filepath.ToSlash(filepath.Join(t.TempDir(), "quota-atomic.db"))
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?_busy_timeout=30000&_journal_mode=WAL", dbPath)), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}))

	model.DB = db
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)

	t.Cleanup(func() {
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
		model.DB = previousDB
		common.RedisEnabled = previousRedisEnabled
		common.BatchUpdateEnabled = previousBatchUpdateEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
	})

	return db
}

func seedAtomicWalletAndToken(t *testing.T, db *gorm.DB, walletQuota int, tokenQuota int) (*model.User, *model.Token) {
	t.Helper()

	user := &model.User{
		Username: "atomic-wallet-owner",
		Password: "hashed-password",
		AffCode:  "atomic-wallet-owner-aff",
		Quota:    walletQuota,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(user).Error)

	token := &model.Token{
		UserId:         user.Id,
		Key:            "atomic-wallet-token-key",
		Name:           "atomic wallet token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		RemainQuota:    tokenQuota,
		UnlimitedQuota: false,
	}
	require.NoError(t, db.Create(token).Error)
	return user, token
}

func walletRelayInfo(user *model.User, token *model.Token) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:        user.Id,
		BillingUserId: user.Id,
		BillingSource: BillingSourceWallet,
		TokenId:       token.Id,
		TokenKey:      token.Key,
	}
}

func TestPostConsumeQuotaRollsBackWalletWhenTokenIsInsufficient(t *testing.T) {
	db := setupAtomicWalletTokenTestDB(t)
	user, token := seedAtomicWalletAndToken(t, db, 100, 5)

	err := PostConsumeQuota(walletRelayInfo(user, token), 10, 0, false)
	require.ErrorIs(t, err, model.ErrInsufficientTokenQuota)

	var storedUser model.User
	var storedToken model.Token
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	assert.Equal(t, 100, storedUser.Quota)
	assert.Equal(t, 5, storedToken.RemainQuota)
	assert.Zero(t, storedToken.UsedQuota)
}

func TestPostConsumeQuotaLeavesTokenUntouchedWhenWalletIsInsufficient(t *testing.T) {
	db := setupAtomicWalletTokenTestDB(t)
	user, token := seedAtomicWalletAndToken(t, db, 5, 100)

	err := PostConsumeQuota(walletRelayInfo(user, token), 10, 0, false)
	require.ErrorIs(t, err, model.ErrInsufficientUserQuota)

	var storedUser model.User
	var storedToken model.Token
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	assert.Equal(t, 5, storedUser.Quota)
	assert.Equal(t, 100, storedToken.RemainQuota)
	assert.Zero(t, storedToken.UsedQuota)
}

func TestQuotaRefundRestoresSoftDeletedTokenLedgerWithoutReactivatingToken(t *testing.T) {
	db := setupAtomicWalletTokenTestDB(t)
	user, token := seedAtomicWalletAndToken(t, db, 100, 100)

	require.NoError(t, model.DecreaseUserAndTokenQuota(user.Id, token.Id, token.Key, 10))
	require.NoError(t, db.Delete(token).Error)
	require.NoError(t, model.IncreaseUserAndTokenQuota(user.Id, token.Id, token.Key, 10))

	var storedUser model.User
	var storedToken model.Token
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	require.NoError(t, db.Unscoped().First(&storedToken, token.Id).Error)
	assert.Equal(t, 100, storedUser.Quota)
	assert.Equal(t, 100, storedToken.RemainQuota)
	assert.Zero(t, storedToken.UsedQuota)
	assert.True(t, storedToken.DeletedAt.Valid)

	err := model.DecreaseUserAndTokenQuota(user.Id, token.Id, token.Key, 10)
	require.ErrorIs(t, err, model.ErrInsufficientTokenQuota)
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	assert.Equal(t, 100, storedUser.Quota)
}

func TestPostConsumeQuotaConcurrentWalletAndTokenStayReconciled(t *testing.T) {
	db := setupAtomicWalletTokenTestDB(t)
	user, token := seedAtomicWalletAndToken(t, db, 200, 100)

	const (
		workers = 20
		charge  = 10
	)
	start := make(chan struct{})
	results := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- PostConsumeQuota(walletRelayInfo(user, token), charge, 0, false)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	insufficientToken := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, model.ErrInsufficientTokenQuota):
			insufficientToken++
		default:
			require.NoError(t, err)
		}
	}

	var storedUser model.User
	var storedToken model.Token
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	assert.Equal(t, 10, successes)
	assert.Equal(t, 10, insufficientToken)
	assert.Equal(t, 100, storedUser.Quota)
	assert.Zero(t, storedToken.RemainQuota)
	assert.Equal(t, 100, storedToken.UsedQuota)
	assert.Equal(t, 200-storedUser.Quota, storedToken.UsedQuota)
}

func TestPostConsumeQuotaConcurrentWalletBottleneckStaysReconciled(t *testing.T) {
	db := setupAtomicWalletTokenTestDB(t)
	user, token := seedAtomicWalletAndToken(t, db, 100, 200)

	const (
		workers = 20
		charge  = 10
	)
	start := make(chan struct{})
	results := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- PostConsumeQuota(walletRelayInfo(user, token), charge, 0, false)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	insufficientWallet := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, model.ErrInsufficientUserQuota):
			insufficientWallet++
		default:
			require.NoError(t, err)
		}
	}

	var storedUser model.User
	var storedToken model.Token
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	assert.Equal(t, 10, successes)
	assert.Equal(t, 10, insufficientWallet)
	assert.Zero(t, storedUser.Quota)
	assert.Equal(t, 100, storedToken.RemainQuota)
	assert.Equal(t, 100, storedToken.UsedQuota)
	assert.Equal(t, 100-storedUser.Quota, storedToken.UsedQuota)
}

func TestBillingSessionWalletPreConsumeRollsBackWhenTokenIsInsufficient(t *testing.T) {
	db := setupAtomicWalletTokenTestDB(t)
	user, token := seedAtomicWalletAndToken(t, db, 100, 5)
	relayInfo := walletRelayInfo(user, token)
	session := &BillingSession{
		relayInfo: relayInfo,
		funding:   &WalletFunding{userId: user.Id},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	apiErr := session.preConsume(ctx, 10)
	require.NotNil(t, apiErr)

	var storedUser model.User
	var storedToken model.Token
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	assert.Equal(t, 100, storedUser.Quota)
	assert.Equal(t, 5, storedToken.RemainQuota)
	assert.Zero(t, storedToken.UsedQuota)
	assert.Zero(t, session.tokenConsumed)
	assert.Zero(t, session.preConsumedQuota)
}

func TestBillingSessionWalletSettleRollsBackWhenTokenDeltaIsInsufficient(t *testing.T) {
	db := setupAtomicWalletTokenTestDB(t)
	user, token := seedAtomicWalletAndToken(t, db, 100, 20)
	relayInfo := walletRelayInfo(user, token)
	session := &BillingSession{
		relayInfo: relayInfo,
		funding:   &WalletFunding{userId: user.Id},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Nil(t, session.preConsume(ctx, 10))

	err := session.Settle(30)
	require.ErrorIs(t, err, model.ErrInsufficientTokenQuota)

	var storedUser model.User
	var storedToken model.Token
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	assert.Equal(t, 90, storedUser.Quota)
	assert.Equal(t, 10, storedToken.RemainQuota)
	assert.Equal(t, 10, storedToken.UsedQuota)
	assert.False(t, session.settled)
	assert.False(t, session.fundingSettled)
}
