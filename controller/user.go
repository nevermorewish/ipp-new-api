package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

var (
	errUserPasswordUnset    = errors.New("user password is not set")
	errOriginalPasswordFail = errors.New("original password is incorrect")
)

func Login(c *gin.Context) {
	if !common.PasswordLoginEnabled {
		common.ApiErrorI18n(c, i18n.MsgUserPasswordLoginDisabled)
		return
	}
	var loginRequest LoginRequest
	err := json.NewDecoder(c.Request.Body).Decode(&loginRequest)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	username := loginRequest.Username
	password := loginRequest.Password
	if username == "" || password == "" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	user := model.User{
		Username: username,
		Password: password,
	}
	err = user.ValidateAndFill()
	if err != nil {
		switch {
		case errors.Is(err, model.ErrDatabase):
			common.SysLog(fmt.Sprintf("Login database error for user %s: %v", username, err))
			common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		case errors.Is(err, model.ErrUserEmptyCredentials):
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		default:
			common.ApiErrorI18n(c, i18n.MsgUserUsernameOrPasswordError)
		}
		return
	}

	// 检查是否启用2FA
	twoFAEnabled, err := model.IsTwoFAEnabled(user.Id)
	if err != nil {
		common.SysLog(fmt.Sprintf("Login failed to load 2FA status for user %d: %v", user.Id, err))
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	if twoFAEnabled {
		// 设置pending session，等待2FA验证
		session := sessions.Default(c)
		session.Set("pending_username", user.Username)
		session.Set("pending_user_id", user.Id)
		err := session.Save()
		if err != nil {
			common.ApiErrorI18n(c, i18n.MsgUserSessionSaveFailed)
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message": i18n.T(c, i18n.MsgUserRequire2FA),
			"success": true,
			"data": map[string]interface{}{
				"require_2fa": true,
			},
		})
		return
	}

	setupLogin(&user, c)
}

// loginMethodFromContext 根据请求路径推导登录方式，用于登录审计日志。
func loginMethodFromContext(c *gin.Context) string {
	switch c.FullPath() {
	case "/api/user/login":
		return "password"
	case "/api/user/login/2fa":
		return "2fa"
	case "/api/user/passkey/login/finish":
		return "passkey"
	case "/api/oauth/wechat":
		return "wechat"
	case "/api/oauth/telegram/login":
		return "telegram"
	case "/api/oauth/:provider":
		if provider := c.Param("provider"); provider != "" {
			return "oauth:" + provider
		}
		return "oauth"
	default:
		return "unknown"
	}
}

// recordLoginAudit 记录登录成功审计日志（对所有用户启用，仅记录成功，不记录失败）。
func recordLoginAudit(user *model.User, c *gin.Context) {
	method := loginMethodFromContext(c)
	ip := c.ClientIP()
	extra := map[string]interface{}{
		"login_method": method,
		"user_agent":   c.Request.UserAgent(),
	}
	content := fmt.Sprintf("Logged in successfully via %s", method)
	model.RecordLoginLog(user.Id, user.Username, content, ip, "login", map[string]interface{}{
		"method": method,
	}, extra)
}

// setup session & cookies and then return user info
func setupLogin(user *model.User, c *gin.Context) {
	model.UpdateUserLastLoginAt(user.Id)
	session := sessions.Default(c)
	session.Set("id", user.Id)
	session.Set("username", user.Username)
	session.Set("role", user.Role)
	session.Set("status", user.Status)
	session.Set("group", user.Group)
	err := session.Save()
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgUserSessionSaveFailed)
		return
	}
	recordLoginAudit(user, c)
	c.JSON(http.StatusOK, gin.H{
		"message": "",
		"success": true,
		"data": map[string]any{
			"id":              user.Id,
			"username":        user.Username,
			"display_name":    user.DisplayName,
			"role":            user.Role,
			"status":          user.Status,
			"group":           user.Group,
			"type":            user.Type,
			"topid":           user.Topid,
			"enterprise_id":   user.EnterpriseId,
			"enterprise_name": user.EnterpriseName,
		},
	})
}

func Logout(c *gin.Context) {
	session := sessions.Default(c)
	session.Clear()
	err := session.Save()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"message": err.Error(),
			"success": false,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "",
		"success": true,
	})
}

func Register(c *gin.Context) {
	if !common.RegisterEnabled {
		common.ApiErrorI18n(c, i18n.MsgUserRegisterDisabled)
		return
	}
	if !common.PasswordRegisterEnabled {
		common.ApiErrorI18n(c, i18n.MsgUserPasswordRegisterDisabled)
		return
	}
	var user model.User
	err := json.NewDecoder(c.Request.Body).Decode(&user)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	user.Username = strings.TrimSpace(user.Username)
	user.Email = model.NormalizeEmail(user.Email)
	if user.Username == "" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err := common.Validate.Struct(&user); err != nil {
		common.ApiErrorI18n(c, i18n.MsgUserInputInvalid, map[string]any{"Error": err.Error()})
		return
	}
	if common.EmailVerificationEnabled {
		if user.Email == "" || user.VerificationCode == "" {
			common.ApiErrorI18n(c, i18n.MsgUserEmailVerificationRequired)
			return
		}
		if !common.VerifyCodeWithKey(user.Email, user.VerificationCode, common.EmailVerificationPurpose) {
			common.ApiErrorI18n(c, i18n.MsgUserVerificationCodeError)
			return
		}
		if err := model.EnsureEmailAvailable(user.Email, 0); err != nil {
			if errors.Is(err, model.ErrEmailAlreadyTaken) {
				common.ApiErrorI18n(c, i18n.MsgUserEmailAlreadyTaken)
				return
			}
			common.ApiErrorI18n(c, i18n.MsgDatabaseError)
			return
		}
	}
	emailForExistCheck := ""
	if common.EmailVerificationEnabled {
		emailForExistCheck = user.Email
	}
	exist, err := model.CheckUserExistOrDeleted(user.Username, emailForExistCheck)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		common.SysLog(fmt.Sprintf("CheckUserExistOrDeleted error: %v", err))
		return
	}
	if exist {
		common.ApiErrorI18n(c, i18n.MsgUserExists)
		return
	}
	affCode := user.AffCode // this code is the inviter's code, not the user's own code
	inviterId, _ := model.GetUserIdByAffCode(affCode)
	cleanUser := model.User{
		Username:    user.Username,
		Password:    user.Password,
		DisplayName: user.Username,
		InviterId:   inviterId,
		Role:        common.RoleCommonUser, // 明确设置角色为普通用户
	}
	if common.EmailVerificationEnabled {
		cleanUser.Email = user.Email
	}
	if err := cleanUser.Insert(inviterId); err != nil {
		if errors.Is(err, model.ErrEmailAlreadyTaken) {
			common.ApiErrorI18n(c, i18n.MsgUserEmailAlreadyTaken)
			return
		}
		common.ApiError(c, err)
		return
	}

	// 获取插入后的用户ID
	var insertedUser model.User
	if err := model.DB.Where("username = ?", cleanUser.Username).First(&insertedUser).Error; err != nil {
		common.ApiErrorI18n(c, i18n.MsgUserRegisterFailed)
		return
	}
	// 生成默认令牌
	if constant.GenerateDefaultToken {
		key, err := common.GenerateKey()
		if err != nil {
			common.ApiErrorI18n(c, i18n.MsgUserDefaultTokenFailed)
			common.SysLog("failed to generate token key: " + err.Error())
			return
		}
		// 生成默认令牌
		token := model.Token{
			UserId:             insertedUser.Id, // 使用插入后的用户ID
			Name:               cleanUser.Username + "的初始令牌",
			Key:                key,
			CreatedTime:        common.GetTimestamp(),
			AccessedTime:       common.GetTimestamp(),
			ExpiredTime:        -1,     // 永不过期
			RemainQuota:        500000, // 示例额度
			UnlimitedQuota:     true,
			ModelLimitsEnabled: false,
		}
		if setting.DefaultUseAutoGroup {
			token.Group = "auto"
		}
		if err := token.Insert(); err != nil {
			common.ApiErrorI18n(c, i18n.MsgCreateDefaultTokenErr)
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func GenerateDefaultTokenForUser(userId int, username string) error {
	key, err := common.GenerateKey()
	if err != nil {
		common.SysLog("failed to generate token key: " + err.Error())
		return err
	}
	token := model.Token{
		UserId:             userId,
		Name:               username + "的初始令牌",
		Key:                key,
		CreatedTime:        common.GetTimestamp(),
		AccessedTime:       common.GetTimestamp(),
		ExpiredTime:        -1,
		RemainQuota:        500000,
		UnlimitedQuota:     true,
		ModelLimitsEnabled: false,
	}
	if setting.DefaultUseAutoGroup {
		token.Group = "auto"
	}
	return token.Insert()
}

func GetAllUsers(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	sortOptions := model.NewUserSortOptions(c.Query("sort_by"), c.Query("sort_order"))
	users, total, err := model.GetAllUsers(pageInfo, sortOptions)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(users)

	common.ApiSuccess(c, pageInfo)
	return
}

func SearchUsers(c *gin.Context) {
	keyword := c.Query("keyword")
	group := c.Query("group")
	var role *int
	if roleStr := c.Query("role"); roleStr != "" {
		if parsed, err := strconv.Atoi(roleStr); err == nil {
			role = &parsed
		}
	}
	var status *int
	if statusStr := c.Query("status"); statusStr != "" {
		if parsed, err := strconv.Atoi(statusStr); err == nil {
			status = &parsed
		}
	}
	pageInfo := common.GetPageQuery(c)
	sortOptions := model.NewUserSortOptions(c.Query("sort_by"), c.Query("sort_order"))
	users, total, err := model.SearchUsers(keyword, group, role, status, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), sortOptions)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(users)
	common.ApiSuccess(c, pageInfo)
	return
}

func canManageTargetRole(myRole int, targetRole int) bool {
	return myRole == common.RoleRootUser || myRole > targetRole
}

func GetUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, user.Role) {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionSameLevel)
		return
	}
	user.AdminPermissions = authz.Capabilities(user.Id, user.Role)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    user,
	})
	return
}

