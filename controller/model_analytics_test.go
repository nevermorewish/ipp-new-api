package controller

import (
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelAnalyticsDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct{ name, env string }{
		{"sqlite", ""}, {"mysql", "MODEL_ANALYTICS_MYSQL_DSN"}, {"postgres", "MODEL_ANALYTICS_POSTGRES_DSN"},
	} {
		t.Run(dialect.name, func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " is not configured")
			}
			for _, separateLog := range []bool{false, true} {
				name := "shared-log"
				if separateLog {
					name = "separate-log"
				}
				t.Run(name, func(t *testing.T) {
					oldDB, oldLogDB := model.DB, model.LOG_DB
					oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
					oldMaster, oldPath := common.IsMasterNode, common.SQLitePath
					t.Cleanup(func() {
						model.DB, model.LOG_DB = oldDB, oldLogDB
						common.SetDatabaseTypes(oldMain, oldLog)
						common.IsMasterNode, common.SQLitePath = oldMaster, oldPath
					})
					db, isolatedDSN := newAuditTestDatabase(t, dialect.name, dsn)
					sqlDB, err := db.DB()
					require.NoError(t, err)
					t.Cleanup(func() { _ = sqlDB.Close() })
					t.Setenv("SQL_DSN", isolatedDSN)
					common.IsMasterNode = false
					if dialect.name == "sqlite" {
						common.SQLitePath = isolatedDSN
						t.Setenv("SQL_DSN", "")
					}
					require.NoError(t, model.InitDB())
					mainSQLDB, err := model.DB.DB()
					require.NoError(t, err)
					t.Cleanup(func() { _ = mainSQLDB.Close() })
					model.LOG_DB = db
					if separateLog {
						logDB, _ := newAuditTestDatabase(t, dialect.name, dsn)
						logSQLDB, err := logDB.DB()
						require.NoError(t, err)
						t.Cleanup(func() { _ = logSQLDB.Close() })
						model.LOG_DB = logDB
					}
					require.NoError(t, model.DB.AutoMigrate(&model.PerfMetric{}))
					require.NoError(t, model.LOG_DB.AutoMigrate(&model.Log{}))
					versionSQL := "SELECT version()"
					if dialect.name == "sqlite" {
						versionSQL = "SELECT sqlite_version()"
					}
					var version string
					require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
					t.Logf("database version: %s", version)

					// Unconfigured historical groups are still part of administrator analytics.
					now := time.Now().Unix()
					bucket := now - now%3600
					rows := []model.PerfMetric{
						{ModelName: "analytics-alpha", Group: "default", BucketTs: bucket, RequestCount: 4, SuccessCount: 3, TotalLatencyMs: 8000, TtftSumMs: 3000, TtftCount: 2},
						{ModelName: "analytics-alpha", Group: "historical", BucketTs: bucket, RequestCount: 2, SuccessCount: 2, TotalLatencyMs: 2000, TtftSumMs: 9000, TtftCount: 1},
						{ModelName: "analytics-beta", Group: "default", BucketTs: bucket, RequestCount: 1, SuccessCount: 0},
						{ModelName: "outside-range", Group: "default", BucketTs: bucket - 86400, RequestCount: 1},
					}
					require.NoError(t, model.DB.Create(&rows).Error)
					result, err := perfmetrics.QueryRangeAll(bucket, now, nil)
					require.NoError(t, err)
					assert.Equal(t, []perfmetrics.RangeBucketPoint{
						{ModelName: "analytics-alpha", BucketTs: bucket, RequestCount: 6, ErrorCount: 1, AvgLatencyMs: 1666, AvgTtftMs: 4000, TtftCount: 3, HasTtft: true},
						{ModelName: "analytics-beta", BucketTs: bucket, RequestCount: 1, ErrorCount: 1},
					}, result.Items)
					filtered, err := perfmetrics.QueryRangeAll(bucket, now, []string{"historical"})
					require.NoError(t, err)
					require.Len(t, filtered.Items, 1)
					assert.EqualValues(t, 2, filtered.Items[0].RequestCount)
					empty, err := perfmetrics.QueryRangeAll(bucket, now, []string{})
					require.NoError(t, err)
					assert.Empty(t, empty.Items)

					logs := []model.Log{
						{ModelName: "analytics-alpha", Type: model.LogTypeConsume, CreatedAt: now, UseTime: 2, Other: `{"frt":1000}`},
						{ModelName: "analytics-alpha", Type: model.LogTypeConsume, CreatedAt: now, UseTime: 4, Other: `{"frt":3000}`},
						{ModelName: "analytics-alpha", Type: model.LogTypeError, CreatedAt: now, UseTime: 6, Other: `{"frt":25000}`},
						{ModelName: "analytics-alpha", Type: model.LogTypeConsume, CreatedAt: now, Other: `{"frt":0}`},
						{ModelName: "analytics-alpha", Type: model.LogTypeRefund, CreatedAt: now, Other: `{"frt":999999}`},
						{ModelName: "analytics-alpha", Type: model.LogTypeConsume, CreatedAt: bucket - 86400, Other: `{"frt":999999}`},
					}
					require.NoError(t, model.LOG_DB.Create(&logs).Error)
					summaries, err := buildModelAnalyticsSummaries(result.Items, bucket, now)
					require.NoError(t, err)
					require.Len(t, summaries, 2)
					assert.Equal(t, ModelAnalyticsSummary{ModelName: "analytics-alpha", Requests: 6, Errors: 1, AvgTtftMs: 9666, P50TtftMs: 3000, P90TtftMs: 25000, TtftCount: 3, MaxTtftMs: 25000}, summaries[0])
					fallback, err := queryModelAnalyticsLogs(bucket, now)
					require.NoError(t, err)
					require.Len(t, fallback.Items, 1)
					assert.Equal(t, perfmetrics.RangeBucketPoint{ModelName: "analytics-alpha", BucketTs: now - now%1800, RequestCount: 4, ErrorCount: 1, AvgLatencyMs: 3000, AvgTtftMs: 9666, TtftCount: 3, HasTtft: true}, fallback.Items[0])

					recorder := httptest.NewRecorder()
					ctx, _ := gin.CreateTestContext(recorder)
					ctx.Request = httptest.NewRequest("GET", "/api/admin/model-analytics?range=invalid", nil)
					GetModelAnalytics(ctx)
					var response struct {
						Success bool `json:"success"`
						Data    struct {
							Range string                 `json:"range"`
							Items []ModelAnalyticsBucket `json:"items"`
						} `json:"data"`
					}
					require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
					assert.True(t, response.Success)
					assert.Equal(t, "2h", response.Data.Range)
					assert.Len(t, response.Data.Items, 2)

					require.NoError(t, model.LOG_DB.Migrator().DropTable(&model.Log{}))
					_, err = queryModelAnalyticsLogs(bucket, now)
					assert.Error(t, err, "a database failure must not appear as an empty chart")
					_, err = buildModelAnalyticsSummaries(result.Items, bucket, now)
					assert.Error(t, err)
				})
			}
		})
	}
}

func TestModelAnalyticsWeekStartsOnLocalMonday(t *testing.T) {
	zone := time.FixedZone("test", 8*3600)
	for _, day := range []int{14, 16, 20} {
		date := time.Date(2026, 9, day, 17, 30, 0, 0, zone)
		assert.Equal(t, time.Date(2026, 9, 14, 0, 0, 0, 0, zone), startOfWeek(date))
	}
}
