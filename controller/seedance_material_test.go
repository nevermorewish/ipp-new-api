package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/embed"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const materialEnvelopeResponse = `{"ResponseMetadata":{"RequestId":"sys-1","Action":"CreateAssetGroup","Version":"2024-01-01","Region":"cn-beijing"},"Result":{"Id":"group-example-id"}}`

func setupSeedanceMaterialTestDB(t *testing.T) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}))

	originalDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = originalDB
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

func insertSeedanceChannel(t *testing.T, baseURL, key, group string, channelType, status int) {
	t.Helper()

	channel := &model.Channel{
		Type:    channelType,
		Key:     key,
		Status:  status,
		Name:    "seedance",
		BaseURL: &baseURL,
		Group:   group,
	}
	require.NoError(t, model.DB.Create(channel).Error)
}

func serveSeedanceMaterial(group string) (*httptest.ResponseRecorder, *gin.Engine) {
	recorder := httptest.NewRecorder()
	router := gin.New()
	router.POST("/api/material", func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
		ProxySeedanceMaterial(c)
	})
	return recorder, router
}

// The caller's own token must never leave this gateway: the upstream is
// authenticated with the Seedance channel key, and the Action query plus body
// are forwarded unchanged.
func TestProxySeedanceMaterialForwardsWithChannelKey(t *testing.T) {
	setupSeedanceMaterialTestDB(t)

	var gotPath, gotQuery, gotAuthorization, gotContentType, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.Path
		gotQuery = request.URL.RawQuery
		gotAuthorization = request.Header.Get("Authorization")
		gotContentType = request.Header.Get("Content-Type")
		body, _ := io.ReadAll(request.Body)
		gotBody = string(body)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(materialEnvelopeResponse))
	}))
	t.Cleanup(upstream.Close)
	insertSeedanceChannel(t, upstream.URL, "sk-upstream", "default", constant.ChannelTypeSeedance, common.ChannelStatusEnabled)

	recorder, router := serveSeedanceMaterial("default")
	request := httptest.NewRequest(http.MethodPost, "/api/material?Action=CreateAssetGroup", strings.NewReader(`{"Name":"角色素材库"}`))
	request.Header.Set("Authorization", "Bearer sk-caller-token")
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "/api/material", gotPath)
	assert.Equal(t, "Action=CreateAssetGroup", gotQuery)
	assert.Equal(t, "Bearer sk-upstream", gotAuthorization)
	assert.Equal(t, "application/json", gotContentType)
	assert.JSONEq(t, `{"Name":"角色素材库"}`, gotBody)
	assert.JSONEq(t, materialEnvelopeResponse, recorder.Body.String())
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
}

