package router

import (
	"github.com/gin-gonic/gin"

	"fleet-log-policy/internal/fleetlog/handler"
)

// RegisterFleetLog 在 /api/v1 下挂载题目 09 业务路由。
func RegisterFleetLog(r *gin.Engine, h *handler.Handler) {
	v1 := r.Group("/api/v1")
	h.Register(v1)
}
