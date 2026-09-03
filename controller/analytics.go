package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

type UserModelUsage struct {
	ModelName    string  `json:"model_name"`
	RequestCount int64   `json:"request_count"`
	TokenCount   int64   `json:"token_count"`
	Consumption  float64 `json:"consumption"`
}

type UserAnalyticsRanking struct {
	UserId       int              `json:"user_id"`
	Username     string           `json:"username"`
	DisplayName  string           `json:"display_name"`
	Remark       string           `json:"remark"`
	Role         int              `json:"role"`
	RequestCount int64            `json:"request_count"`
	TokenCount   int64            `json:"token_count"`
	Consumption  float64          `json:"consumption"`
	Models       []UserModelUsage `json:"models"`
}

type UserAnalyticsResponse struct {
	TotalUsers       int64                  `json:"total_users"`
	ActiveToday      int64                  `json:"active_today"`
	ActivePeriod     int64                  `json:"active_period"`
	TotalConsumption float64                `json:"total_consumption"`
	Rankings         []UserAnalyticsRanking `json:"rankings"`
}

type userAnalyticsTotals struct {
	QuotaSum int64 `gorm:"column:quota_sum"`
}

func analyticsNetQuotaExpression() string {
	return fmt.Sprintf(
		"CASE WHEN type = %d THEN quota WHEN type = %d THEN -quota ELSE 0 END",
		model.LogTypeConsume,
		model.LogTypeRefund,
	)
}

func analyticsAggregateSelect(prefix string) string {
	return fmt.Sprintf(
		"%sSUM(CASE WHEN type = %d THEN 1 ELSE 0 END) AS request_count, "+
			"COALESCE(SUM(CASE WHEN type = %d THEN prompt_tokens + completion_tokens ELSE 0 END), 0) AS token_count, "+
			"COALESCE(SUM(%s), 0) AS quota_sum",
		prefix,
		model.LogTypeConsume,
		model.LogTypeConsume,
		analyticsNetQuotaExpression(),
	)
}

func analyticsLogTypes() []int {
	return []int{model.LogTypeConsume, model.LogTypeRefund}
}

func getUserAnalyticsTotals(periodStart, periodEnd int64) (userAnalyticsTotals, error) {
	query := model.LOG_DB.Model(&model.Log{}).
		Select(fmt.Sprintf(
			"COALESCE(SUM(%s), 0) AS quota_sum",
			analyticsNetQuotaExpression(),
		)).
		Where("type IN ?", analyticsLogTypes())
	if periodStart > 0 {
		query = query.Where("created_at >= ?", periodStart)
	}
	if periodEnd > 0 {
		query = query.Where("created_at <= ?", periodEnd)
	}

	var totals userAnalyticsTotals
	err := query.Scan(&totals).Error
	return totals, err
}

func GetUserAnalytics(c *gin.Context) {
	period := c.DefaultQuery("period", "7d")
	periodStart, periodEnd := getAnalyticsPeriodRange(period)
	if period == "custom" {
		periodStart = parseUnixQuery(c.Query("start"))
		periodEnd = parseUnixQuery(c.Query("end"))
		if periodStart <= 0 || periodEnd < periodStart {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid analytics date range"})
			return
		}
	}
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()

	var totalUsers int64
	if err := model.DB.Model(&model.User{}).Count(&totalUsers).Error; err != nil {
		common.ApiError(c, err)
		return
	}

	var activeToday int64
	if err := model.LOG_DB.Model(&model.Log{}).
		Where("created_at >= ? AND type = ?", todayStart, model.LogTypeConsume).
		Distinct("user_id").
		Count(&activeToday).Error; err != nil {
		common.ApiError(c, err)
		return
	}

	periodQuery := model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume)
	if periodStart > 0 {
		periodQuery = periodQuery.Where("created_at >= ?", periodStart)
	}
	if periodEnd > 0 {
		periodQuery = periodQuery.Where("created_at <= ?", periodEnd)
	}

	var activePeriod int64
	if err := periodQuery.Distinct("user_id").Count(&activePeriod).Error; err != nil {
		common.ApiError(c, err)
		return
	}

	totals, err := getUserAnalyticsTotals(periodStart, periodEnd)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	rows, err := getUserAnalyticsRankingRows(periodStart, periodEnd)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	userIds := extractUserIds(rows)
	usersById := getUsersByIds(userIds)
	modelUsageByUser, err := getUserModelUsage(periodStart, 0, periodEnd, userIds)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	rankings := make([]UserAnalyticsRanking, 0, len(rows))
	for _, row := range rows {
		user := usersById[row.UserId]
		rankings = append(rankings, UserAnalyticsRanking{
			UserId:       row.UserId,
			Username:     user.Username,
			DisplayName:  user.DisplayName,
			Remark:       user.Remark,
			Role:         user.Role,
			RequestCount: row.RequestCount,
			TokenCount:   row.TokenCount,
			Consumption:  float64(row.QuotaSum) / common.QuotaPerUnit,
			Models:       modelUsageByUser[row.UserId],
		})
	}

	common.ApiSuccess(c, UserAnalyticsResponse{
		TotalUsers:       totalUsers,
		ActiveToday:      activeToday,
		ActivePeriod:     activePeriod,
		TotalConsumption: float64(totals.QuotaSum) / common.QuotaPerUnit,
		Rankings:         rankings,
	})
}

