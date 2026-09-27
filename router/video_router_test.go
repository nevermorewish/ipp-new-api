package router

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeedanceArkRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetVideoRouter(engine)

	routes := make(map[string]bool)
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	assert.True(t, routes[http.MethodPost+" /api/v3/contents/generations/tasks"])
	assert.True(t, routes[http.MethodGet+" /api/v3/contents/generations/tasks"])
	assert.True(t, routes[http.MethodGet+" /api/v3/contents/generations/tasks/:task_id"])
}

func TestGetOpenAIVideoRouteRendersJimengTask(t *testing.T) {
	gin.SetMode(gin.TestMode)

	previousDB := model.DB
	previousDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	previousSQLitePath := common.SQLitePath
	previousMasterNode := common.IsMasterNode
	previousRedisEnabled := common.RedisEnabled
	common.SQLitePath = t.TempDir() + "/router-video.db"
	common.IsMasterNode = false
	common.RedisEnabled = false
	t.Setenv("SQL_DSN", "")
	require.NoError(t, model.InitDB())
	database := model.DB
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Task{}))
	t.Cleanup(func() {
		sqlDB, closeErr := database.DB()
		require.NoError(t, closeErr)
		require.NoError(t, sqlDB.Close())
		model.DB = previousDB
		common.SetDatabaseTypes(previousDatabaseType, previousLogDatabaseType)
		common.SQLitePath = previousSQLitePath
		common.IsMasterNode = previousMasterNode
		common.RedisEnabled = previousRedisEnabled
	})

	require.NoError(t, database.Create(&model.User{
		Id:          91,
		Username:    "jimeng-fetch-user",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Quota:       100,
		Group:       "default",
		AuthVersion: 1,
	}).Error)
	require.NoError(t, database.Create(&model.Token{
		Id:             1,
		UserId:         91,
		Key:            "jimengfetch",
		Status:         common.TokenStatusEnabled,
		Name:           "jimeng fetch",
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)
	require.NoError(t, database.Create(&model.Channel{
		Id:     17,
		Type:   constant.ChannelTypeJimeng,
		Key:    "unused",
		Status: common.ChannelStatusEnabled,
		Name:   "jimeng fetch",
		Models: "jimeng_vgfm_t2v_l20",
		Group:  "default",
	}).Error)

	task := &model.Task{
		CreatedAt: 1710000000,
		UpdatedAt: 1710000060,
		TaskID:    "task_jimeng_public",
		Platform:  constant.TaskPlatform("jimeng"),
		UserId:    91,
		Group:     "default",
		ChannelId: 17,
		Status:    model.TaskStatusSuccess,
		Progress:  "100%",
		PrivateData: model.TaskPrivateData{
			ResultURL: "data:video/mp4;base64,ZGF0YQ==",
		},
	}
	task.SetData(map[string]any{
		"code": 10000,
		"data": map[string]any{
			"status":    "done",
			"task_id":   "jimeng-private-1",
			"video_url": "https://cdn.example/video.mp4",
		},
		"message": "success",
	})
	require.NoError(t, database.Create(task).Error)

	engine := gin.New()
	SetVideoRouter(engine)
	SetTaskPluginProtocolRouter(engine)
	request := httptest.NewRequest(http.MethodGet, "/v1/videos/task_jimeng_public", nil)
	request.Header.Set("Authorization", "Bearer sk-jimengfetch")
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response struct {
		ID          string `json:"id"`
		Object      string `json:"object"`
		Status      string `json:"status"`
		Progress    int    `json:"progress"`
		CreatedAt   int64  `json:"created_at"`
		CompletedAt int64  `json:"completed_at"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "task_jimeng_public", response.ID)
	assert.Equal(t, "video", response.Object)
	assert.Equal(t, "completed", response.Status)
	assert.Equal(t, 100, response.Progress)
	assert.Equal(t, int64(1710000000), response.CreatedAt)
	assert.Equal(t, int64(1710000060), response.CompletedAt)
	assert.NotContains(t, recorder.Body.String(), "cdn.example")
	assert.NotContains(t, recorder.Body.String(), "jimeng-private-1")

	for _, testCase := range []struct {
		name          string
		authorization string
		query         string
		wantStatus    int
	}{
		{name: "missing credential rejected", wantStatus: http.StatusUnauthorized},
		{name: "access rejected", query: "?access=not-a-video-credential", wantStatus: http.StatusUnauthorized},
		{name: "bearer accepted", authorization: "Bearer sk-jimengfetch", wantStatus: http.StatusOK},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodGet,
				"/v1/videos/task_jimeng_public/content"+testCase.query,
				nil,
			)
			if testCase.authorization != "" {
				request.Header.Set("Authorization", testCase.authorization)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			assert.Equal(t, testCase.wantStatus, recorder.Code, recorder.Body.String())
			if testCase.wantStatus == http.StatusOK {
				assert.Equal(t, "data", recorder.Body.String())
			}
		})
	}
}

func TestServeDocsPagesAndAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	assets := os.DirFS("../doc")
	engine := gin.New()
	engine.GET("/docs/*filepath", serveDocs(assets))
	count := 0
	require.NoError(t, fs.WalkDir(assets, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		count++
		expected, err := fs.ReadFile(assets, name)
		require.NoError(t, err)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/docs/"+name, nil))
		require.Equal(t, http.StatusOK, recorder.Code, name)
		assert.Equal(t, expected, recorder.Body.Bytes(), name)
		assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store", name)
		if strings.HasSuffix(name, ".html") {
			assert.Contains(t, recorder.Header().Get("Content-Type"), "text/html", name)
		}
		return nil
	}))
	assert.Greater(t, count, 100)
}

func TestServeDocsFallbackAndMissingAsset(t *testing.T) {
	engine := gin.New()
	engine.GET("/docs/*filepath", serveDocs(fstest.MapFS{
		"index.html":          &fstest.MapFile{Data: []byte("documentation home")},
		"seedance/index.html": &fstest.MapFile{Data: []byte("video docs")},
	}))
	for _, tc := range []struct {
		path   string
		status int
		body   string
	}{
		{"/docs/", 200, "documentation home"},
		{"/docs/seedance/", 200, "video docs"},
		{"/docs/unknown/page.html", 200, "documentation home"},
		{"/docs/missing.png", 404, ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
			assert.Equal(t, tc.status, recorder.Code)
			if tc.body != "" {
				assert.Equal(t, tc.body, recorder.Body.String())
			}
		})
	}
}

func TestSeedanceNativeRouteSelectsMatchingVideoPlugin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ model, plugin string }{
		{"doubao-seedance-2.0-mini", "seedanceapi"},
		{"doubao-seedance-2-0-260128", "doubao"},
	} {
		t.Run(tc.plugin, func(t *testing.T) {
			engine := gin.New()
			engine.POST("/api/v3/contents/generations/tasks", pinSeedanceNativeRoute(jsplugin.Route{Method: http.MethodPost, Type: jsplugin.RouteTypeSubmit}), func(c *gin.Context) {
				pinned, exists := c.Get(jsplugin.ContextKeyPinnedRoute)
				require.True(t, exists)
				assert.Equal(t, tc.plugin, pinned.(jsplugin.PinnedRoute).Plugin.Meta.Key)
				storage, err := common.GetBodyStorage(c)
				require.NoError(t, err)
				body, err := storage.Bytes()
				require.NoError(t, err)
				assert.Contains(t, string(body), tc.model)
				c.Status(http.StatusNoContent)
			})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v3/contents/generations/tasks", strings.NewReader(`{"model":"`+tc.model+`","content":[{"type":"text","text":"ball"}]}`))
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)
			assert.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
		})
	}
}