func GenerateAccessToken(c *gin.Context) {
	id := c.GetInt("id")
	user, err := model.GetUserById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// get rand int 28-32
	randI := common.GetRandomInt(4)
	key, err := common.GenerateRandomKey(29 + randI)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgGenerateFailed)
		common.SysLog("failed to generate key: " + err.Error())
		return
	}
	user.SetAccessToken(key)

	if model.DB.Where("access_token = ?", user.AccessToken).First(user).RowsAffected != 0 {
		common.ApiErrorI18n(c, i18n.MsgUuidDuplicate)
		return
	}

	if err := user.Update(false); err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    user.AccessToken,
	})
	return
}

type TransferAffQuotaRequest struct {
	Quota int `json:"quota" binding:"required"`
}

func TransferAffQuota(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	id := c.GetInt("id")
	user, err := model.GetUserById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	tran := TransferAffQuotaRequest{}
	if err := c.ShouldBindJSON(&tran); err != nil {
		common.ApiError(c, err)
		return
	}
	err = user.TransferAffQuotaToQuota(tran.Quota)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgUserTransferFailed, map[string]any{"Error": err.Error()})
		return
	}
	common.ApiSuccessI18n(c, i18n.MsgUserTransferSuccess, nil)
}

func GetAffCode(c *gin.Context) {
	id := c.GetInt("id")
	user, err := model.GetUserById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if user.AffCode == "" {
		user.AffCode = common.GetRandomString(4)
		if err := user.Update(false); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    user.AffCode,
	})
	return
}

func GetSelf(c *gin.Context) {
	id := c.GetInt("id")
	userRole := c.GetInt("role")
	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// Hide admin remarks: set to empty to trigger omitempty tag, ensuring the remark field is not included in JSON returned to regular users
	user.Remark = ""

	// 计算用户权限信息
	permissions := calculateUserPermissions(userRole)
	permissions["admin_permissions"] = authz.Capabilities(id, userRole)

	// 获取用户设置并提取sidebar_modules
	userSetting := user.GetSetting()

	// 构建响应数据，包含用户信息和权限
	parentUsername := ""
	if user.Type >= 2 && user.Topid > 0 {
		if parent, err := model.GetUserById(user.Topid, false); err == nil {
			parentUsername = parent.Username
		}
	}

	responseData := map[string]interface{}{
		"id":                user.Id,
		"username":          user.Username,
		"display_name":      user.DisplayName,
		"role":              user.Role,
		"status":            user.Status,
		"email":             user.Email,
		"github_id":         user.GitHubId,
		"discord_id":        user.DiscordId,
		"oidc_id":           user.OidcId,
		"wechat_id":         user.WeChatId,
		"telegram_id":       user.TelegramId,
		"group":             user.Group,
		"type":              user.Type,
		"topid":             user.Topid,
		"enterprise_id":     user.EnterpriseId,
		"enterprise_name":   user.EnterpriseName,
		"parent_username":   parentUsername,
		"quota":             user.Quota,
		"used_quota":        user.UsedQuota,
		"request_count":     user.RequestCount,
		"aff_code":          user.AffCode,
		"aff_count":         user.AffCount,
		"aff_quota":         user.AffQuota,
		"aff_history_quota": user.AffHistoryQuota,
		"inviter_id":        user.InviterId,
		"linux_do_id":       user.LinuxDOId,
		"setting":           user.Setting,
		"stripe_customer":   user.StripeCustomer,
		"sidebar_modules":   userSetting.SidebarModules, // 正确提取sidebar_modules字段
		"permissions":       permissions,                // 新增权限字段
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    responseData,
	})
	return
}

// 计算用户权限的辅助函数
func calculateUserPermissions(userRole int) map[string]interface{} {
	permissions := map[string]interface{}{}

	// 根据用户角色计算权限
	if userRole == common.RoleRootUser {
		// 超级管理员不需要边栏设置功能
		permissions["sidebar_settings"] = false
		permissions["sidebar_modules"] = map[string]interface{}{}
	} else if userRole == common.RoleAdminUser {
		// 管理员可以设置边栏，但不包含系统设置功能
		permissions["sidebar_settings"] = true
		permissions["sidebar_modules"] = map[string]interface{}{
			"admin": map[string]interface{}{
				"setting": false, // 管理员不能访问系统设置
			},
		}
	} else {
		// 普通用户只能设置个人功能，不包含管理员区域
		permissions["sidebar_settings"] = true
		permissions["sidebar_modules"] = map[string]interface{}{
			"admin": false, // 普通用户不能访问管理员区域
		}
	}

	return permissions
}

// 根据用户角色生成默认的边栏配置
func generateDefaultSidebarConfig(userRole int) string {
	defaultConfig := map[string]interface{}{}

	// 聊天区域 - 所有用户都可以访问
	defaultConfig["chat"] = map[string]interface{}{
		"enabled":    true,
		"playground": true,
		"chat":       true,
	}

	// 控制台区域 - 所有用户都可以访问
	defaultConfig["console"] = map[string]interface{}{
		"enabled":    true,
		"detail":     true,
		"token":      true,
		"log":        true,
		"midjourney": true,
		"task":       true,
	}

	// 个人中心区域 - 所有用户都可以访问
	defaultConfig["personal"] = map[string]interface{}{
		"enabled":  true,
		"topup":    true,
		"personal": true,
	}

	// 管理员区域 - 根据角色决定
	if userRole == common.RoleAdminUser {
		// 管理员可以访问管理员区域，但不能访问系统设置
		defaultConfig["admin"] = map[string]interface{}{
			"enabled":    true,
			"channel":    true,
			"models":     true,
			"redemption": true,
			"user":       true,
			"setting":    false, // 管理员不能访问系统设置
		}
	} else if userRole == common.RoleRootUser {
		// 超级管理员可以访问所有功能
		defaultConfig["admin"] = map[string]interface{}{
			"enabled":    true,
			"channel":    true,
			"models":     true,
			"redemption": true,
			"user":       true,
			"setting":    true,
		}
	}
	// 普通用户不包含admin区域

	// 转换为JSON字符串
	configBytes, err := json.Marshal(defaultConfig)
	if err != nil {
		common.SysLog("生成默认边栏配置失败: " + err.Error())
		return ""
	}

	return string(configBytes)
}