func ExportUserAnalytics(c *gin.Context) {
	start := parseUnixQuery(c.Query("start"))
	end := parseUnixQuery(c.Query("end"))
	lang := c.DefaultQuery("lang", "en")

	if start <= 0 && end <= 0 {
		now := time.Now()
		start = now.AddDate(0, 0, -29).Unix()
		end = now.Unix()
	}
	if start <= 0 {
		start = end
	}
	if end <= 0 {
		end = start
	}
	if start > end {
		start, end = end, start
	}

	file := excelize.NewFile()
	if err := writeUserAnalyticsWorkbook(file, start, end, lang); err != nil {
		common.ApiError(c, err)
		return
	}

	var buf bytes.Buffer
	if err := file.Write(&buf); err != nil {
		common.ApiError(c, err)
		return
	}

	filename := fmt.Sprintf("user-analytics-%s.xlsx", time.Now().Format("20060102-150405"))
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buf.Bytes())
}

type userAnalyticsDailyUsageRow struct {
	DayBucket    int64  `gorm:"column:day_bucket"`
	UserId       int    `gorm:"column:user_id"`
	ModelName    string `gorm:"column:model_name"`
	RequestCount int64  `gorm:"column:request_count"`
	TokenCount   int64  `gorm:"column:token_count"`
	QuotaSum     int64  `gorm:"column:quota_sum"`
}

type userAnalyticsDailyAggregate struct {
	DayBucket    int64
	RequestCount int64
	TokenCount   int64
	QuotaSum     int64
	Models       []userAnalyticsDailyUsageRow
}

// writeUserAnalyticsWorkbook creates one worksheet for each user. Each row in
// a worksheet represents that user's usage for a single calendar day.
func writeUserAnalyticsWorkbook(file *excelize.File, start, end int64, lang string) error {
	dayOffset := analyticsExportDayOffset(start)
	rows, err := getUserAnalyticsDailyUsageRows(start, end, dayOffset)
	if err != nil {
		return err
	}

	aggregatesByUser := make(map[int][]*userAnalyticsDailyAggregate)
	dailyByUser := make(map[int]map[int64]*userAnalyticsDailyAggregate)
	userIds := make([]int, 0)
	for _, row := range rows {
		if dailyByUser[row.UserId] == nil {
			dailyByUser[row.UserId] = make(map[int64]*userAnalyticsDailyAggregate)
			userIds = append(userIds, row.UserId)
		}
		daily := dailyByUser[row.UserId][row.DayBucket]
		if daily == nil {
			daily = &userAnalyticsDailyAggregate{DayBucket: row.DayBucket}
			dailyByUser[row.UserId][row.DayBucket] = daily
			aggregatesByUser[row.UserId] = append(aggregatesByUser[row.UserId], daily)
		}
		daily.RequestCount += row.RequestCount
		daily.TokenCount += row.TokenCount
		daily.QuotaSum += row.QuotaSum
		daily.Models = append(daily.Models, row)
	}

	if len(userIds) == 0 {
		return file.SetSheetName(file.GetSheetName(0), "No Data")
	}

	knownUserIds := make([]int, 0, len(userIds))
	for _, userId := range userIds {
		if userId > 0 {
			knownUserIds = append(knownUserIds, userId)
		}
	}
	usersById := getUsersByIds(knownUserIds)

	defaultSheet := file.GetSheetName(0)
	usedSheetNames := make(map[string]struct{}, len(userIds))
	for index, userId := range userIds {
		user := usersById[userId]
		sheetName := uniqueUserAnalyticsSheetName(userId, user, usedSheetNames)
		if index == 0 {
			if err := file.SetSheetName(defaultSheet, sheetName); err != nil {
				return err
			}
		} else if _, err := file.NewSheet(sheetName); err != nil {
			return err
		}
		if err := writeUserAnalyticsSheet(file, sheetName, user, aggregatesByUser[userId], dayOffset, lang); err != nil {
			return err
		}
	}

	return nil
}

