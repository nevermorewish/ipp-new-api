package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/gin-gonic/gin"
)

const enterpriseModelAliasBodyLimit = 16 << 10

type enterpriseModelAliasUpsertRequest struct {
	// OwnerUserID is accepted only to make accidental/legacy clients harmless;
	// ownership is always derived from the authenticated context.
	OwnerUserID     int     `json:"owner_user_id,omitempty"`
	Alias           string  `json:"alias"`
	UpstreamModelID string  `json:"upstream_model_id"`
	Enabled         *bool   `json:"enabled,omitempty"`
	ExpectedVersion *uint64 `json:"expected_version,omitempty"`
}

type enterpriseModelAliasDeleteRequest struct {
	ExpectedVersion *uint64 `json:"expected_version,omitempty"`
}

func GetEnterpriseModelAlias(c *gin.Context) {
	common.SetNoStoreHeaders(c)
	result, err := model.GetEnterpriseModelAliasBySource(c.GetInt("id"), c.Param("source_id"))
	if err != nil {
		if errors.Is(err, model.ErrEnterpriseModelAliasNotFound) {
			writeEnterpriseModelAliasAbsence(c, c.GetInt("id"), c.Param("source_id"))
			return
		}
		handleEnterpriseModelAliasError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func UpsertEnterpriseModelAlias(c *gin.Context) {
	common.SetNoStoreHeaders(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, enterpriseModelAliasBodyLimit)
	var request enterpriseModelAliasUpsertRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		writeEnterpriseModelAliasError(c, http.StatusBadRequest, "invalid enterprise model alias request")
		return
	}
	request.UpstreamModelID = strings.TrimSpace(request.UpstreamModelID)
	if request.UpstreamModelID == "" || !helper.HasModelBillingConfig(request.UpstreamModelID) {
		writeEnterpriseModelAliasError(c, http.StatusBadRequest, "upstream model is not available with a billing configuration")
		return
	}

	result, err := model.UpsertEnterpriseModelAlias(c.GetInt("id"), model.EnterpriseModelAliasMutation{
		SourceID:        c.Param("source_id"),
		Alias:           request.Alias,
		UpstreamModelID: request.UpstreamModelID,
		Enabled:         request.Enabled,
		ExpectedVersion: request.ExpectedVersion,
	})
	if err != nil {
		handleEnterpriseModelAliasError(c, err)
		return
	}
	recordEnterpriseModelAliasOperation(c, "enterprise_model_alias.upsert", result, false)
	common.ApiSuccess(c, result)
}

func DeleteEnterpriseModelAlias(c *gin.Context) {
	common.SetNoStoreHeaders(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, enterpriseModelAliasBodyLimit)
	var request enterpriseModelAliasDeleteRequest
	if err := c.ShouldBindJSON(&request); err != nil && !errors.Is(err, io.EOF) {
		writeEnterpriseModelAliasError(c, http.StatusBadRequest, "invalid enterprise model alias request")
		return
	}

	result, err := model.TombstoneEnterpriseModelAlias(c.GetInt("id"), c.Param("source_id"), request.ExpectedVersion)
	if err != nil {
		if errors.Is(err, model.ErrEnterpriseModelAliasNotFound) &&
			(request.ExpectedVersion == nil || *request.ExpectedVersion == 0) {
			absent := model.EnterpriseModelAlias{
				OwnerUserID: c.GetInt("id"),
				SourceID:    strings.TrimSpace(c.Param("source_id")),
				Status:      model.EnterpriseModelAliasStatusTombstone,
			}
			recordEnterpriseModelAliasOperation(c, "enterprise_model_alias.delete", absent, true)
			writeEnterpriseModelAliasAbsence(c, absent.OwnerUserID, absent.SourceID)
			return
		}
		handleEnterpriseModelAliasError(c, err)
		return
	}
	recordEnterpriseModelAliasOperation(c, "enterprise_model_alias.delete", result, false)
	common.ApiSuccess(c, result)
}

func writeEnterpriseModelAliasAbsence(c *gin.Context, ownerUserID int, sourceID string) {
	common.ApiSuccess(c, gin.H{
		"owner_user_id": ownerUserID,
		"source_id":     strings.TrimSpace(sourceID),
		"status":        model.EnterpriseModelAliasStatusTombstone,
		"version":       0,
		"absent":        true,
	})
}

func recordEnterpriseModelAliasOperation(c *gin.Context, action string, result model.EnterpriseModelAlias, absent bool) {
	params := map[string]interface{}{
		"source_id": result.SourceID,
		"status":    result.Status,
		"version":   result.Version,
	}
	if result.Alias != "" {
		params["alias"] = result.Alias
	}
	if result.UpstreamModelID != "" {
		params["upstream_model_id"] = result.UpstreamModelID
	}
	if absent {
		params["absent"] = true
	}
	auditInfo := map[string]interface{}{
		"method": c.Request.Method,
		"path":   c.Request.URL.Path,
		"result": "success",
	}
	model.RecordOperationAuditLog(
		result.OwnerUserID,
		fmt.Sprintf("enterprise model alias operation succeeded: %s", action),
		c.ClientIP(),
		action,
		params,
		auditOperatorInfo(c),
		auditInfo,
	)
	markAuditLogged(c)
}

func handleEnterpriseModelAliasError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrEnterpriseModelAliasInvalid),
		errors.Is(err, model.ErrEnterpriseModelAliasUnsupported):
		writeEnterpriseModelAliasError(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, model.ErrEnterpriseModelAliasConflict),
		errors.Is(err, model.ErrEnterpriseModelAliasImmutable),
		errors.Is(err, model.ErrEnterpriseModelAliasVersionConflict):
		writeEnterpriseModelAliasError(c, http.StatusConflict, err.Error())
	case errors.Is(err, model.ErrEnterpriseModelAliasForbidden):
		writeEnterpriseModelAliasError(c, http.StatusForbidden, "enterprise model alias is not owned by this enterprise")
	case errors.Is(err, model.ErrEnterpriseModelAliasNotFound):
		writeEnterpriseModelAliasError(c, http.StatusNotFound, "enterprise model alias not found")
	default:
		common.SysLog("enterprise model alias database operation failed: " + err.Error())
		writeEnterpriseModelAliasError(c, http.StatusInternalServerError, "enterprise model alias operation failed")
	}
}

func writeEnterpriseModelAliasError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{
		"success": false,
		"message": message,
	})
}