func GetUserModels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		id = c.GetInt("id")
	}
	user, err := model.GetUserCache(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	groups := service.GetUserUsableGroups(user.Group)
	group := c.Query("group")
	if group != "" {
		if _, ok := groups[group]; !ok {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"message": "",
				"data":    []string{},
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    model.GetGroupEnabledModels(group),
		})
		return
	}

	var models []string
	for group := range groups {
		for _, g := range model.GetGroupEnabledModels(group) {
			if !common.StringsContains(models, g) {
				models = append(models, g)
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    models,
	})
	return
}

func UpdateUser(c *gin.Context) {
	var updatedUser model.User
	err := json.NewDecoder(c.Request.Body).Decode(&updatedUser)
	if err != nil || updatedUser.Id == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	updatedUser.Username = strings.TrimSpace(updatedUser.Username)
	if updatedUser.Username == "" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if updatedUser.Password == "" {
		updatedUser.Password = "$I_LOVE_U" // make Validator happy :)
	}
	if err := common.Validate.Struct(&updatedUser); err != nil {
		common.ApiErrorI18n(c, i18n.MsgUserInputInvalid, map[string]any{"Error": err.Error()})
		return
	}
	originUser, err := model.GetUserById(updatedUser.Id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if updatedUser.Role != common.RoleGuestUser && updatedUser.Role != originUser.Role {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	updatedUser.Role = originUser.Role
	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, originUser.Role) {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionHigherLevel)
		return
	}
	if updatedUser.Password == "$I_LOVE_U" {
		updatedUser.Password = "" // rollback to what it should be
	}
	updatePassword := updatedUser.Password != ""
	authzTouched := false
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := updatedUser.EditWithTx(tx, updatePassword); err != nil {
			return err
		}
		touched, err := updateAdminPermissionsForUserInTx(c, tx, updatedUser.Id, originUser.Role, updatedUser.AdminPermissions)
		authzTouched = touched
		return err
	}); err != nil {
		common.ApiError(c, err)
		return
	}
	if authzTouched {
		if err := authz.ReloadPolicy(); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	if err := model.InvalidateUserCache(updatedUser.Id); err != nil {
		common.SysLog(fmt.Sprintf("failed to invalidate user cache for user %d: %s", updatedUser.Id, err.Error()))
	}
	if updatedUser.Type == 1 || originUser.Type == 1 {
		enterpriseUser, err := model.GetUserById(updatedUser.Id, false)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if enterpriseUser.Type == 1 {
			enterprise, err := model.EnsureEnterpriseForUser(enterpriseUser)
			if err != nil {
				common.ApiError(c, err)
				return
			}
			if enterprise != nil {
				if err := model.DB.Model(&model.User{}).
					Where("type = ? AND topid = ?", 2, enterpriseUser.Id).
					Updates(map[string]interface{}{
						"enterprise_id":   enterprise.Id,
						"enterprise_name": enterprise.Name,
					}).Error; err != nil {
					common.ApiError(c, err)
					return
				}
			}
			_ = model.InvalidateUserCache(enterpriseUser.Id)
		}
	}
	recordManageAuditFor(c, updatedUser.Id, "user.update", map[string]interface{}{
		"username": originUser.Username,
		"id":       updatedUser.Id,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func AdminClearUserBinding(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	bindingType := strings.ToLower(strings.TrimSpace(c.Param("binding_type")))
	if bindingType == "" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, user.Role) {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionSameLevel)
		return
	}

	if err := user.ClearBinding(bindingType); err != nil {
		common.ApiError(c, err)
		return
	}

	recordManageAuditFor(c, user.Id, "user.binding_clear", map[string]interface{}{
		"bindingType": bindingType,
		"username":    user.Username,
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "success",
	})
}

func UpdateSelf(c *gin.Context) {
	var requestData map[string]interface{}
	if err := common.DecodeJson(c.Request.Body, &requestData); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	// 检查是否是用户设置更新请求 (sidebar_modules 或 language)
	if sidebarModules, sidebarExists := requestData["sidebar_modules"]; sidebarExists {
		userId := c.GetInt("id")
		user, err := model.GetUserById(userId, false)
		if err != nil {
			common.ApiError(c, err)
			return
		}

		// 获取当前用户设置
		currentSetting := user.GetSetting()

		// 更新sidebar_modules字段
		if sidebarModulesStr, ok := sidebarModules.(string); ok {
			currentSetting.SidebarModules = sidebarModulesStr
		}

		if err := model.UpdateUserSetting(user.Id, currentSetting); err != nil {
			common.ApiErrorI18n(c, i18n.MsgUpdateFailed)
			return
		}

		common.ApiSuccessI18n(c, i18n.MsgUpdateSuccess, nil)
		return
	}

	// 检查是否是语言偏好更新请求
	if language, langExists := requestData["language"]; langExists {
		userId := c.GetInt("id")
		user, err := model.GetUserById(userId, false)
		if err != nil {
			common.ApiError(c, err)
			return
		}

		// 获取当前用户设置
		currentSetting := user.GetSetting()

		// 更新language字段
		if langStr, ok := language.(string); ok {
			currentSetting.Language = langStr
		}

		if err := model.UpdateUserSetting(user.Id, currentSetting); err != nil {
			common.ApiErrorI18n(c, i18n.MsgUpdateFailed)
			return
		}

		common.ApiSuccessI18n(c, i18n.MsgUpdateSuccess, nil)
		return
	}

	// 原有的用户信息更新逻辑
	var user model.User
	requestDataBytes, err := common.Marshal(requestData)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err = common.Unmarshal(requestDataBytes, &user); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	if user.Password == "" {
		user.Password = "$I_LOVE_U" // make Validator happy :)
	}
	if err := common.Validate.Struct(&user); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidInput)
		return
	}

	cleanUser := model.User{
		Id:          c.GetInt("id"),
		Username:    user.Username,
		Password:    user.Password,
		DisplayName: user.DisplayName,
	}
	if user.Password == "$I_LOVE_U" {
		user.Password = "" // rollback to what it should be
		cleanUser.Password = ""
	}
	updatePassword, err := checkUpdatePassword(user.OriginalPassword, user.Password, cleanUser.Id)
	if err != nil {
		if errors.Is(err, errUserPasswordUnset) {
			common.ApiErrorI18n(c, i18n.MsgUserPasswordUnset)
			return
		}
		if errors.Is(err, errOriginalPasswordFail) {
			common.ApiErrorI18n(c, i18n.MsgUserOriginalPasswordError)
			return
		}
		common.ApiError(c, err)
		return
	}
	if err := cleanUser.Update(updatePassword); err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func checkUpdatePassword(originalPassword string, newPassword string, userId int) (updatePassword bool, err error) {
	if newPassword == "" {
		return
	}
	var currentUser *model.User
	currentUser, err = model.GetUserById(userId, true)
	if err != nil {
		return
	}

	// 密码不为空,需要验证原密码
	if currentUser.Password == "" {
		err = errUserPasswordUnset
		return
	}
	if !common.ValidatePasswordAndHash(originalPassword, currentUser.Password) {
		err = errOriginalPasswordFail
		return
	}
	updatePassword = true
	return
}

func DeleteUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	originUser, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	myRole := c.GetInt("role")
	if myRole <= originUser.Role {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionHigherLevel)
		return
	}
	err = model.HardDeleteUserById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, originUser.Id, "user.delete", map[string]interface{}{
		"username": originUser.Username,
		"id":       originUser.Id,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteSelf(c *gin.Context) {
	id := c.GetInt("id")
	user, _ := model.GetUserById(id, false)

	if user.Role == common.RoleRootUser {
		common.ApiErrorI18n(c, i18n.MsgUserCannotDeleteRootUser)
		return
	}

	err := model.DeleteUserById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func CreateUser(c *gin.Context) {
	var user model.User
	err := json.NewDecoder(c.Request.Body).Decode(&user)
	user.Username = strings.TrimSpace(user.Username)
	if err != nil || user.Username == "" || user.Password == "" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err := common.Validate.Struct(&user); err != nil {
		common.ApiErrorI18n(c, i18n.MsgUserInputInvalid, map[string]any{"Error": err.Error()})
		return
	}
	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}
	myRole := c.GetInt("role")
	if user.Role >= myRole {
		common.ApiErrorI18n(c, i18n.MsgUserCannotCreateHigherLevel)
		return
	}
	// Even for admin users, we cannot fully trust them!
	cleanUser := model.User{
		Username:    user.Username,
		Password:    user.Password,
		DisplayName: user.DisplayName,
		Role:        user.Role, // 保持管理员设置的角色
	}
	authzTouched := false
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := cleanUser.InsertWithTx(tx, 0); err != nil {
			return err
		}
		touched, err := updateAdminPermissionsForUserInTx(c, tx, cleanUser.Id, cleanUser.Role, user.AdminPermissions)
		authzTouched = touched
		return err
	}); err != nil {
		common.ApiError(c, err)
		return
	}
	if authzTouched {
		if err := authz.ReloadPolicy(); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	cleanUser.FinishInsert(0)

	recordManageAuditFor(c, cleanUser.Id, "user.create", map[string]interface{}{
		"username": cleanUser.Username,
		"role":     cleanUser.Role,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func updateAdminPermissionsForUserInTx(c *gin.Context, tx *gorm.DB, userID int, userRole int, permissions map[string]map[string]bool) (bool, error) {
	if permissions == nil {
		if userRole < common.RoleAdminUser && c.GetInt("role") == common.RoleRootUser {
			return true, authz.ClearUserAuthorizationInTx(tx, userID)
		}
		return false, nil
	}
	if c.GetInt("role") != common.RoleRootUser {
		return false, fmt.Errorf("only root can update admin permissions")
	}
	if userRole < common.RoleAdminUser {
		return true, authz.ClearUserAuthorizationInTx(tx, userID)
	}
	return true, authz.SetUserPermissionsInTx(tx, userID, permissions)
}

type ManageRequest struct {
	Id     int    `json:"id"`
	Action string `json:"action"`
	Value  int    `json:"value"`
	Mode   string `json:"mode"`
}

// ManageUser Only admin user can do this
func ManageUser(c *gin.Context) {
	var req ManageRequest
	err := json.NewDecoder(c.Request.Body).Decode(&req)

	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	user := model.User{
		Id: req.Id,
	}
	// Fill attributes
	model.DB.Unscoped().Where(&user).First(&user)
	if user.Id == 0 {
		common.ApiErrorI18n(c, i18n.MsgUserNotExists)
		return
	}
	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, user.Role) {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionHigherLevel)
		return
	}
	switch req.Action {
	case "disable":
		user.Status = common.UserStatusDisabled
		if user.Role == common.RoleRootUser {
			common.ApiErrorI18n(c, i18n.MsgUserCannotDisableRootUser)
			return
		}
		if err := model.DisableEnterpriseForOwner(user.Id); err != nil {
			common.ApiError(c, err)
			return
		}
	case "enable":
		user.Status = common.UserStatusEnabled
		if user.Type == 1 {
			if _, err := model.EnsureEnterpriseForUser(&user); err != nil {
				common.ApiError(c, err)
				return
			}
		}
	case "delete":
		if user.Role == common.RoleRootUser {
			common.ApiErrorI18n(c, i18n.MsgUserCannotDeleteRootUser)
			return
		}
		if err := user.Delete(); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		// 删除用户后，强制清理 Redis 中所有该用户令牌的缓存，
		// 避免已缓存的令牌在 TTL 过期前仍能通过 TokenAuth 校验。
		if err := model.InvalidateUserTokensCache(user.Id); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate tokens cache for user %d: %s", user.Id, err.Error()))
		}
	case "promote":
		if myRole != common.RoleRootUser {
			common.ApiErrorI18n(c, i18n.MsgUserAdminCannotPromote)
			return
		}
		if user.Role >= common.RoleAdminUser {
			common.ApiErrorI18n(c, i18n.MsgUserAlreadyAdmin)
			return
		}
		user.Role = common.RoleAdminUser
	case "demote":
		if user.Role == common.RoleRootUser {
			common.ApiErrorI18n(c, i18n.MsgUserCannotDemoteRootUser)
			return
		}
		if user.Role == common.RoleCommonUser {
			common.ApiErrorI18n(c, i18n.MsgUserAlreadyCommon)
			return
		}
		user.Role = common.RoleCommonUser
	case "promote_enterprise":
		if myRole != common.RoleRootUser {
			common.ApiErrorI18n(c, i18n.MsgUserAdminCannotPromote)
			return
		}
		if user.Type >= 2 {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "子账号不能提升为企业管理员"})
			return
		}
		if user.Type == 1 {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "该用户已是企业管理员"})
			return
		}
		user.Type = 1
		if _, err := model.EnsureEnterpriseForUser(&user); err != nil {
			common.ApiError(c, err)
			return
		}
		if err := model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("type", 1).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		_ = model.InvalidateUserCache(user.Id)
		c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
		return
	case "demote_enterprise":
		if myRole != common.RoleRootUser {
			common.ApiErrorI18n(c, i18n.MsgUserAdminCannotPromote)
			return
		}
		if user.Type != 1 {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "该用户不是企业管理员"})
			return
		}
		if err := model.DB.Model(&model.User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
			"type":          0,
			"enterprise_id": 0,
		}).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		if err := model.DisableEnterpriseForOwner(user.Id); err != nil {
			common.ApiError(c, err)
			return
		}
		_ = model.InvalidateUserCache(user.Id)
		c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
		return
	case "add_quota":
		switch req.Mode {
		case "add":
			if req.Value <= 0 {
				common.ApiErrorI18n(c, i18n.MsgUserQuotaChangeZero)
				return
			}
			if err := model.IncreaseUserQuota(user.Id, req.Value, true); err != nil {
				common.ApiError(c, err)
				return
			}
			recordManageAuditFor(c, user.Id, "user.quota_add", map[string]interface{}{
				"quota": logger.LogQuota(req.Value),
			})
		case "subtract":
			if req.Value <= 0 {
				common.ApiErrorI18n(c, i18n.MsgUserQuotaChangeZero)
				return
			}
			if err := model.DecreaseUserQuota(user.Id, req.Value, true); err != nil {
				common.ApiError(c, err)
				return
			}
			recordManageAuditFor(c, user.Id, "user.quota_subtract", map[string]interface{}{
				"quota": logger.LogQuota(req.Value),
			})
		case "override":
			oldQuota := user.Quota
			if err := model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", req.Value).Error; err != nil {
				common.ApiError(c, err)
				return
			}
			recordManageAuditFor(c, user.Id, "user.quota_override", map[string]interface{}{
				"from": logger.LogQuota(oldQuota),
				"to":   logger.LogQuota(req.Value),
			})
		default:
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
		})
		return
	}

	authzTouched := false
	if req.Action == "demote" {
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			if err := user.UpdateWithTx(tx, false); err != nil {
				return err
			}
			authzTouched = true
			return authz.ClearUserAuthorizationInTx(tx, user.Id)
		}); err != nil {
			common.ApiError(c, err)
			return
		}
		if authzTouched {
			if err := authz.ReloadPolicy(); err != nil {
				common.ApiError(c, err)
				return
			}
		}
	} else {
		if err := user.Update(false); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	// 禁用 / 角色调整后，强制失效用户缓存与其全部令牌缓存，
	// 避免在 Redis TTL 过期前仍使用旧状态（尤其是禁用后仍可发起请求的问题）。
	// InvalidateUserCache 会让下一次 GetUserCache 从数据库重新加载，
	// InvalidateUserTokensCache 则确保令牌侧的缓存也同步刷新。
	if req.Action == "disable" || req.Action == "promote" || req.Action == "demote" {
		if err := model.InvalidateUserCache(user.Id); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate user cache for user %d: %s", user.Id, err.Error()))
		}
		if err := model.InvalidateUserTokensCache(user.Id); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate tokens cache for user %d: %s", user.Id, err.Error()))
		}
	}
	recordManageAuditFor(c, user.Id, "user.manage", map[string]interface{}{
		"action":   req.Action,
		"username": user.Username,
		"id":       user.Id,
	})
	clearUser := model.User{
		Role:   user.Role,
		Status: user.Status,
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    clearUser,
	})
	return
}

type emailBindRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func EmailBind(c *gin.Context) {
	var req emailBindRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiError(c, errors.New("invalid request body"))
		return
	}
	email := req.Email
	email = model.NormalizeEmail(email)
	code := req.Code
	if !common.VerifyCodeWithKey(email, code, common.EmailVerificationPurpose) {
		common.ApiErrorI18n(c, i18n.MsgUserVerificationCodeError)
		return
	}
	session := sessions.Default(c)
	id := session.Get("id")
	user := model.User{
		Id: id.(int),
	}
	err := user.FillUserById()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.BindEmailToUser(&user, email); err != nil {
		if errors.Is(err, model.ErrEmailAlreadyTaken) {
			common.ApiErrorI18n(c, i18n.MsgUserEmailAlreadyTaken)
			return
		}
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

type topUpRequest struct {
	Key string `json:"key"`
}

var topUpLocks sync.Map
var topUpCreateLock sync.Mutex

type topUpTryLock struct {
	ch chan struct{}
}

func newTopUpTryLock() *topUpTryLock {
	return &topUpTryLock{ch: make(chan struct{}, 1)}
}

func (l *topUpTryLock) TryLock() bool {
	select {
	case l.ch <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *topUpTryLock) Unlock() {
	select {
	case <-l.ch:
	default:
	}
}

func getTopUpLock(userID int) *topUpTryLock {
	if v, ok := topUpLocks.Load(userID); ok {
		return v.(*topUpTryLock)
	}
	topUpCreateLock.Lock()
	defer topUpCreateLock.Unlock()
	if v, ok := topUpLocks.Load(userID); ok {
		return v.(*topUpTryLock)
	}
	l := newTopUpTryLock()
	topUpLocks.Store(userID, l)
	return l
}

func TopUp(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorI18n(c, i18n.MsgPaymentComplianceRequired)
		return
	}

	id := c.GetInt("id")
	lock := getTopUpLock(id)
	if !lock.TryLock() {
		common.ApiErrorI18n(c, i18n.MsgUserTopUpProcessing)
		return
	}
	defer lock.Unlock()
	req := topUpRequest{}
	err := c.ShouldBindJSON(&req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	quota, err := model.Redeem(req.Key, id)
	if err != nil {
		// 不向用户暴露兑换失败的细分原因，避免攻击者根据错误类型判断兑换码状态。
		common.ApiErrorI18n(c, i18n.MsgRedeemFailed)
		logger.LogError(c, fmt.Sprintf("failed to redeem key %s for user %d: %s", req.Key, id, err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    quota,
	})
}

type UpdateUserSettingRequest struct {
	QuotaWarningType                 string  `json:"notify_type"`
	QuotaWarningThreshold            float64 `json:"quota_warning_threshold"`
	WebhookUrl                       string  `json:"webhook_url,omitempty"`
	WebhookSecret                    string  `json:"webhook_secret,omitempty"`
	NotificationEmail                string  `json:"notification_email,omitempty"`
	BarkUrl                          string  `json:"bark_url,omitempty"`
	GotifyUrl                        string  `json:"gotify_url,omitempty"`
	GotifyToken                      string  `json:"gotify_token,omitempty"`
	GotifyPriority                   int     `json:"gotify_priority,omitempty"`
	UpstreamModelUpdateNotifyEnabled *bool   `json:"upstream_model_update_notify_enabled,omitempty"`
	AcceptUnsetModelRatioModel       bool    `json:"accept_unset_model_ratio_model"`
	RecordIpLog                      bool    `json:"record_ip_log"`
}

func UpdateUserSetting(c *gin.Context) {
	var req UpdateUserSettingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	// 验证预警类型
	if req.QuotaWarningType != dto.NotifyTypeEmail && req.QuotaWarningType != dto.NotifyTypeWebhook && req.QuotaWarningType != dto.NotifyTypeBark && req.QuotaWarningType != dto.NotifyTypeGotify {
		common.ApiErrorI18n(c, i18n.MsgSettingInvalidType)
		return
	}

	// 验证预警阈值
	if req.QuotaWarningThreshold <= 0 {
		common.ApiErrorI18n(c, i18n.MsgQuotaThresholdGtZero)
		return
	}

	// 如果是webhook类型,验证webhook地址
	if req.QuotaWarningType == dto.NotifyTypeWebhook {
		if req.WebhookUrl == "" {
			common.ApiErrorI18n(c, i18n.MsgSettingWebhookEmpty)
			return
		}
		// 验证URL格式
		if _, err := url.ParseRequestURI(req.WebhookUrl); err != nil {
			common.ApiErrorI18n(c, i18n.MsgSettingWebhookInvalid)
			return
		}
	}

	// 如果是邮件类型，验证邮箱地址
	if req.QuotaWarningType == dto.NotifyTypeEmail && req.NotificationEmail != "" {
		// 验证邮箱格式
		if !strings.Contains(req.NotificationEmail, "@") {
			common.ApiErrorI18n(c, i18n.MsgSettingEmailInvalid)
			return
		}
	}

	// 如果是Bark类型，验证Bark URL
	if req.QuotaWarningType == dto.NotifyTypeBark {
		if req.BarkUrl == "" {
			common.ApiErrorI18n(c, i18n.MsgSettingBarkUrlEmpty)
			return
		}
		// 验证URL格式
		if _, err := url.ParseRequestURI(req.BarkUrl); err != nil {
			common.ApiErrorI18n(c, i18n.MsgSettingBarkUrlInvalid)
			return
		}
		// 检查是否是HTTP或HTTPS
		if !strings.HasPrefix(req.BarkUrl, "https://") && !strings.HasPrefix(req.BarkUrl, "http://") {
			common.ApiErrorI18n(c, i18n.MsgSettingUrlMustHttp)
			return
		}
	}

	// 如果是Gotify类型，验证Gotify URL和Token
	if req.QuotaWarningType == dto.NotifyTypeGotify {
		if req.GotifyUrl == "" {
			common.ApiErrorI18n(c, i18n.MsgSettingGotifyUrlEmpty)
			return
		}
		if req.GotifyToken == "" {
			common.ApiErrorI18n(c, i18n.MsgSettingGotifyTokenEmpty)
			return
		}
		// 验证URL格式
		if _, err := url.ParseRequestURI(req.GotifyUrl); err != nil {
			common.ApiErrorI18n(c, i18n.MsgSettingGotifyUrlInvalid)
			return
		}
		// 检查是否是HTTP或HTTPS
		if !strings.HasPrefix(req.GotifyUrl, "https://") && !strings.HasPrefix(req.GotifyUrl, "http://") {
			common.ApiErrorI18n(c, i18n.MsgSettingUrlMustHttp)
			return
		}
	}

	userId := c.GetInt("id")
	user, err := model.GetUserById(userId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	existingSettings := user.GetSetting()
	upstreamModelUpdateNotifyEnabled := existingSettings.UpstreamModelUpdateNotifyEnabled
	if user.Role >= common.RoleAdminUser && req.UpstreamModelUpdateNotifyEnabled != nil {
		upstreamModelUpdateNotifyEnabled = *req.UpstreamModelUpdateNotifyEnabled
	}

	// 构建设置
	settings := dto.UserSetting{
		NotifyType:                       req.QuotaWarningType,
		QuotaWarningThreshold:            req.QuotaWarningThreshold,
		UpstreamModelUpdateNotifyEnabled: upstreamModelUpdateNotifyEnabled,
		AcceptUnsetRatioModel:            req.AcceptUnsetModelRatioModel,
		RecordIpLog:                      req.RecordIpLog,
	}

	// 如果是webhook类型,添加webhook相关设置
	if req.QuotaWarningType == dto.NotifyTypeWebhook {
		settings.WebhookUrl = req.WebhookUrl
		if req.WebhookSecret != "" {
			settings.WebhookSecret = req.WebhookSecret
		}
	}

	// 如果提供了通知邮箱，添加到设置中
	if req.QuotaWarningType == dto.NotifyTypeEmail && req.NotificationEmail != "" {
		settings.NotificationEmail = req.NotificationEmail
	}

	// 如果是Bark类型，添加Bark URL到设置中
	if req.QuotaWarningType == dto.NotifyTypeBark {
		settings.BarkUrl = req.BarkUrl
	}

	// 如果是Gotify类型，添加Gotify配置到设置中
	if req.QuotaWarningType == dto.NotifyTypeGotify {
		settings.GotifyUrl = req.GotifyUrl
		settings.GotifyToken = req.GotifyToken
		// Gotify优先级范围0-10，超出范围则使用默认值5
		if req.GotifyPriority < 0 || req.GotifyPriority > 10 {
			settings.GotifyPriority = 5
		} else {
			settings.GotifyPriority = req.GotifyPriority
		}
	}

	// 更新用户设置
	if err := model.UpdateUserSetting(user.Id, settings); err != nil {
		common.ApiErrorI18n(c, i18n.MsgUpdateFailed)
		return
	}

	common.ApiSuccessI18n(c, i18n.MsgSettingSaved, nil)
}

func GetSonUsers(c *gin.Context) {
	topUserId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	users, total, err := model.GetSonUsers(pageInfo, topUserId)
	if err != nil {
		handleEnterpriseSonInternalError(c, "list sub-accounts")
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(model.ToSonUserResponses(users))
	common.ApiSuccess(c, pageInfo)
}

type SonTokenResponse struct {
	Id                 int    `json:"id"`
	Name               string `json:"name"`
	Key                string `json:"key,omitempty"`
	Status             int    `json:"status"`
	Group              string `json:"group"`
	CreatedTime        int64  `json:"created_time"`
	AccessedTime       int64  `json:"accessed_time"`
	ExpiredTime        int64  `json:"expired_time"`
	RemainQuota        int    `json:"remain_quota"`
	UsedQuota          int    `json:"used_quota"`
	UnlimitedQuota     bool   `json:"unlimited_quota"`
	ModelLimitsEnabled bool   `json:"model_limits_enabled"`
	ModelLimits        string `json:"model_limits"`
	CrossGroupRetry    bool   `json:"cross_group_retry"`
}

var errEnterpriseSonForbidden = errors.New("sub-account is not owned by this enterprise")

func getEnterpriseSonUser(adminId int, sonId int) (*model.User, error) {
	if adminId <= 0 || sonId <= 0 {
		return nil, errEnterpriseSonForbidden
	}
	admin, err := model.GetUserById(adminId, false)
	if err != nil {
		return nil, err
	}
	enabled, err := model.IsEnterpriseAdminEnabled(admin)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, errEnterpriseSonForbidden
	}

	var son model.User
	err = model.DB.Where("id = ? AND type = ? AND enterprise_id = ?", sonId, 2, admin.EnterpriseId).First(&son).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errEnterpriseSonForbidden
	}
	if err != nil {
		return nil, err
	}
	return &son, nil
}

func getEnterpriseSonToken(adminId int, sonId int, tokenId int) (*model.User, *model.Token, error) {
	son, err := getEnterpriseSonUser(adminId, sonId)
	if err != nil {
		return nil, nil, err
	}
	if tokenId <= 0 {
		return nil, nil, errEnterpriseSonForbidden
	}
	token, err := model.GetTokenByIds(tokenId, son.Id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, errEnterpriseSonForbidden
	}
	if err != nil {
		return nil, nil, err
	}
	return son, token, nil
}

func handleEnterpriseSonError(c *gin.Context, err error) {
	if errors.Is(err, errEnterpriseSonForbidden) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "sub-account is not owned by this enterprise"})
		return
	}
	handleEnterpriseSonInternalError(c, "authorize sub-account ownership")
}

