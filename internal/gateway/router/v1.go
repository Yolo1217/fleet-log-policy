package router

import "github.com/gin-gonic/gin"

// registerAPIRoutes 创建 /api/v1 路由命名空间。
// 新增服务时，在该文件旁边增加对应业务路由文件。
func registerAPIRoutes(r *gin.Engine) {
	api := r.Group("/api")
	v1 := api.Group("/v1")
	_ = v1
}
