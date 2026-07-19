package middleware

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

var errEnterpriseModelAliasInactive = errors.New("enterprise model alias is inactive")

// resolveEnterpriseModelRequest canonicalizes an enterprise-scoped WorkBuddy
// alias before token limits, pricing, channel affinity, and channel selection.
// The original alias remains in context for audit and usage-log attribution.
func resolveEnterpriseModelRequest(c *gin.Context, request *ModelRequest) error {
	if c == nil || request == nil || strings.TrimSpace(request.Model) == "" {
		return nil
	}
	ownerUserID := enterpriseAliasOwnerUserID(c)
	if ownerUserID <= 0 {
		return nil
	}

	requestedModel := strings.TrimSpace(request.Model)
	lookupAlias := requestedModel
	isCompact := strings.HasSuffix(lookupAlias, ratio_setting.CompactModelSuffix)
	if isCompact {
		lookupAlias = strings.TrimSuffix(lookupAlias, ratio_setting.CompactModelSuffix)
	}

	resolution, found, err := model.ResolveEnterpriseModelAlias(ownerUserID, lookupAlias)
	if err != nil || !found {
		return err
	}
	if resolution.Status != model.EnterpriseModelAliasStatusActive {
		return errEnterpriseModelAliasInactive
	}

	canonicalModel := resolution.UpstreamModelID
	if isCompact {
		canonicalModel = ratio_setting.WithCompactModelSuffix(canonicalModel)
	}
	common.SetContextKey(c, constant.ContextKeyRequestedModel, lookupAlias)
	request.Model = canonicalModel
	return nil
}

func enterpriseAliasOwnerUserID(c *gin.Context) int {
	userType := common.GetContextKeyInt(c, constant.ContextKeyUserType)
	switch userType {
	case 1:
		return c.GetInt("id")
	case 2:
		return common.GetContextKeyInt(c, constant.ContextKeyUserTopId)
	default:
		return 0
	}
}
