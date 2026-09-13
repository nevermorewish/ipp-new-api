package controller

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// ProxySeedanceMaterial forwards material-library requests through an enabled
// Seedance channel. Assets created here can then be referenced as asset:// IDs
// in Seedance video requests sent through the same upstream channel.
func ProxySeedanceMaterial(c *gin.Context) {
	if strings.TrimSpace(c.Query("Action")) == "" {
		writeSeedanceMaterialError(c, http.StatusBadRequest, "InvalidRequest", "missing Action query parameter")
		return
	}

	channel, err := seedanceMaterialChannel(c)
	if err != nil {
		writeSeedanceMaterialError(c, http.StatusServiceUnavailable, "MaterialUpstreamUnavailable", err.Error())
		return
	}
	key, _, keyErr := channel.GetNextEnabledKey()
	if keyErr != nil {
		writeSeedanceMaterialError(c, http.StatusServiceUnavailable, "MaterialUpstreamUnavailable", "the Seedance channel has no available key")
		return
	}

	storage, err := common.GetBodyStorage(c)
	if err != nil {
		status := http.StatusBadRequest
		if common.IsRequestBodyTooLargeError(err) {
			status = http.StatusRequestEntityTooLarge
		}
		writeSeedanceMaterialError(c, status, "InvalidRequest", "failed to read request body")
		return
	}
	requestBody, err := storage.Bytes()
	if err != nil {
		writeSeedanceMaterialError(c, http.StatusBadRequest, "InvalidRequest", "failed to read request body")
		return
	}

	targetURL := strings.TrimRight(channel.GetBaseURL(), "/") + "/api/material"
	if c.Request.URL.RawQuery != "" {
		targetURL += "?" + c.Request.URL.RawQuery
	}
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, targetURL, bytes.NewReader(requestBody))
	if err != nil {
		writeSeedanceMaterialError(c, http.StatusBadRequest, "InvalidRequest", err.Error())
		return
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	client, err := service.GetHttpClientWithProxy(channel.GetSetting().Proxy)
	if err != nil {
		writeSeedanceMaterialError(c, http.StatusServiceUnavailable, "MaterialUpstreamUnavailable", "new proxy http client failed")
		return
	}
	response, err := client.Do(request)
	if err != nil {
		writeSeedanceMaterialError(c, http.StatusBadGateway, "MaterialUpstreamError", "Seedance material service request failed")
		return
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		writeSeedanceMaterialError(c, http.StatusBadGateway, "MaterialUpstreamError", "failed to read Seedance material response")
		return
	}
	var envelope map[string]any
	if err := common.Unmarshal(body, &envelope); err != nil || envelope["ResponseMetadata"] == nil || envelope["Result"] == nil {
		writeSeedanceMaterialError(c, http.StatusBadGateway, "MaterialInvalidResponse", "Seedance material service returned a non-standard response")
		return
	}

	c.Header("Cache-Control", "no-store")
	if contentType := response.Header.Get("Content-Type"); contentType != "" {
		c.Header("Content-Type", contentType)
	}
	c.Status(response.StatusCode)
	_, _ = c.Writer.Write(body)
}

func seedanceMaterialChannel(c *gin.Context) (*model.Channel, error) {
	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		return nil, fmt.Errorf("load Seedance channels failed: %w", err)
	}
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	for _, channel := range channels {
		if channel.Status != common.ChannelStatusEnabled ||
			channel.Type != constant.ChannelTypeSeedance ||
			(group != "" && !slices.Contains(channel.GetGroups(), group)) {
			continue
		}
		parsed, parseErr := url.Parse(strings.TrimSpace(channel.GetBaseURL()))
		if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			continue
		}
		return channel, nil
	}
	return nil, fmt.Errorf("no enabled Seedance channel matches the token group")
}

func writeSeedanceMaterialError(c *gin.Context, status int, code, message string) {
	action := strings.TrimSpace(c.Query("Action"))
	if action == "" {
		action = "Unknown"
	}
	c.JSON(status, gin.H{
		"ResponseMetadata": gin.H{
			"RequestId": common.GetUUID(),
			"Action":    action,
			"Version":   "2024-01-01",
			"Service":   "ark",
			"Region":    "cn-beijing",
		},
		"Result": gin.H{
			"Error": gin.H{"Code": code, "Message": message},
		},
	})
}