func handleEnterpriseSonInternalError(c *gin.Context, operation string) {
	common.SysLog("enterprise sub-account operation failed: " + operation)
	common.ApiErrorI18n(c, i18n.MsgDatabaseError)
}

func buildSonTokenResponse(token *model.Token, revealKey bool) SonTokenResponse {
	response := SonTokenResponse{
		Id:                 token.Id,
		Name:               token.Name,
		Status:             token.Status,
		Group:              token.Group,
		CreatedTime:        token.CreatedTime,
		AccessedTime:       token.AccessedTime,
		ExpiredTime:        token.ExpiredTime,
		RemainQuota:        token.RemainQuota,
		UsedQuota:          token.UsedQuota,
		UnlimitedQuota:     token.UnlimitedQuota,
		ModelLimitsEnabled: token.ModelLimitsEnabled,
		ModelLimits:        token.ModelLimits,
		CrossGroupRetry:    token.CrossGroupRetry,
	}
	if revealKey {
		response.Key = token.GetFullKey()
	}
	return response
}

func GetSonTokens(c *gin.Context) {
	topUserId := c.GetInt("id")
	sonId, err := strconv.Atoi(c.Param("id"))
	if err != nil || sonId <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	sonUser, err := getEnterpriseSonUser(topUserId, sonId)
	if err != nil {
		handleEnterpriseSonError(c, err)
		return
	}
	tokens, err := model.GetAllUserTokens(sonUser.Id, 0, 1000)
	if err != nil {
		handleEnterpriseSonInternalError(c, "list sub-account tokens")
		return
	}
	items := make([]SonTokenResponse, 0, len(tokens))
	for _, token := range tokens {
		items = append(items, buildSonTokenResponse(token, false))
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

type tokenModelLimitsInput struct {
	Set   bool
	Value string
}

func (input *tokenModelLimitsInput) UnmarshalJSON(data []byte) error {
	input.Set = true
	if string(data) == "null" {
		input.Value = ""
		return nil
	}

	var raw string
	if err := common.Unmarshal(data, &raw); err == nil {
		value, err := normalizeTokenModelLimits(strings.Split(raw, ","))
		if err != nil {
			return err
		}
		input.Value = value
		return nil
	}

	var models []string
	if err := common.Unmarshal(data, &models); err != nil {
		return errors.New("model limits must be a comma-separated string or a string array")
	}
	value, err := normalizeTokenModelLimits(models)
	if err != nil {
		return err
	}
	input.Value = value
	return nil
}

func normalizeTokenModelLimits(models []string) (string, error) {
	normalized := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, item := range models {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if len(item) > 200 || strings.ContainsAny(item, "\r\n,") {
			return "", errors.New("invalid model limit")
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	if len(normalized) > 100 {
		return "", errors.New("too many model limits")
	}
	value := strings.Join(normalized, ",")
	if len(value) > 4096 {
		return "", errors.New("model limits are too long")
	}
	return value, nil
}

func defaultSubaccountTokenQuota() int {
	if constant.SubaccountDefaultTokenQuota > 0 {
		return constant.SubaccountDefaultTokenQuota
	}
	return 500000
}

const maxEnterpriseTokenQuota = 2147483647

func resolveTokenModelLimits(currentEnabled bool, currentValue string, limits tokenModelLimitsInput, enabled *bool) (bool, string, error) {
	if enabled == nil && !limits.Set {
		return currentEnabled, currentValue, nil
	}
	if enabled == nil {
		return limits.Value != "", limits.Value, nil
	}
	if !*enabled && limits.Set && limits.Value != "" {
		return false, "", errors.New("model limits cannot be provided when model limit enforcement is disabled")
	}
	if !*enabled {
		return false, "", nil
	}
	if limits.Set {
		return true, limits.Value, nil
	}
	return true, currentValue, nil
}

func buildEnterpriseSonToken(son *model.User, name string, quota *int, unlimited *bool, expiredTime *int64, limits tokenModelLimitsInput, limitsEnabled *bool) (*model.Token, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = son.Username + "的初始令牌"
	}
	if len(name) > 50 {
		return nil, errors.New("token name is too long")
	}

	remaining := defaultSubaccountTokenQuota()
	if quota != nil {
		remaining = *quota
	}
	isUnlimited := false
	if unlimited != nil {
		isUnlimited = *unlimited
	}
	if remaining < 0 || remaining > maxEnterpriseTokenQuota {
		return nil, errors.New("token quota is outside the valid range")
	}
	if !isUnlimited && remaining == 0 {
		return nil, errors.New("a finite token quota must be greater than zero")
	}

	expiresAt := int64(-1)
	if expiredTime != nil {
		expiresAt = *expiredTime
	}
	if expiresAt != -1 && expiresAt <= common.GetTimestamp() {
		return nil, errors.New("token expiry must be in the future or -1")
	}
	modelLimitsEnabled, modelLimits, err := resolveTokenModelLimits(false, "", limits, limitsEnabled)
	if err != nil {
		return nil, err
	}

	key, err := common.GenerateKey()
	if err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	token := &model.Token{
		UserId:             son.Id,
		Name:               name,
		Key:                key,
		Status:             common.TokenStatusEnabled,
		CreatedTime:        now,
		AccessedTime:       now,
		ExpiredTime:        expiresAt,
		RemainQuota:        remaining,
		UnlimitedQuota:     isUnlimited,
		ModelLimitsEnabled: modelLimitsEnabled,
		ModelLimits:        modelLimits,
		Group:              son.Group,
	}
	if setting.DefaultUseAutoGroup {
		token.Group = "auto"
	}
	return token, nil
}

type CreateSonRequest struct {
	Username                string                `json:"username"`
	Password                string                `json:"password"`
	DisplayName             string                `json:"display_name"`
	Phone                   string                `json:"phone"`
	TokenName               string                `json:"token_name"`
	TokenQuota              *int                  `json:"token_quota"`
	TokenUnlimited          *bool                 `json:"token_unlimited"`
	TokenModelLimits        tokenModelLimitsInput `json:"token_model_limits"`
	TokenModelLimitsEnabled *bool                 `json:"token_model_limits_enabled"`
}

func validateCreateSonInitialTokenRequest(req CreateSonRequest) error {
	if req.TokenUnlimited != nil && *req.TokenUnlimited {
		return errors.New("unlimited initial tokens are not allowed")
	}
	if len(strings.TrimSpace(req.TokenName)) > 50 {
		return errors.New("token name is too long")
	}
	if req.TokenQuota != nil && (*req.TokenQuota <= 0 || *req.TokenQuota > maxEnterpriseTokenQuota) {
		return errors.New("token quota is outside the valid range")
	}
	return nil
}

func CreateSonUser(c *gin.Context) {
	common.SetNoStoreHeaders(c)
	topUserId := c.GetInt("id")
	topUser, err := model.GetUserById(topUserId, false)
	if err != nil {
		handleEnterpriseSonInternalError(c, "load enterprise owner")
		return
	}
	if topUser.Type >= 2 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "子账号不能创建子账号"})
		return
	}

	var req CreateSonRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.Phone = strings.TrimSpace(req.Phone)
	if err := validateCreateSonInitialTokenRequest(req); err != nil {
		common.ApiError(c, err)
		return
	}
	if req.Username == "" || req.Password == "" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if len(req.Password) < 8 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "密码长度不能少于8位"})
		return
	}

	exist, err := model.CheckUserExistOrDeleted(req.Username, "")
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	if exist {
		common.ApiErrorI18n(c, i18n.MsgUserExists)
		return
	}

	if req.Phone != "" {
		if len(req.Phone) > 20 {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "手机号长度不能超过20位"})
			return
		}
		if model.IsPhoneAlreadyTaken(req.Phone) {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "手机号已被占用"})
			return
		}
	}

	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Username
	}
	if err := common.Validate.Struct(&model.User{
		Username:    req.Username,
		Password:    req.Password,
		DisplayName: displayName,
		Phone:       req.Phone,
	}); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidInput)
		return
	}
	enterprise, err := model.EnsureEnterpriseForUser(topUser)
	if err != nil {
		handleEnterpriseSonInternalError(c, "load enterprise")
		return
	}
	enterpriseId := topUser.EnterpriseId
	enterpriseName := topUser.EnterpriseName
	if enterprise != nil {
		enterpriseId = enterprise.Id
		enterpriseName = enterprise.Name
	}

	cleanUser := model.User{
		Username:       req.Username,
		Password:       req.Password,
		DisplayName:    displayName,
		Role:           common.RoleCommonUser,
		Type:           2,
		Topid:          topUserId,
		EnterpriseId:   enterpriseId,
		Phone:          req.Phone,
		Group:          topUser.Group,
		EnterpriseName: enterpriseName,
	}
	var createdToken *model.Token
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if err := cleanUser.InsertWithTx(tx, 0); err != nil {
			return err
		}
		// InsertWithTx is shared with normal registration. Sub-accounts never
		// own the billing wallet, so keep their user quota at zero.
		cleanUser.Quota = 0
		if err := tx.Model(&cleanUser).Update("quota", 0).Error; err != nil {
			return err
		}
		createdToken, err = buildEnterpriseSonToken(
			&cleanUser,
			req.TokenName,
			req.TokenQuota,
			req.TokenUnlimited,
			nil,
			req.TokenModelLimits,
			req.TokenModelLimitsEnabled,
		)
		if err != nil {
			return err
		}
		return tx.Create(createdToken).Error
	})
	if err != nil {
		handleEnterpriseSonInternalError(c, "create sub-account and initial token")
		return
	}
	cleanUser.FinishInsert(0)

	model.RecordLog(topUserId, model.LogTypeManage, fmt.Sprintf("创建子账号 %s", req.Username))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"user_id": cleanUser.Id,
			"token":   buildSonTokenResponse(createdToken, true),
		},
	})
}

