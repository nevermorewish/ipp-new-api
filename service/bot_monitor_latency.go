package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// StartBotMonitorFirstTokenWatch starts one lightweight timer for each
// configured model binding. The returned function cancels all timers and must
// be called when the relay request finishes.
func StartBotMonitorFirstTokenWatch(ctx context.Context, info *relaycommon.RelayInfo) func() {
	if info == nil || !info.IsStream || strings.TrimSpace(info.OriginModelName) == "" {
		return func() {}
	}
	bindings, err := model.ListEnabledBotMonitorLatencyBindings()
	if err != nil || len(bindings) == 0 {
		if err != nil {
			common.SysLog("failed to list first-token monitor bindings: " + err.Error())
		}
		return func() {}
	}

	type watch struct {
		cancel context.CancelFunc
	}
	watches := make([]watch, 0, len(bindings))
	for _, binding := range bindings {
		modelNames, decodeErr := model.DecodeBotMonitorModelNames(binding.ModelNames)
		if decodeErr != nil || (!matchesBotMonitorModel(modelNames, info.OriginModelName)) {
			continue
		}
		robot, getErr := model.GetBotMonitorRobot(binding.RobotId)
		if getErr != nil || robot == nil || !robot.Enabled {
			continue
		}
		timeoutSeconds := binding.FirstTokenTimeoutSeconds
		if timeoutSeconds <= 0 {
			timeoutSeconds = model.DefaultBotMonitorFirstTokenTimeoutSeconds
		}
		intervalSeconds := binding.MinIntervalSeconds
		if intervalSeconds < 0 {
			intervalSeconds = model.DefaultBotMonitorFirstTokenMinIntervalSeconds
		}
		watchCtx, cancel := context.WithCancel(ctx)
		watches = append(watches, watch{cancel: cancel})
		go runBotMonitorFirstTokenWatch(watchCtx, info, binding, robot, timeoutSeconds, intervalSeconds)
	}
	return func() {
		for _, item := range watches {
			item.cancel()
		}
	}
}

func matchesBotMonitorModel(modelNames []string, modelName string) bool {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return false
	}
	for _, candidate := range modelNames {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == modelName {
			return true
		}
	}
	return false
}

func runBotMonitorFirstTokenWatch(ctx context.Context, info *relaycommon.RelayInfo, binding *model.BotMonitorLatencyBinding, robot *model.BotMonitorRobot, timeoutSeconds, intervalSeconds int) {
	remaining := time.Duration(timeoutSeconds)*time.Second - time.Since(info.StartTime)
	if remaining < 0 {
		remaining = 0
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return
	}
	if info.HasSendResponse() {
		return
	}
	allowed, release := acquireBotMonitorLatencySilence(ctx, binding.Id, robot.Id, info.OriginModelName, intervalSeconds)
	if !allowed {
		return
	}
	alertText := buildBotMonitorFirstTokenText(binding, info, timeoutSeconds)
	sendCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	err := sendBotMonitorTextAlert(sendCtx, robot, "First token latency alert", alertText)
	cancel()
	if err != nil {
		release()
		_ = model.UpdateBotMonitorRobotDelivery(robot.Id, "failed", err.Error())
		common.SysLog(fmt.Sprintf("failed to send first-token monitor alert for robot %s: %v", robot.Name, err))
		return
	}
	_ = model.UpdateBotMonitorRobotDelivery(robot.Id, "success", "")
}

func buildBotMonitorFirstTokenText(binding *model.BotMonitorLatencyBinding, info *relaycommon.RelayInfo, timeoutSeconds int) string {
	elapsed := time.Since(info.StartTime)
	lines := []string{
		fmt.Sprintf("> Model: %s", info.OriginModelName),
		fmt.Sprintf("> Threshold: %d seconds", timeoutSeconds),
		fmt.Sprintf("> Elapsed: %.1f seconds", elapsed.Seconds()),
	}
	if binding != nil && binding.Name != "" {
		lines = append(lines, fmt.Sprintf("> Monitor: %s", binding.Name))
	}
	if info.RequestId != "" {
		lines = append(lines, fmt.Sprintf("> Request ID: %s", info.RequestId))
	}
	if requestPath := botMonitorAlertRequestPath(info.RequestURLPath); requestPath != "" {
		lines = append(lines, fmt.Sprintf("> Path: %s", requestPath))
	}
	if info.UserGroup != "" {
		lines = append(lines, fmt.Sprintf("> Group: %s", info.UserGroup))
	}
	lines = append(lines, fmt.Sprintf("> Time: %s", time.Now().Format(time.RFC3339)))
	return strings.Join(lines, "\n")
}

// RelayInfo keeps the full request URL for internal diagnostics. Robot alerts
// should only include the path so query parameters (which may contain API
// keys) are never forwarded to an external chat.
func botMonitorAlertRequestPath(raw string) string {
	if index := strings.IndexByte(raw, '?'); index >= 0 {
		return raw[:index]
	}
	return raw
}

func acquireBotMonitorLatencySilence(ctx context.Context, bindingID, robotID int, modelName string, intervalSeconds int) (bool, func()) {
	if intervalSeconds <= 0 {
		return true, func() {}
	}
	fingerprint := fmt.Sprintf("latency:%d:%d:%s", bindingID, robotID, modelName)
	hash := sha256.Sum256([]byte(fingerprint))
	key := fmt.Sprintf("huanxing:bot-monitor:latency-silence:v1:%x", hash[:16])
	return acquireBotMonitorSilenceKey(ctx, key, time.Duration(intervalSeconds)*time.Second)
}

func acquireBotMonitorSilenceKey(ctx context.Context, key string, ttl time.Duration) (bool, func()) {
	owner := fmt.Sprintf("%d", time.Now().UnixNano())
	if common.RedisEnabled && common.RDB != nil {
		acquired, err := common.RDB.SetNX(ctx, key, owner, ttl).Result()
		if err == nil {
			if !acquired {
				return false, func() {}
			}
			return true, func() {
				const script = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`
				_ = common.RDB.Eval(context.Background(), script, []string{key}, owner).Err()
			}
		}
	}
	now := time.Now()
	botMonitorSilenceStore.Lock()
	defer botMonitorSilenceStore.Unlock()
	for savedKey, entry := range botMonitorSilenceStore.entries {
		if !entry.expiresAt.After(now) {
			delete(botMonitorSilenceStore.entries, savedKey)
		}
	}
	if entry, exists := botMonitorSilenceStore.entries[key]; exists && entry.expiresAt.After(now) {
		return false, func() {}
	}
	botMonitorSilenceStore.entries[key] = botMonitorSilenceEntry{expiresAt: now.Add(ttl), owner: owner}
	return true, func() {
		botMonitorSilenceStore.Lock()
		defer botMonitorSilenceStore.Unlock()
		if entry, exists := botMonitorSilenceStore.entries[key]; exists && entry.owner == owner {
			delete(botMonitorSilenceStore.entries, key)
		}
	}
}
