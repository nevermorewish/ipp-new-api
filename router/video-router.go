package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"

	"github.com/gin-gonic/gin"
)

func SetVideoRouter(router *gin.Engine) {
	seedanceArkRouter := router.Group("/api/v3/contents")
	seedanceArkRouter.Use(middleware.RouteTag("relay"), middleware.TokenAuth(), middleware.SystemPerformanceCheck())
	seedanceArkRouter.POST(
		"/generations/tasks",
		pinSeedanceNativeRoute(pluginruntime.Route{Method: http.MethodPost, Path: "/api/v3/contents/generations/tasks", Type: pluginruntime.RouteTypeSubmit, Decode: "createTask", Render: "taskCreated"}),
		middleware.ModelRequestRateLimit(), middleware.PrepareTaskPluginRoute(), middleware.Distribute(), controller.RelayTask,
	)
	seedanceArkRouter.GET(
		"/generations/tasks",
		pinSeedanceNativeRoute(pluginruntime.Route{Method: http.MethodGet, Path: "/api/v3/contents/generations/tasks", Type: pluginruntime.RouteTypeDynamic, Decode: "queryTask", Render: "taskStatus"}),
		middleware.PrepareTaskPluginRoute(),
	)
	seedanceArkRouter.GET(
		"/generations/tasks/:task_id",
		pinSeedanceNativeRoute(pluginruntime.Route{Method: http.MethodGet, Path: "/api/v3/contents/generations/tasks/:task_id", Type: pluginruntime.RouteTypeQuery, Render: "taskStatus", TaskIDParam: "task_id"}),
		middleware.PrepareTaskPluginRoute(),
	)

	videoSharedRouter := router.Group("/v1")
	videoSharedRouter.Use(middleware.RouteTag("relay"))
	videoSharedRouter.Use(middleware.TokenAuth())
	videoSharedRouter.Use(middleware.SystemPerformanceCheck())
	videoSharedRouter.POST(
		"/video/generations",
		middleware.PinTaskPluginEndpoint(),
		middleware.TaskPluginEndpointOnly(middleware.ModelRequestRateLimit()),
		middleware.PrepareTaskPluginEndpoint(),
		middleware.Distribute(),
		func(c *gin.Context) {
			controller.RelayTaskPluginEndpoint(c, controller.RelayTask)
		},
	)

	videoV1Router := router.Group("/v1")
	videoV1Router.Use(middleware.RouteTag("relay"))
	videoV1Router.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		videoV1Router.GET("/video/generations/:task_id", controller.RelayTaskFetch)
		videoV1Router.POST("/videos/:video_id/remix", controller.RelayTask)
	}
}

func pinSeedanceNativeRoute(route pluginruntime.Route) gin.HandlerFunc {
	return func(c *gin.Context) {
		generation := pluginruntime.DefaultRegistry.Generation()
		if generation == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "task_plugin_unavailable", "message": "Seedance task plugin is unavailable"}})
			return
		}
		plugin, found := generation.Get("doubao")
		if !found {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "task_plugin_unavailable", "message": "Seedance task plugin is unavailable"}})
			return
		}
		c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Generation: generation, Plugin: plugin, Route: route})
		c.Next()
	}
}