// Business errors live inside Result.Error, so upstream status codes and bodies
// must reach the caller untouched.
func TestProxySeedanceMaterialPassesUpstreamBusinessError(t *testing.T) {
	setupSeedanceMaterialTestDB(t)

	businessError := `{"ResponseMetadata":{"RequestId":"sys-2","Action":"GetAsset","Version":"2024-01-01","Region":"cn-beijing"},"Result":{"Error":{"Code":"ResourceNotFound","Message":"asset not found"}}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(businessError))
	}))
	t.Cleanup(upstream.Close)
	insertSeedanceChannel(t, upstream.URL, "sk-upstream", "default", constant.ChannelTypeSeedance, common.ChannelStatusEnabled)

	recorder, router := serveSeedanceMaterial("default")
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/material?Action=GetAsset", strings.NewReader(`{"Id":"asset-1"}`)))

	require.Equal(t, http.StatusNotFound, recorder.Code)
	assert.JSONEq(t, businessError, recorder.Body.String())
}

func TestProxySeedanceMaterialRejectsNonEnvelopeResponse(t *testing.T) {
	setupSeedanceMaterialTestDB(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`<html>login</html>`))
	}))
	t.Cleanup(upstream.Close)
	insertSeedanceChannel(t, upstream.URL, "sk-upstream", "default", constant.ChannelTypeSeedance, common.ChannelStatusEnabled)

	recorder, router := serveSeedanceMaterial("default")
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/material?Action=GetAsset", strings.NewReader(`{"Id":"asset-1"}`)))

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	assert.Equal(t, "MaterialInvalidResponse", materialErrorCode(t, recorder.Body.Bytes()))
}

func TestProxySeedanceMaterialRequiresAction(t *testing.T) {
	setupSeedanceMaterialTestDB(t)

	recorder, router := serveSeedanceMaterial("default")
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/material", strings.NewReader(`{}`)))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Equal(t, "InvalidRequest", materialErrorCode(t, recorder.Body.Bytes()))
}

func TestProxySeedanceMaterialChannelResolution(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		status      int
		group       string
		callerGroup string
	}{
		{name: "channel type is not Seedance", channelType: constant.ChannelTypeOpenAI, status: common.ChannelStatusEnabled, group: "default", callerGroup: "default"},
		{name: "channel disabled", channelType: constant.ChannelTypeSeedance, status: common.ChannelStatusManuallyDisabled, group: "default", callerGroup: "default"},
		{name: "group not served by the channel", channelType: constant.ChannelTypeSeedance, status: common.ChannelStatusEnabled, group: "vip", callerGroup: "default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupSeedanceMaterialTestDB(t)
			insertSeedanceChannel(t, "https://upstream.example.com", "sk-upstream", tt.group, tt.channelType, tt.status)

			recorder, router := serveSeedanceMaterial(tt.callerGroup)
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/material?Action=CreateAssetGroup", strings.NewReader(`{"Name":"x"}`)))

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			assert.Equal(t, "MaterialUpstreamUnavailable", materialErrorCode(t, recorder.Body.Bytes()))
		})
	}
}

func materialErrorCode(t *testing.T, body []byte) string {
	t.Helper()

	var envelope struct {
		Result struct {
			Error struct {
				Code string `json:"Code"`
			} `json:"Error"`
		} `json:"Result"`
	}
	require.NoError(t, common.Unmarshal(body, &envelope))
	return envelope.Result.Error.Code
}

func TestProxySeedanceAPIMaterialSignedIdentityAndRedirect(t *testing.T) {
	setupSeedanceMaterialTestDB(t)
	secret := "material-signing-secret"
	var gotHeaders http.Header
	var gotBody []byte
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	t.Cleanup(target.Close)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		if r.URL.Query().Get("Action") == "Redirect" {
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
			return
		}
		_, _ = w.Write([]byte(materialEnvelopeResponse))
	}))
	t.Cleanup(upstream.Close)
	insertSeedanceChannel(t, upstream.URL, secret, "default", constant.ChannelTypeSeedanceAPI, common.ChannelStatusEnabled)
	engine := gin.New()
	engine.POST("/api/material", func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		if c.GetHeader("Authorization") != "" {
			common.SetContextKey(c, constant.ContextKeyTokenId, 42)
			common.SetContextKey(c, constant.ContextKeyTokenKey, "caller-secret")
		}
		ProxySeedanceMaterial(c)
	})
	request := httptest.NewRequest(http.MethodPost, "/api/material?Action=CreateVisualValidateSession", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer caller-secret")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Empty(t, gotHeaders.Get("Authorization"))
	payload, err := base64.RawURLEncoding.DecodeString(gotHeaders.Get(embed.HeaderContext))
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "caller-secret")
	mac := hmac.New(sha256.New, []byte(secret))
	_, err = mac.Write([]byte(gotHeaders.Get(embed.HeaderContext)))
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), gotHeaders.Get(embed.HeaderSignature))
	assert.Contains(t, string(gotBody), "/docs/seedance/assets.html")
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/material?Action=Redirect", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer caller-secret")
	engine.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusBadGateway, recorder.Code)
	assert.False(t, redirected)
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/material?Action=ListAssets", strings.NewReader(`{}`)))
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
}