func writeUserAnalyticsSheet(
	file *excelize.File,
	sheet string,
	user userAnalyticsUserInfo,
	aggregates []*userAnalyticsDailyAggregate,
	dayOffset int64,
	lang string,
) error {

	headers := analyticsExportHeaders(lang)
	for col, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(col+1, 1)
		if err := file.SetCellStr(sheet, cell, header); err != nil {
			return err
		}
	}

	headerStyle, _ := file.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	dateFormat := "yyyy-mm-dd"
	dateStyle, _ := file.NewStyle(&excelize.Style{CustomNumFmt: &dateFormat})
	wrapStyle, _ := file.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})
	_ = file.SetCellStyle(sheet, "A1", "G1", headerStyle)
	_ = file.SetColWidth(sheet, "A", "A", 13)
	_ = file.SetColWidth(sheet, "B", "B", 22)
	_ = file.SetColWidth(sheet, "C", "F", 16)
	_ = file.SetColWidth(sheet, "G", "G", 48)

	for i, aggregate := range aggregates {
		modelLines := make([]string, 0, len(aggregate.Models))
		for _, m := range aggregate.Models {
			modelLines = append(modelLines, formatModelUsageLine(
				m.ModelName, m.RequestCount, float64(m.QuotaSum)/common.QuotaPerUnit, lang))
		}
		rowNumber := i + 2
		values := []interface{}{
			analyticsExportDate(aggregate.DayBucket, dayOffset),
			analyticsDisplayName(user),
			user.Remark,
			aggregate.RequestCount,
			aggregate.TokenCount,
			float64(aggregate.QuotaSum) / common.QuotaPerUnit,
			strings.Join(modelLines, "\n"),
		}
		for col, value := range values {
			cell, _ := excelize.CoordinatesToCellName(col+1, rowNumber)
			if err := file.SetCellValue(sheet, cell, value); err != nil {
				return err
			}
		}
		dateCell, _ := excelize.CoordinatesToCellName(1, rowNumber)
		_ = file.SetCellStyle(sheet, dateCell, dateCell, dateStyle)
		modelCell, _ := excelize.CoordinatesToCellName(7, rowNumber)
		_ = file.SetCellStyle(sheet, modelCell, modelCell, wrapStyle)
	}

	return nil
}

func analyticsExportDayOffset(timestamp int64) int64 {
	_, offset := time.Unix(timestamp, 0).In(time.Local).Zone()
	return int64(offset)
}

func analyticsExportDate(dayBucket, dayOffset int64) time.Time {
	date := time.Unix(dayBucket-dayOffset, 0).In(time.Local)
	return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.Local)
}

