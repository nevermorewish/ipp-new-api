package controller

import (
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type botMonitorBalanceBindingRequest struct {
	RobotId            int      `json:"robot_id"`
	UserIds            []int    `json:"user_ids"`
	ThresholdAmount    *float64 `json:"threshold_amount"`
	LegacyThreshold    *float64 `json:"threshold,omitempty"`
	CheckIntervalHours *float64 `json:"check_interval_hours"`
	Enabled            bool     `json:"enabled"`
}

func ListBotMonitorBalanceBindings(c *gin.Context) {
	bindings, err := model.ListBotMonitorBalanceBindings()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	robots, err := model.ListBotMonitorRobots()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	robotNames := make(map[int]string, len(robots))
	robotTypes := make(map[int]string, len(robots))
	for _, robot := range robots {
		robotNames[robot.Id] = robot.Name
		robotTypes[robot.Id] = robot.Type
	}

	items := make([]gin.H, 0, len(bindings))
	for _, binding := range bindings {
		userIds, decodeErr := model.DecodeBotMonitorUserIds(binding.UserIds)
		if decodeErr != nil {
			userIds = []int{}
		}
		items = append(items, gin.H{
			"id":                   binding.Id,
			"robot_id":             binding.RobotId,
			"robot_name":           robotNames[binding.RobotId],
			"robot_type":           robotTypes[binding.RobotId],
			"user_ids":             userIds,
			"threshold_amount":     binding.ThresholdAmount,
			"check_interval_hours": binding.CheckIntervalHours,
			"enabled":              binding.Enabled,
			"created_at":           binding.CreatedAt,
			"updated_at":           binding.UpdatedAt,
		})
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

func CreateBotMonitorBalanceBinding(c *gin.Context) {
	var req botMonitorBalanceBindingRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiError(c, err)
		return
	}
	binding, err := buildBotMonitorBalanceBinding(req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.CreateBotMonitorBalanceBinding(binding); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, binding)
}

func UpdateBotMonitorBalanceBinding(c *gin.Context) {
	id, ok := parseBotMonitorID(c, "balance binding")
	if !ok {
		return
	}
	existing, err := model.GetBotMonitorBalanceBinding(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req botMonitorBalanceBindingRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiError(c, err)
		return
	}
	binding, err := buildBotMonitorBalanceBinding(req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	binding.Id = id
	binding.CreatedAt = existing.CreatedAt
	if err := model.UpdateBotMonitorBalanceBinding(binding); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, binding)
}

func DeleteBotMonitorBalanceBinding(c *gin.Context) {
	id, ok := parseBotMonitorID(c, "balance binding")
	if !ok {
		return
	}
	if err := model.DeleteBotMonitorBalanceBinding(id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func ListBotMonitorUserOptions(c *gin.Context) {
	users, err := model.ListBotMonitorUserOptions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": users})
}

func buildBotMonitorBalanceBinding(req botMonitorBalanceBindingRequest) (*model.BotMonitorBalanceBinding, error) {
	if _, err := model.GetBotMonitorRobot(req.RobotId); err != nil {
		return nil, fmt.Errorf("robot not found: %w", err)
	}
	if err := model.ValidateBotMonitorUserIds(req.UserIds); err != nil {
		return nil, err
	}
	thresholdAmount := model.DefaultBotMonitorBalanceThresholdAmount
	if req.ThresholdAmount != nil {
		thresholdAmount = *req.ThresholdAmount
	} else if req.LegacyThreshold != nil {
		thresholdAmount = *req.LegacyThreshold
	}
	if math.IsNaN(thresholdAmount) || math.IsInf(thresholdAmount, 0) || thresholdAmount < 0 {
		return nil, fmt.Errorf("balance threshold amount must be a finite value greater than or equal to 0")
	}
	checkIntervalHours := model.DefaultBotMonitorCheckIntervalHours
	if req.CheckIntervalHours != nil {
		checkIntervalHours = *req.CheckIntervalHours
	}
	if math.IsNaN(checkIntervalHours) || math.IsInf(checkIntervalHours, 0) || checkIntervalHours < model.MinimumBotMonitorCheckIntervalHours || checkIntervalHours > model.MaximumBotMonitorCheckIntervalHours {
		return nil, fmt.Errorf("check interval must be between %.2f and %.0f hours", model.MinimumBotMonitorCheckIntervalHours, model.MaximumBotMonitorCheckIntervalHours)
	}
	encoded, err := model.EncodeBotMonitorUserIds(req.UserIds)
	if err != nil {
		return nil, err
	}
	return &model.BotMonitorBalanceBinding{
		RobotId:            req.RobotId,
		UserIds:            encoded,
		Threshold:          model.DefaultBotMonitorBalanceThreshold,
		ThresholdAmount:    thresholdAmount,
		CheckIntervalHours: checkIntervalHours,
		Enabled:            req.Enabled,
	}, nil
}
