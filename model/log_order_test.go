package model

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useLogOrderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:log-order?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))
	require.NoError(t, db.Exec("DELETE FROM logs").Error)

	previousDB, previousLogDB := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
	})
	return db
}

func recordConsumeLogStartedAt(t *testing.T, start time.Time, model string) {
	t.Helper()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(ctx, constant.ContextKeyRequestStartTime, start)
	RecordConsumeLog(ctx, 42, RecordConsumeLogParams{ModelName: model, TokenId: 7, Quota: 1, Other: NewLogOther()})
}

func logModels(logs []*Log) []string {
	models := make([]string, len(logs))
	for i, log := range logs {
		models[i] = log.ModelName
	}
	return models
}

// A burst written in completion order must still list in send order: the slow
// request finishes last (largest id) but started first.
func TestLogListsOrderByRequestStartTime(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := useLogOrderTestDB(t)
	start := time.Now().Add(-time.Minute)

	require.NoError(t, createLog(&Log{UserId: 42, TokenId: 7, CreatedAt: start.Add(-time.Hour).Unix(), Type: LogTypeConsume, ModelName: "legacy"}))
	require.NoError(t, db.Model(&Log{}).Where("model_name = ?", "legacy").Update("start_time_ms", 0).Error)
	recordConsumeLogStartedAt(t, start.Add(2*time.Second), "fast")
	recordConsumeLogStartedAt(t, start, "slow")

	var stored Log
	require.NoError(t, db.Where("model_name = ?", "slow").First(&stored).Error)
	require.Equal(t, start.UnixMilli(), stored.StartTimeMs)

	// Logs without a request context take their write time.
	RecordLog(42, LogTypeSystem, common.NewMessage("system notice"))
	var system Log
	require.NoError(t, db.Where("type = ?", LogTypeSystem).First(&system).Error)
	require.Equal(t, system.CreatedAt*1000, system.StartTimeMs)

	logs, total, err := GetAllLogs(LogFilter{Type: LogTypeConsume}, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.Equal(t, []string{"fast", "slow", "legacy"}, logModels(logs))

	logs, _, err = GetUserLogs(42, LogFilter{}, 0, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"", "fast", "slow", "legacy"}, logModels(logs))

	logs, err = GetLogByTokenId(7)
	require.NoError(t, err)
	require.Equal(t, []string{"fast", "slow", "legacy"}, logModels(logs))

	var streamed []string
	require.NoError(t, StreamAllLogs(t.Context(), LogFilter{Type: LogTypeConsume}, common.RoleRootUser, func(log *Log) error {
		streamed = append(streamed, log.ModelName)
		return nil
	}))
	require.Equal(t, []string{"fast", "slow", "legacy"}, streamed)
}
