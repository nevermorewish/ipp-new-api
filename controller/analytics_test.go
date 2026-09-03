package controller

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

func setupUserAnalyticsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "-"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}))

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	model.DB = db
	model.LOG_DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
	})
	return db
}

func TestWriteUserAnalyticsWorkbookGroupsByUserAndDate(t *testing.T) {
	db := setupUserAnalyticsTestDB(t)
	users := []model.User{
		{Id: 101, Username: "alpha", DisplayName: "Alpha Team", Remark: "first user", AffCode: "alpha-code"},
		{Id: 202, Username: "beta", DisplayName: "Beta Team", Remark: "second user", AffCode: "beta-code"},
	}
	require.NoError(t, db.Create(&users).Error)

	dayOne := time.Date(2026, time.July, 23, 0, 0, 0, 0, time.Local)
	dayTwo := dayOne.AddDate(0, 0, 1)
	logs := []model.Log{
		{UserId: 101, CreatedAt: dayOne.Add(9 * time.Hour).Unix(), Type: model.LogTypeConsume, ModelName: "gpt-5", PromptTokens: 10, CompletionTokens: 5, Quota: 100},
		{UserId: 101, CreatedAt: dayOne.Add(12 * time.Hour).Unix(), Type: model.LogTypeConsume, ModelName: "claude", PromptTokens: 20, CompletionTokens: 5, Quota: 200},
		{UserId: 101, CreatedAt: dayOne.Add(13 * time.Hour).Unix(), Type: model.LogTypeRefund, ModelName: "claude", Quota: 50},
		{UserId: 101, CreatedAt: dayTwo.Add(10 * time.Hour).Unix(), Type: model.LogTypeConsume, ModelName: "gpt-5", PromptTokens: 30, CompletionTokens: 10, Quota: 300},
		{UserId: 202, CreatedAt: dayOne.Add(10 * time.Hour).Unix(), Type: model.LogTypeConsume, ModelName: "gpt-5", PromptTokens: 40, CompletionTokens: 20, Quota: 400},
		{UserId: 101, CreatedAt: dayOne.Add(15 * time.Hour).Unix(), Type: model.LogTypeError, ModelName: "ignored", PromptTokens: 999, CompletionTokens: 999, Quota: 999},
	}
	require.NoError(t, db.Create(&logs).Error)

	file := excelize.NewFile()
	require.NoError(t, writeUserAnalyticsWorkbook(file, dayOne.Unix(), dayTwo.AddDate(0, 0, 1).Add(-time.Second).Unix(), "en"))
	require.Equal(t, []string{"Alpha Team", "Beta Team"}, file.GetSheetList())

	alphaRows, err := file.GetRows("Alpha Team")
	require.NoError(t, err)
	require.Equal(t, []string{"Date", "Display Name", "Remark", "Requests", "Tokens", "Consumption ($)", "Model Usage"}, alphaRows[0])
	require.NotContains(t, alphaRows[0], "Role")
	require.Len(t, alphaRows, 3)
	require.Equal(t, "2026-07-23", alphaRows[1][0])
	require.Equal(t, "Alpha Team", alphaRows[1][1])
	require.Equal(t, "2", alphaRows[1][3])
	require.Equal(t, "40", alphaRows[1][4])
	require.Equal(t, "0.0005", alphaRows[1][5])
	require.Contains(t, alphaRows[1][6], "claude")
	require.Contains(t, alphaRows[1][6], "gpt-5")
	require.Equal(t, "2026-07-24", alphaRows[2][0])
	require.Equal(t, "1", alphaRows[2][3])
	require.Equal(t, "40", alphaRows[2][4])

	betaRows, err := file.GetRows("Beta Team")
	require.NoError(t, err)
	require.Len(t, betaRows, 2)
	require.Equal(t, "2026-07-23", betaRows[1][0])
	require.Equal(t, "1", betaRows[1][3])
	require.Equal(t, "60", betaRows[1][4])
}

func TestUserAnalyticsSubtractsRefundsFromNetConsumption(t *testing.T) {
	db := setupUserAnalyticsTestDB(t)
	start := time.Date(2026, time.August, 27, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 0, 1).Add(-time.Second)
	logs := []model.Log{
		{UserId: 101, CreatedAt: start.Add(time.Hour).Unix(), Type: model.LogTypeConsume, ModelName: "doubao-seedance-2.5", PromptTokens: 10, CompletionTokens: 5, Quota: 500},
		{UserId: 101, CreatedAt: start.Add(2 * time.Hour).Unix(), Type: model.LogTypeRefund, ModelName: "doubao-seedance-2.5", Quota: 300},
		{UserId: 101, CreatedAt: start.Add(3 * time.Hour).Unix(), Type: model.LogTypeConsume, ModelName: "gpt-5", PromptTokens: 20, CompletionTokens: 10, Quota: 200},
		{UserId: 202, CreatedAt: start.Add(4 * time.Hour).Unix(), Type: model.LogTypeRefund, ModelName: "older-task", Quota: 50},
		{UserId: 101, CreatedAt: start.Add(5 * time.Hour).Unix(), Type: model.LogTypeError, ModelName: "ignored", PromptTokens: 999, CompletionTokens: 999, Quota: 999},
	}
	require.NoError(t, db.Create(&logs).Error)

	totals, err := getUserAnalyticsTotals(start.Unix(), end.Unix())
	require.NoError(t, err)
	require.Equal(t, int64(350), totals.QuotaSum)

	rankings, err := getUserAnalyticsRankingRows(start.Unix(), end.Unix())
	require.NoError(t, err)
	require.Len(t, rankings, 2)
	require.Equal(t, 101, rankings[0].UserId)
	require.Equal(t, int64(2), rankings[0].RequestCount)
	require.Equal(t, int64(45), rankings[0].TokenCount)
	require.Equal(t, int64(400), rankings[0].QuotaSum)
	require.Equal(t, 202, rankings[1].UserId)
	require.Equal(t, int64(0), rankings[1].RequestCount)
	require.Equal(t, int64(-50), rankings[1].QuotaSum)

	modelRows, err := getUserModelUsageRows(start.Unix(), 0, end.Unix(), []int{101})
	require.NoError(t, err)
	require.Len(t, modelRows, 2)
	rowsByModel := make(map[string]userModelUsageRow, len(modelRows))
	for _, row := range modelRows {
		rowsByModel[row.ModelName] = row
	}
	seedance := rowsByModel["doubao-seedance-2.5"]
	require.Equal(t, int64(1), seedance.RequestCount)
	require.Equal(t, int64(15), seedance.TokenCount)
	require.Equal(t, int64(200), seedance.QuotaSum)

	dailyRows, err := getUserAnalyticsDailyUsageRows(start.Unix(), end.Unix(), analyticsExportDayOffset(start.Unix()))
	require.NoError(t, err)
	require.Len(t, dailyRows, 3)
}

func TestUniqueUserAnalyticsSheetName(t *testing.T) {
	used := make(map[string]struct{})
	user := userAnalyticsUserInfo{DisplayName: "Same Name"}
	require.Equal(t, "Same Name", uniqueUserAnalyticsSheetName(1, user, used))
	require.Equal(t, "Same Name (2)", uniqueUserAnalyticsSheetName(2, user, used))
}