type CreateSonTokenRequest struct {
	Name               string                `json:"name"`
	RemainQuota        *int                  `json:"remain_quota"`
	UnlimitedQuota     *bool                 `json:"unlimited_quota"`
	ExpiredTime        *int64                `json:"expired_time"`
	ModelLimits        tokenModelLimitsInput `json:"model_limits"`
	ModelLimitsEnabled *bool                 `json:"model_limits_enabled"`
}

func CreateSonToken(c *gin.Context) {
	common.SetNoStoreHeaders(c)
	adminId := c.GetInt("id")
	sonId, err := strconv.Atoi(c.Param("id"))
	if err != nil || sonId <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	son, err := getEnterpriseSonUser(adminId, sonId)
	if err != nil {
		handleEnterpriseSonError(c, err)
		return
	}

	var req CreateSonTokenRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	count, err := model.CountUserTokens(son.Id)
	if err != nil {
		handleEnterpriseSonInternalError(c, "count sub-account tokens")
		return
	}
	if int(count) >= operation_setting.GetMaxUserTokens() {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "sub-account token limit reached"})
		return
	}

	token, err := buildEnterpriseSonToken(son, req.Name, req.RemainQuota, req.UnlimitedQuota, req.ExpiredTime, req.ModelLimits, req.ModelLimitsEnabled)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := token.Insert(); err != nil {
		handleEnterpriseSonInternalError(c, "create sub-account token")
		return
	}
	model.RecordLog(adminId, model.LogTypeManage, fmt.Sprintf("created sub-account token user_id=%d token_id=%d", son.Id, token.Id))
	common.ApiSuccess(c, buildSonTokenResponse(token, true))
}