func uniqueUserAnalyticsSheetName(userId int, user userAnalyticsUserInfo, used map[string]struct{}) string {
	name := strings.TrimSpace(safeExcelSheetName(analyticsDisplayName(user)))
	if name == "" {
		name = fmt.Sprintf("User %d", userId)
	}

	for suffixNumber := 1; ; suffixNumber++ {
		candidate := name
		if suffixNumber > 1 {
			suffix := fmt.Sprintf(" (%d)", suffixNumber)
			maxNameLength := 31 - len([]rune(suffix))
			candidateRunes := []rune(name)
			if len(candidateRunes) > maxNameLength {
				candidateRunes = candidateRunes[:maxNameLength]
			}
			candidate = string(candidateRunes) + suffix
		}
		key := strings.ToLower(candidate)
		if _, exists := used[key]; !exists {
			used[key] = struct{}{}
			return candidate
		}
	}
}

// analyticsDisplayName prefers the user's display name, falling back to the
// username when no display name is set.
func analyticsDisplayName(user userAnalyticsUserInfo) string {
	if user.DisplayName != "" {
		return user.DisplayName
	}
	return user.Username
}

func safeExcelSheetName(name string) string {
	replacer := strings.NewReplacer(":", "-", "\\", "-", "/", "-", "?", "", "*", "", "[", "(", "]", ")")
	name = replacer.Replace(name)
	if len([]rune(name)) <= 31 {
		return name
	}
	return string([]rune(name)[:31])
}

// formatModelUsageLine renders one model's request count and net consumption,
// matching the analytics page's Model Usage cell. Refunds are reflected only
// in the consumption amount.
func formatModelUsageLine(modelName string, requestCount int64, consumption float64, lang string) string {
	unit := ""
	if lang == "zh" {
		unit = "次"
	}
	return fmt.Sprintf("%s / %d%s / $%.2f", modelName, requestCount, unit, consumption)
}

func parseUnixQuery(value string) int64 {
	if value == "" {
		return 0
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func analyticsExportHeaders(lang string) []string {
	if lang == "zh" {
		return []string{"日期", "显示名称", "备注", "请求次数", "Token 数", "消费 ($)", "模型用量"}
	}
	return []string{"Date", "Display Name", "Remark", "Requests", "Tokens", "Consumption ($)", "Model Usage"}
}

func getAnalyticsPeriodStart(period string) int64 {
	start, _ := getAnalyticsPeriodRange(period)
	return start
}

func getAnalyticsPeriodRange(period string) (int64, int64) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()

	switch period {
	case "today":
		return todayStart, 0
	case "yesterday":
		return time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, now.Location()).Unix(), todayStart - 1
	case "month":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix(), 0
	case "30d":
		return now.AddDate(0, 0, -30).Unix(), 0
	case "all":
		return 0, 0
	case "7d":
		fallthrough
	default:
		return now.AddDate(0, 0, -7).Unix(), 0
	}
}

type userAnalyticsRankingRow struct {
	UserId       int   `gorm:"column:user_id"`
	RequestCount int64 `gorm:"column:request_count"`
	TokenCount   int64 `gorm:"column:token_count"`
	QuotaSum     int64 `gorm:"column:quota_sum"`
}

func getUserAnalyticsRankingRows(periodStart, periodEnd int64) ([]userAnalyticsRankingRow, error) {
	query := model.LOG_DB.Model(&model.Log{}).
		Select(analyticsAggregateSelect("user_id, ")).
		Where("type IN ?", analyticsLogTypes()).
		Group("user_id").
		Order("quota_sum DESC").
		Limit(100)
	if periodStart > 0 {
		query = query.Where("created_at >= ?", periodStart)
	}
	if periodEnd > 0 {
		query = query.Where("created_at <= ?", periodEnd)
	}

	var rows []userAnalyticsRankingRow
	err := query.Find(&rows).Error
	return rows, err
}

type userModelUsageRow struct {
	UserId       int    `gorm:"column:user_id"`
	ModelName    string `gorm:"column:model_name"`
	RequestCount int64  `gorm:"column:request_count"`
	TokenCount   int64  `gorm:"column:token_count"`
	QuotaSum     int64  `gorm:"column:quota_sum"`
}

