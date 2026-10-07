package controller

import (
	"math"
	"slices"
	"sort"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/gin-gonic/gin"
)

type ModelAnalyticsBucket struct {
	ModelName    string `json:"model_name"`
	BucketTs     int64  `json:"bucket_ts"`
	RequestCount int64  `json:"request_count"`
	ErrorCount   int64  `json:"error_count"`
	AvgLatencyMs int64  `json:"avg_latency_ms"`
	AvgTtftMs    int64  `json:"avg_ttft_ms"`
	TtftCount    int64  `json:"ttft_count"`
	HasTtft      bool   `json:"has_ttft"`
}

type ModelAnalyticsSummary struct {
	ModelName string `json:"model_name"`
	Requests  int64  `json:"requests"`
	Errors    int64  `json:"errors"`
	AvgTtftMs int64  `json:"avg_ttft_ms"`
	P50TtftMs int64  `json:"ttft_p50_ms"`
	P90TtftMs int64  `json:"ttft_p90_ms"`
	TtftCount int64  `json:"ttft_count"`
	MaxTtftMs int64  `json:"max_ttft_ms"`
}

var modelAnalyticsRanges = map[string]time.Duration{
	"1h": time.Hour,
	"2h": 2 * time.Hour,
	"4h": 4 * time.Hour,
	"1d": 24 * time.Hour,
}

// GetModelAnalytics returns per-model, per-bucket request/latency/TTFT
// aggregates for the selected range (1h/2h/4h/1d/week, default 2h).
func GetModelAnalytics(c *gin.Context) {
	rangeParam := c.Query("range")
	now := time.Now()
	var start time.Time
	if rangeParam == "week" {
		start = startOfWeek(now)
	} else if d, ok := modelAnalyticsRanges[rangeParam]; ok {
		start = now.Add(-d)
	} else {
		rangeParam = "2h"
		start = now.Add(-modelAnalyticsRanges["2h"])
	}

	// Model analytics should include every group that has recorded metrics. The
	// configured ratio groups describe current routing availability, but they
	// can change over time (and older metrics may belong to a group that is no
	// longer configured). Filtering by that map made the page appear empty even
	// though perf_metrics contained data. QueryRangeAll treats nil as no group
	// filter, so historical and custom groups remain visible here.
	result, err := perfmetrics.QueryRangeAll(start.Unix(), now.Unix(), nil)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if len(result.Items) == 0 {
		result, err = queryModelAnalyticsLogs(start.Unix(), now.Unix())
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}

	rows := make([]ModelAnalyticsBucket, 0, len(result.Items))
	for _, item := range result.Items {
		rows = append(rows, ModelAnalyticsBucket{
			ModelName:    item.ModelName,
			BucketTs:     item.BucketTs,
			RequestCount: item.RequestCount,
			ErrorCount:   item.ErrorCount,
			AvgLatencyMs: item.AvgLatencyMs,
			AvgTtftMs:    item.AvgTtftMs,
			TtftCount:    item.TtftCount,
			HasTtft:      item.HasTtft,
		})
	}
	summaries, err := buildModelAnalyticsSummaries(result.Items, start.Unix(), now.Unix())
	if err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, gin.H{
		"start":           start.Unix(),
		"end":             now.Unix(),
		"bucket_seconds":  result.BucketSeconds,
		"range":           rangeParam,
		"items":           rows,
		"model_summaries": summaries,
	})
}

type modelAnalyticsSummaryAccumulator struct {
	requests int64
	errors   int64
	avgSum   int64
	ttftN    int64
	values   []int64
}

