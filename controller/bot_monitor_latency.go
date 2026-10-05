package controller

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type botMonitorLatencyBindingRequest struct {
	Name                     string   `json:"name"`
	RobotId                  int      `json:"robot_id"`
	ModelNames               []string `json:"model_names"`
	FirstTokenTimeoutSeconds int      `json:"first_token_timeout_seconds"`
	MinIntervalSeconds       *int     `json:"min_interval_seconds"`
	Enabled                  bool     `json:"enabled"`
}

func botMonitorLatencyBindingResponse(binding *model.BotMonitorLatencyBinding, robot *model.BotMonitorRobot) gin.H {
	modelNames, err := model.DecodeBotMonitorModelNames(binding.ModelNames)
	if err != nil {
		modelNames = []string{}
	}
	response := gin.H{
		"id":                          binding.Id,
		"name":                        binding.Name,
		"robot_id":                    binding.RobotId,
		"robot_name":                  "",
		"robot_type":                  "",
		"model_names":                 modelNames,
		"first_token_timeout_seconds": binding.FirstTokenTimeoutSeconds,
		"min_interval_seconds":        binding.MinIntervalSeconds,
		"enabled":                     binding.Enabled,
		"created_at":                  binding.CreatedAt,
		"updated_at":                  binding.UpdatedAt,
	}
	if robot != nil {
		response["robot_name"] = robot.Name
		response["robot_type"] = robot.Type
	}
	return response
}

func ListBotMonitorLatencyBindings(c *gin.Context) {
	bindings, err := model.ListBotMonitorLatencyBindings()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	robots, err := model.ListBotMonitorRobots()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	robotByID := make(map[int]*model.BotMonitorRobot, len(robots))
	for _, robot := range robots {
		robotByID[robot.Id] = robot
	}
	items := make([]gin.H, 0, len(bindings))
	for _, binding := range bindings {
		items = append(items, botMonitorLatencyBindingResponse(binding, robotByID[binding.RobotId]))
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

func ListBotMonitorLatencyModelOptions(c *gin.Context) {
	models := model.GetEnabledModels()
	sort.Strings(models)
	common.ApiSuccess(c, gin.H{"items": models})
}

func CreateBotMonitorLatencyBinding(c *gin.Context) {
	var req botMonitorLatencyBindingRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiError(c, err)
		return
	}
	binding, err := buildBotMonitorLatencyBinding(req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.CreateBotMonitorLatencyBinding(binding); err != nil {
		common.ApiError(c, err)
		return
	}
	robot, _ := model.GetBotMonitorRobot(binding.RobotId)
	common.ApiSuccess(c, botMonitorLatencyBindingResponse(binding, robot))
}

func UpdateBotMonitorLatencyBinding(c *gin.Context) {
	id, ok := parseBotMonitorID(c, "latency binding")
	if !ok {
		return
	}
	existing, err := model.GetBotMonitorLatencyBinding(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req botMonitorLatencyBindingRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiError(c, err)
		return
	}
	binding, err := buildBotMonitorLatencyBinding(req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	binding.Id = id
	binding.CreatedAt = existing.CreatedAt
	if err := model.UpdateBotMonitorLatencyBinding(binding); err != nil {
		common.ApiError(c, err)
		return
	}
	updated, err := model.GetBotMonitorLatencyBinding(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	robot, _ := model.GetBotMonitorRobot(updated.RobotId)
	common.ApiSuccess(c, botMonitorLatencyBindingResponse(updated, robot))
}

func DeleteBotMonitorLatencyBinding(c *gin.Context) {
	id, ok := parseBotMonitorID(c, "latency binding")
	if !ok {
		return
	}
	if err := model.DeleteBotMonitorLatencyBinding(id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func buildBotMonitorLatencyBinding(req botMonitorLatencyBindingRequest) (*model.BotMonitorLatencyBinding, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 128 {
		return nil, fmt.Errorf("latency binding name is required and must be at most 128 characters")
	}
	if _, err := model.GetBotMonitorRobot(req.RobotId); err != nil {
		return nil, fmt.Errorf("robot not found: %w", err)
	}
	modelNames := model.NormalizeBotMonitorModelNames(req.ModelNames)
	if len(modelNames) == 0 {
		return nil, fmt.Errorf("select at least one model")
	}
	for _, modelName := range modelNames {
		if len([]rune(modelName)) > 255 {
			return nil, fmt.Errorf("model name must be at most 255 characters")
		}
	}
	timeout := req.FirstTokenTimeoutSeconds
	if timeout <= 0 {
		timeout = model.DefaultBotMonitorFirstTokenTimeoutSeconds
	}
	if timeout < model.MinimumBotMonitorFirstTokenTimeoutSeconds || timeout > model.MaximumBotMonitorFirstTokenTimeoutSeconds {
		return nil, fmt.Errorf("first token timeout must be between %d and %d seconds", model.MinimumBotMonitorFirstTokenTimeoutSeconds, model.MaximumBotMonitorFirstTokenTimeoutSeconds)
	}
	interval := model.DefaultBotMonitorFirstTokenMinIntervalSeconds
	if req.MinIntervalSeconds != nil {
		interval = *req.MinIntervalSeconds
	}
	if interval < 0 || interval > 86400 {
		return nil, fmt.Errorf("minimum interval must be between 0 and 86400 seconds")
	}
	encoded, err := model.EncodeBotMonitorModelNames(modelNames)
	if err != nil {
		return nil, err
	}
	return &model.BotMonitorLatencyBinding{
		Name: name, RobotId: req.RobotId, ModelNames: encoded,
		FirstTokenTimeoutSeconds: timeout, MinIntervalSeconds: interval, Enabled: req.Enabled,
	}, nil
}