// getUserModelUsageRows aggregates consume and refund logs grouped by
// (user_id, model_name), with refunds subtracted from quota.
// periodStart (when > 0) and start/end (when > 0) bound the time range. When
// userIds is non-empty, results are restricted to those users.
func getUserModelUsageRows(periodStart, start, end int64, userIds []int) ([]userModelUsageRow, error) {
	query := model.LOG_DB.Model(&model.Log{}).
		Select(analyticsAggregateSelect("user_id, model_name, ")).
		Where("type IN ?", analyticsLogTypes()).
		Group("user_id, model_name").
		Order("user_id ASC, quota_sum DESC")
	if periodStart > 0 {
		query = query.Where("created_at >= ?", periodStart)
	}
	if start > 0 {
		query = query.Where("created_at >= ?", start)
	}
	if end > 0 {
		query = query.Where("created_at <= ?", end)
	}
	if len(userIds) > 0 {
		query = query.Where("user_id IN ?", userIds)
	}

	var rows []userModelUsageRow
	err := query.Find(&rows).Error
	return rows, err
}

func getUserAnalyticsDailyUsageRows(start, end, dayOffset int64) ([]userAnalyticsDailyUsageRow, error) {
	dayBucketExpr := analyticsDayBucketExpression(dayOffset)
	var rows []userAnalyticsDailyUsageRow
	query := model.LOG_DB.Model(&model.Log{}).
		Select(analyticsAggregateSelect(fmt.Sprintf("user_id, %s AS day_bucket, model_name, ", dayBucketExpr))).
		Where("type IN ?", analyticsLogTypes()).
		Where("created_at >= ? AND created_at <= ?", start, end).
		Group(fmt.Sprintf("user_id, %s, model_name", dayBucketExpr)).
		Order("user_id ASC, day_bucket ASC, quota_sum DESC")
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// analyticsDayBucketExpression groups Unix timestamps by a calendar day in
// the application's local time zone. MySQL needs FLOOR because `/` produces a
// decimal result; SQLite and PostgreSQL use integer division for this input.
func analyticsDayBucketExpression(dayOffset int64) string {
	shiftedCreatedAt := fmt.Sprintf("created_at + %d", dayOffset)
	if common.UsingLogDatabase(common.DatabaseTypeMySQL) {
		return fmt.Sprintf("FLOOR((%s) / 86400) * 86400", shiftedCreatedAt)
	}
	return fmt.Sprintf("((%s) / 86400) * 86400", shiftedCreatedAt)
}

func getUserModelUsage(periodStart, start, end int64, userIds []int) (map[int][]UserModelUsage, error) {
	result := make(map[int][]UserModelUsage)
	if len(userIds) == 0 {
		return result, nil
	}
	rows, err := getUserModelUsageRows(periodStart, start, end, userIds)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.UserId] = append(result[row.UserId], UserModelUsage{
			ModelName:    row.ModelName,
			RequestCount: row.RequestCount,
			TokenCount:   row.TokenCount,
			Consumption:  float64(row.QuotaSum) / common.QuotaPerUnit,
		})
	}
	return result, nil
}

type userAnalyticsUserInfo struct {
	Id          int    `gorm:"column:id"`
	Username    string `gorm:"column:username"`
	DisplayName string `gorm:"column:display_name"`
	Remark      string `gorm:"column:remark"`
	Role        int    `gorm:"column:role"`
}

func extractUserIds(rows []userAnalyticsRankingRow) []int {
	userIds := make([]int, 0, len(rows))
	for _, row := range rows {
		if row.UserId > 0 {
			userIds = append(userIds, row.UserId)
		}
	}
	return userIds
}

func getUsersByIds(userIds []int) map[int]userAnalyticsUserInfo {
	usersById := make(map[int]userAnalyticsUserInfo)
	if len(userIds) == 0 {
		return usersById
	}

	var users []userAnalyticsUserInfo
	if err := model.DB.Model(&model.User{}).
		Select("id, username, display_name, remark, role").
		Where("id IN ?", userIds).
		Find(&users).Error; err != nil {
		common.SysLog("failed to load analytics users: " + err.Error())
		return usersById
	}
	for _, user := range users {
		usersById[user.Id] = user
	}
	return usersById
}