func buildModelAnalyticsSummaries(items []perfmetrics.RangeBucketPoint, startTs, endTs int64) ([]ModelAnalyticsSummary, error) {
	byModel := make(map[string]*modelAnalyticsSummaryAccumulator)
	for _, item := range items {
		acc := byModel[item.ModelName]
		if acc == nil {
			acc = &modelAnalyticsSummaryAccumulator{}
			byModel[item.ModelName] = acc
		}
		acc.requests += item.RequestCount
		acc.errors += item.ErrorCount
		if item.TtftCount > 0 {
			acc.avgSum += item.AvgTtftMs * item.TtftCount
			acc.ttftN += item.TtftCount
		}
	}
	if model.LOG_DB != nil && len(byModel) > 0 {
		modelNames := make([]string, 0, len(byModel))
		for name := range byModel {
			if name != "" {
				modelNames = append(modelNames, name)
			}
		}
		var logs []struct {
			ModelName string
			Other     string
		}
		query := model.LOG_DB.Model(&model.Log{}).
			Select("model_name, other").
			Where("created_at >= ? AND created_at <= ? AND type IN ?", startTs, endTs, []int{model.LogTypeConsume, model.LogTypeError})
		if len(modelNames) > 0 {
			query = query.Where("model_name IN ?", modelNames)
		}
		if err := query.Find(&logs).Error; err != nil {
			return nil, err
		}
		for _, log := range logs {
			var other struct {
				FRT float64 `json:"frt"`
			}
			if common.UnmarshalJsonStr(log.Other, &other) != nil || other.FRT <= 0 {
				continue
			}
			acc := byModel[log.ModelName]
			if acc == nil {
				acc = &modelAnalyticsSummaryAccumulator{}
				byModel[log.ModelName] = acc
			}
			acc.values = append(acc.values, int64(other.FRT))
		}
	}
	result := make([]ModelAnalyticsSummary, 0, len(byModel))
	for name, acc := range byModel {
		if len(acc.values) > 0 {
			slices.Sort(acc.values)
			acc.avgSum = 0
			for _, value := range acc.values {
				acc.avgSum += value
			}
			acc.ttftN = int64(len(acc.values))
		}
		avg := int64(0)
		if acc.ttftN > 0 {
			avg = acc.avgSum / acc.ttftN
		}
		p50, p90, max := int64(0), int64(0), int64(0)
		if len(acc.values) > 0 {
			p50 = percentileInt64(acc.values, 0.50)
			p90 = percentileInt64(acc.values, 0.90)
			max = acc.values[len(acc.values)-1]
		}
		result = append(result, ModelAnalyticsSummary{ModelName: name, Requests: acc.requests, Errors: acc.errors, AvgTtftMs: avg, P50TtftMs: p50, P90TtftMs: p90, TtftCount: acc.ttftN, MaxTtftMs: max})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Requests > result[j].Requests })
	return result, nil
}

func percentileInt64(values []int64, quantile float64) int64 {
	if len(values) == 0 {
		return 0
	}
	index := min(max(int(math.Ceil(float64(len(values))*quantile))-1, 0), len(values)-1)
	return values[index]
}

// queryModelAnalyticsLogs keeps analytics useful for installations upgraded
// before perf_metrics was introduced, or while the first flush is pending.
func queryModelAnalyticsLogs(startTs, endTs int64) (perfmetrics.RangeAllResult, error) {
	if model.LOG_DB == nil {
		return perfmetrics.RangeAllResult{BucketSeconds: 1800}, nil
	}
	var logs []struct {
		ModelName string
		CreatedAt int64
		UseTime   int
		Type      int
		Other     string
	}
	if err := model.LOG_DB.Model(&model.Log{}).Select("model_name, created_at, use_time, type, other").
		Where("created_at >= ? AND created_at <= ? AND type IN ?", startTs, endTs, []int{model.LogTypeConsume, model.LogTypeError}).Find(&logs).Error; err != nil {
		return perfmetrics.RangeAllResult{}, err
	}
	type aggregate struct{ requests, errors, latency, ttftSum, ttftCount int64 }
	groups := map[string]map[int64]*aggregate{}
	for _, log := range logs {
		bucket := (log.CreatedAt / 1800) * 1800
		byBucket := groups[log.ModelName]
		if byBucket == nil {
			byBucket = map[int64]*aggregate{}
			groups[log.ModelName] = byBucket
		}
		a := byBucket[bucket]
		if a == nil {
			a = &aggregate{}
			byBucket[bucket] = a
		}
		a.requests++
		if log.Type == model.LogTypeError {
			a.errors++
		}
		if log.UseTime > 0 {
			a.latency += int64(log.UseTime) * 1000
		}
		var other struct {
			FRT float64 `json:"frt"`
		}
		if common.UnmarshalJsonStr(log.Other, &other) == nil && other.FRT > 0 {
			a.ttftSum += int64(other.FRT)
			a.ttftCount++
		}
	}
	items := make([]perfmetrics.RangeBucketPoint, 0)
	for name, buckets := range groups {
		for ts, a := range buckets {
			latencyAvg := int64(0)
			if a.requests > 0 {
				latencyAvg = a.latency / a.requests
			}
			ttftAvg := int64(0)
			if a.ttftCount > 0 {
				ttftAvg = a.ttftSum / a.ttftCount
			}
			items = append(items, perfmetrics.RangeBucketPoint{
				ModelName: name, BucketTs: ts, RequestCount: a.requests, ErrorCount: a.errors,
				AvgLatencyMs: latencyAvg, AvgTtftMs: ttftAvg, TtftCount: a.ttftCount, HasTtft: a.ttftCount > 0,
			})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].BucketTs == items[j].BucketTs {
			return items[i].ModelName < items[j].ModelName
		}
		return items[i].BucketTs < items[j].BucketTs
	})
	return perfmetrics.RangeAllResult{Items: items, BucketSeconds: 1800}, nil
}

// startOfWeek returns local-time Monday 00:00 of the week containing t.
func startOfWeek(t time.Time) time.Time {
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	return d.AddDate(0, 0, -(weekday - 1))
}
