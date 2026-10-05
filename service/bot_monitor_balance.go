package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/bytedance/gopkg/util/gopool"
)

const botMonitorBalanceScanInterval = 10 * time.Second

var (
	botMonitorBalanceCheckOnce    sync.Once
	botMonitorBalanceCheckRunning atomic.Bool
)

// StartBotMonitorBalanceCheckTask scans configured bindings frequently and
// runs each binding according to its own interval. Only the master node runs
// the task, preventing duplicate alerts in a multi-node deployment.
func StartBotMonitorBalanceCheckTask() {
	botMonitorBalanceCheckOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			ctx := context.Background()
			logger.LogInfo(ctx, fmt.Sprintf("bot monitor balance check task started: scan_interval=%s", botMonitorBalanceScanInterval))
			ticker := time.NewTicker(botMonitorBalanceScanInterval)
			defer ticker.Stop()

			runBotMonitorBalanceCheckOnce()
			for range ticker.C {
				runBotMonitorBalanceCheckOnce()
			}
		})
	})
}

func runBotMonitorBalanceCheckOnce() {
	if !botMonitorBalanceCheckRunning.CompareAndSwap(false, true) {
		return
	}
	defer botMonitorBalanceCheckRunning.Store(false)

	ctx := context.Background()
	bindings, err := model.ListEnabledBotMonitorBalanceBindings()
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("bot monitor balance check: list bindings failed: %v", err))
		return
	}

	for _, binding := range bindings {
		if !shouldRunBotMonitorBalanceBinding(binding, time.Now()) {
			continue
		}
		checkErr := checkBotMonitorBalanceBinding(ctx, binding)
		if updateErr := model.UpdateBotMonitorBalanceBindingLastCheckedAt(binding.Id, time.Now().Unix()); updateErr != nil {
			logger.LogWarn(ctx, fmt.Sprintf("bot monitor balance check: binding_id=%d update last checked time failed: %v", binding.Id, updateErr))
		}
		if checkErr != nil {
			logger.LogWarn(ctx, fmt.Sprintf("bot monitor balance check: binding_id=%d failed: %v", binding.Id, checkErr))
		}
	}
}

func shouldRunBotMonitorBalanceBinding(binding *model.BotMonitorBalanceBinding, now time.Time) bool {
	if binding == nil || binding.LastCheckedAt <= 0 {
		return true
	}
	hours := binding.CheckIntervalHours
	if math.IsNaN(hours) || math.IsInf(hours, 0) || hours < model.MinimumBotMonitorCheckIntervalHours {
		hours = model.DefaultBotMonitorCheckIntervalHours
	}
	interval := time.Duration(hours * float64(time.Hour))
	if interval <= 0 {
		interval = time.Hour
	}
	return now.Sub(time.Unix(binding.LastCheckedAt, 0)) >= interval
}

func checkBotMonitorBalanceBinding(ctx context.Context, binding *model.BotMonitorBalanceBinding) error {
	userIds, err := model.DecodeBotMonitorUserIds(binding.UserIds)
	if err != nil {
		return fmt.Errorf("decode users failed: %w", err)
	}
	users, err := model.ListBotMonitorUsersByIds(userIds)
	if err != nil {
		return fmt.Errorf("list users failed: %w", err)
	}
	lowBalanceUsers := filterBotMonitorLowBalanceUsers(users, binding.ThresholdAmount)
	if len(lowBalanceUsers) == 0 {
		return nil
	}

	robot, err := model.GetBotMonitorRobot(binding.RobotId)
	if err != nil {
		return fmt.Errorf("get robot failed: %w", err)
	}
	if !robot.Enabled {
		return nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	err = sendBotMonitorTextAlert(
		sendCtx,
		robot,
		"用户余额不足提醒",
		buildBotMonitorBalanceText(binding, lowBalanceUsers),
	)
	cancel()
	if err != nil {
		_ = model.UpdateBotMonitorRobotDelivery(robot.Id, "failed", err.Error())
		return fmt.Errorf("send failed: %w", err)
	}
	_ = model.UpdateBotMonitorRobotDelivery(robot.Id, "success", "")
	return nil
}

func filterBotMonitorLowBalanceUsers(users []model.BotMonitorUserOption, thresholdAmount float64) []model.BotMonitorUserOption {
	result := make([]model.BotMonitorUserOption, 0, len(users))
	for _, user := range users {
		if botMonitorQuotaToCNY(user.Quota) < thresholdAmount {
			result = append(result, user)
		}
	}
	return result
}

func botMonitorQuotaToCNY(quota int) float64 {
	rate := operation_setting.USDExchangeRate
	if rate <= 0 {
		rate = 1
	}
	return float64(quota) / common.QuotaPerUnit * rate
}

func buildBotMonitorBalanceText(binding *model.BotMonitorBalanceBinding, users []model.BotMonitorUserOption) string {
	lines := []string{
		fmt.Sprintf("**监控配置：** #%d", binding.Id),
		fmt.Sprintf("**提醒余额：** ¥%.2f", binding.ThresholdAmount),
		fmt.Sprintf("**低余额用户：** %d", len(users)),
		fmt.Sprintf("**检测时间：** %s", time.Now().Format(time.RFC3339)),
		"",
	}
	for _, user := range users {
		name := strings.TrimSpace(user.DisplayName)
		if name == "" || name == user.Username {
			name = user.Username
		} else {
			name = fmt.Sprintf("%s（%s）", user.Username, name)
		}
		lines = append(lines, fmt.Sprintf("- #%d %s：¥%.2f", user.Id, name, botMonitorQuotaToCNY(user.Quota)))
	}
	return strings.Join(lines, "\n")
}