type UpdateSonTokenRequest struct {
	Status             *int                  `json:"status"`
	RemainQuota        *int                  `json:"remain_quota"`
	UnlimitedQuota     *bool                 `json:"unlimited_quota"`
	ExpiredTime        *int64                `json:"expired_time"`
	ModelLimits        tokenModelLimitsInput `json:"model_limits"`
	ModelLimitsEnabled *bool                 `json:"model_limits_enabled"`
}

func UpdateSonToken(c *gin.Context) {
	adminId := c.GetInt("id")
	sonId, sonErr := strconv.Atoi(c.Param("id"))
	tokenId, tokenErr := strconv.Atoi(c.Param("token_id"))
	if sonErr != nil || tokenErr != nil || sonId <= 0 || tokenId <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	son, token, err := getEnterpriseSonToken(adminId, sonId, tokenId)
	if err != nil {
		handleEnterpriseSonError(c, err)
		return
	}

	var req UpdateSonTokenRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if req.Status == nil && req.RemainQuota == nil && req.UnlimitedQuota == nil && req.ExpiredTime == nil && !req.ModelLimits.Set && req.ModelLimitsEnabled == nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	nextStatus := token.Status
	nextRemainQuota := token.RemainQuota
	nextUnlimitedQuota := token.UnlimitedQuota
	nextExpiredTime := token.ExpiredTime
	updates := make(map[string]interface{})
	if req.Status != nil {
		if *req.Status != common.TokenStatusEnabled && *req.Status != common.TokenStatusDisabled {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		nextStatus = *req.Status
		updates["status"] = *req.Status
	}
	if req.RemainQuota != nil {
		if *req.RemainQuota < 0 || *req.RemainQuota > maxEnterpriseTokenQuota {
			common.ApiError(c, errors.New("token quota is outside the valid range"))
			return
		}
		nextRemainQuota = *req.RemainQuota
		updates["remain_quota"] = *req.RemainQuota
	}
	if req.UnlimitedQuota != nil {
		nextUnlimitedQuota = *req.UnlimitedQuota
		updates["unlimited_quota"] = *req.UnlimitedQuota
	}
	if req.ExpiredTime != nil {
		if *req.ExpiredTime != -1 && *req.ExpiredTime <= common.GetTimestamp() {
			common.ApiError(c, errors.New("token expiry must be in the future or -1"))
			return
		}
		nextExpiredTime = *req.ExpiredTime
		updates["expired_time"] = *req.ExpiredTime
	}
	if req.ModelLimits.Set || req.ModelLimitsEnabled != nil {
		modelLimitsEnabled, modelLimits, limitErr := resolveTokenModelLimits(
			token.ModelLimitsEnabled,
			token.ModelLimits,
			req.ModelLimits,
			req.ModelLimitsEnabled,
		)
		if limitErr != nil {
			common.ApiError(c, limitErr)
			return
		}
		updates["model_limits"] = modelLimits
		updates["model_limits_enabled"] = modelLimitsEnabled
	}

	if nextStatus == common.TokenStatusEnabled {
		if nextExpiredTime != -1 && nextExpiredTime <= common.GetTimestamp() {
			common.ApiError(c, errors.New("an expired token cannot be enabled"))
			return
		}
		if !nextUnlimitedQuota && nextRemainQuota <= 0 {
			if req.Status != nil {
				common.ApiError(c, errors.New("an exhausted token cannot be enabled"))
				return
			}
			nextStatus = common.TokenStatusExhausted
			updates["status"] = nextStatus
		}
	}

	query := model.DB.Model(&model.Token{}).Where("id = ? AND user_id = ?", token.Id, son.Id)
	if req.Status != nil && nextStatus == common.TokenStatusEnabled {
		if !nextUnlimitedQuota && req.RemainQuota == nil {
			query = query.Where("unlimited_quota = ? OR remain_quota > 0", true)
		}
		if req.ExpiredTime == nil {
			query = query.Where("expired_time = -1 OR expired_time > ?", common.GetTimestamp())
		}
	}
	result := query.Updates(updates)
	if result.Error != nil {
		handleEnterpriseSonInternalError(c, "update sub-account token")
		return
	}
	if result.RowsAffected == 0 {
		current, currentErr := model.GetTokenByIds(token.Id, son.Id)
		if currentErr != nil || !sonTokenUpdateAlreadyApplied(current, req) {
			common.ApiError(c, errors.New("token changed concurrently or cannot be enabled"))
			return
		}
		token = current
	}
	if err := model.InvalidateUserTokensCache(son.Id); err != nil {
		handleEnterpriseSonInternalError(c, "invalidate sub-account token cache")
		return
	}
	if result.RowsAffected > 0 {
		token, err = model.GetTokenByIds(token.Id, son.Id)
		if err != nil {
			handleEnterpriseSonInternalError(c, "reload sub-account token")
			return
		}
	}
	model.RecordLog(adminId, model.LogTypeManage, fmt.Sprintf("updated sub-account token user_id=%d token_id=%d", son.Id, token.Id))
	common.ApiSuccess(c, buildSonTokenResponse(token, false))
}

func sonTokenUpdateAlreadyApplied(token *model.Token, req UpdateSonTokenRequest) bool {
	if token == nil {
		return false
	}
	if req.Status != nil && token.Status != *req.Status {
		return false
	}
	if req.RemainQuota != nil && token.RemainQuota != *req.RemainQuota {
		return false
	}
	if req.UnlimitedQuota != nil && token.UnlimitedQuota != *req.UnlimitedQuota {
		return false
	}
	if req.ExpiredTime != nil && token.ExpiredTime != *req.ExpiredTime {
		return false
	}
	if req.ModelLimits.Set || req.ModelLimitsEnabled != nil {
		modelLimitsEnabled, modelLimits, err := resolveTokenModelLimits(
			token.ModelLimitsEnabled,
			token.ModelLimits,
			req.ModelLimits,
			req.ModelLimitsEnabled,
		)
		if err != nil || token.ModelLimits != modelLimits || token.ModelLimitsEnabled != modelLimitsEnabled {
			return false
		}
	}
	return true
}

func DeleteSonToken(c *gin.Context) {
	adminId := c.GetInt("id")
	sonId, sonErr := strconv.Atoi(c.Param("id"))
	tokenId, tokenErr := strconv.Atoi(c.Param("token_id"))
	if sonErr != nil || tokenErr != nil || sonId <= 0 || tokenId <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	son, token, err := getEnterpriseSonToken(adminId, sonId, tokenId)
	if err != nil {
		handleEnterpriseSonError(c, err)
		return
	}
	if err := token.Delete(); err != nil {
		handleEnterpriseSonInternalError(c, "revoke sub-account token")
		return
	}
	model.RecordLog(adminId, model.LogTypeManage, fmt.Sprintf("revoked sub-account token user_id=%d token_id=%d", son.Id, token.Id))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

type SonStatusRequest struct {
	Id     int    `json:"id"`
	Action string `json:"action"`
}

func SonManageStatus(c *gin.Context) {
	topUserId := c.GetInt("id")
	var req SonStatusRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	topUser, err := model.GetUserById(topUserId, false)
	if err != nil {
		handleEnterpriseSonInternalError(c, "load enterprise owner for status update")
		return
	}
	query := model.DB.Where("type = ? AND topid = ? AND id = ?", 2, topUserId, req.Id)
	if topUser.EnterpriseId > 0 {
		query = model.DB.Where("type = ? AND enterprise_id = ? AND id = ?", 2, topUser.EnterpriseId, req.Id)
	}
	var sonUser model.User
	if err := query.First(&sonUser).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "非直属子账号，无权操作"})
		return
	}
	if sonUser.Role == common.RoleRootUser {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无法操作超级管理员"})
		return
	}

	switch req.Action {
	case "disable":
		sonUser.Status = common.UserStatusDisabled
	case "enable":
		sonUser.Status = common.UserStatusEnabled
	default:
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	if err := sonUser.Update(false); err != nil {
		handleEnterpriseSonInternalError(c, "update sub-account status")
		return
	}

	model.RecordLog(topUserId, model.LogTypeManage, fmt.Sprintf("子账号 %s 状态变更为 %s", sonUser.Username, req.Action))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}
