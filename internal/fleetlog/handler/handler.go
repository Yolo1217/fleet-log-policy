// Package handler 提供题目 09 的 HTTP 适配层：鉴权、参数绑定与错误映射。
package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"fleet-log-policy/internal/fleetlog/entity"
	"fleet-log-policy/internal/fleetlog/model"
	"fleet-log-policy/internal/fleetlog/service"
	"fleet-log-policy/pkg/httpx"
)

// Handler 持有业务服务。
type Handler struct {
	svc *service.Service
}

// New 创建 HTTP 处理器。
func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

// Register 注册题目 09 全部路由。
func (h *Handler) Register(v1 *gin.RouterGroup) {
	v1.GET("/demo/accounts", h.accounts)
	v1.GET("/demo/vehicles", h.vehicles)

	app := v1.Group("/applications")
	{
		app.GET("", h.listApplications)
		app.GET("/:id", h.getApplication)
		app.POST("", h.withAccount(entity.RoleApplicant), h.createApplication)
		app.POST("/:id/resubmit", h.withAccount(entity.RoleApplicant), h.resubmit)
		app.POST("/:id/decide", h.withAccount(entity.RoleApprover), h.decide)
	}

	camp := v1.Group("/campaigns")
	{
		camp.GET("/latest", h.latestCampaign)
		camp.GET("/:id", h.getCampaign)
		camp.POST("/:id/pilot", h.withAccount(entity.RoleOperator), h.startPilot)
		camp.POST("/:id/promote", h.withAccount(entity.RoleOperator), h.promote)
		camp.POST("/:id/stop", h.withAccount(entity.RoleApprover), h.stop)
		camp.POST("/:id/restore", h.withAccount(entity.RoleApprover), h.restore)
		camp.POST("/:id/complete", h.withAccount(entity.RoleApprover), h.complete)
		camp.POST("/:id/restore/complete", h.withAccount(entity.RoleApprover), h.completeRestore)
	}

	v1.GET("/exceptions", h.exceptions)
	v1.GET("/audits", h.audits)

	vehicle := v1.Group("/vehicle")
	{
		vehicle.GET("/config", h.vehicleAuth, h.pull)
		vehicle.POST("/receipts", h.vehicleAuth, h.receipt)
	}
}

// ---------------- 鉴权 ----------------

// withAccount 校验会话令牌并把角色写入上下文；role 为空表示任意登录角色。
func (h *Handler) withAccount(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := strings.TrimSpace(c.GetHeader("X-Auth-Token"))
		acc, err := h.svc.AccountByToken(token)
		if err != nil {
			h.svc.RecordException(entity.ExcUnauthorized, "无效或缺失会话令牌: "+c.FullPath())
			fail(c, entity.Err("unauthorized", "缺少或无效的登录令牌"))
			c.Abort()
			return
		}
		if role != "" && acc.Role != role {
			h.svc.RecordException(entity.ExcForbidden, "角色越权访问: "+acc.Username+" -> "+c.FullPath())
			fail(c, entity.Err("forbidden", "当前角色无权执行该操作"))
			c.Abort()
			return
		}
		c.Set("account", acc)
		c.Next()
	}
}

// vehicleAuth 校验车辆凭证。
func (h *Handler) vehicleAuth(c *gin.Context) {
	token := strings.TrimSpace(c.GetHeader("X-Vehicle-Token"))
	v, err := h.svc.VehicleByToken(token)
	if err != nil {
		h.svc.RecordException(entity.ExcUnauthorized, "车辆凭证无效: "+c.FullPath())
		fail(c, entity.Err("unauthorized", "缺少或无效的车辆凭证"))
		c.Abort()
		return
	}
	c.Set("vehicle", v)
	c.Next()
}

func account(c *gin.Context) model.Account {
	v, _ := c.Get("account")
	acc, _ := v.(model.Account)
	return acc
}

func vehicleOf(c *gin.Context) model.Vehicle {
	v, _ := c.Get("vehicle")
	veh, _ := v.(model.Vehicle)
	return veh
}

// ---------------- 申请 ----------------

type createApplicationReq struct {
	Reason   string `json:"reason"`
	Interval int    `json:"interval"`
}

func (h *Handler) createApplication(c *gin.Context) {
	var req createApplicationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, entity.Err("invalid_argument", "请求体格式错误"))
		return
	}
	view, err := h.svc.CreateApplication(account(c), req.Reason, req.Interval)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Created(c, view)
}

func (h *Handler) resubmit(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req createApplicationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, entity.Err("invalid_argument", "请求体格式错误"))
		return
	}
	view, err := h.svc.Resubmit(account(c), id, req.Reason, req.Interval)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, view)
}

type decideReq struct {
	Approve      *bool  `json:"approve"`
	RejectReason string `json:"reject_reason"`
}

func (h *Handler) decide(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req decideReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Approve == nil {
		fail(c, entity.Err("invalid_argument", "必须明确 approve 为 true 或 false"))
		return
	}
	view, err := h.svc.Decide(account(c), id, *req.Approve, req.RejectReason)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, view)
}

func (h *Handler) listApplications(c *gin.Context) {
	list, err := h.svc.ApplicationList()
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, list)
}

func (h *Handler) getApplication(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	view, err := h.svc.ApplicationView(id)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, view)
}

// ---------------- 活动 ----------------

type pilotReq struct {
	Vehicles []string `json:"vehicles"`
}

func (h *Handler) startPilot(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req pilotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, entity.Err("invalid_argument", "请求体格式错误"))
		return
	}
	view, err := h.svc.StartPilot(account(c), id, req.Vehicles)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, view)
}

func (h *Handler) promote(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	view, err := h.svc.Promote(account(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, view)
}

type stopReq struct {
	Reason string `json:"reason"`
}

func (h *Handler) stop(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req stopReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, entity.Err("invalid_argument", "请求体格式错误"))
		return
	}
	view, err := h.svc.Stop(account(c), id, req.Reason)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, view)
}

func (h *Handler) restore(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	view, err := h.svc.ApproveRestore(account(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, view)
}

func (h *Handler) complete(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	view, err := h.svc.Complete(account(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, view)
}

func (h *Handler) completeRestore(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	view, err := h.svc.CompleteRestore(account(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, view)
}

func (h *Handler) getCampaign(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	view, err := h.svc.CampaignView(id)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, view)
}

func (h *Handler) latestCampaign(c *gin.Context) {
	view, err := h.svc.LatestCampaignView()
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, view)
}

// ---------------- 车端 ----------------

func (h *Handler) pull(c *gin.Context) {
	result, err := h.svc.Pull(vehicleOf(c))
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, result)
}

type receiptReq struct {
	Version    int    `json:"version"`
	Success    bool   `json:"success"`
	Reason     string `json:"reason"`
	RequestKey string `json:"request_key"`
}

func (h *Handler) receipt(c *gin.Context) {
	var req receiptReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, entity.Err("invalid_argument", "请求体格式错误"))
		return
	}
	res, err := h.svc.Receipt(vehicleOf(c), req.Version, req.Success, req.Reason, req.RequestKey)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, res)
}

// ---------------- 查询 ----------------

func (h *Handler) exceptions(c *gin.Context) {
	list, err := h.svc.Exceptions()
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, list)
}

func (h *Handler) audits(c *gin.Context) {
	list, err := h.svc.AuditList()
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, list)
}

func (h *Handler) accounts(c *gin.Context) {
	list, err := h.svc.Accounts()
	if err != nil {
		fail(c, err)
		return
	}
	type item struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Token       string `json:"token"`
	}
	out := make([]item, 0, len(list))
	for _, a := range list {
		out = append(out, item{Username: a.Username, DisplayName: a.DisplayName, Role: a.Role, Token: a.Token})
	}
	httpx.OK(c, gin.H{"accounts": out, "note": "演示用预置凭据，非真实用户数据"})
}

func (h *Handler) vehicles(c *gin.Context) {
	list, err := h.svc.Vehicles()
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, list)
}

// ---------------- 工具 ----------------

func idParam(c *gin.Context) (uint, bool) {
	raw := c.Param("id")
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || v == 0 {
		fail(c, entity.Err("invalid_argument", "无效的编号"))
		return 0, false
	}
	return uint(v), true
}

// fail 将业务错误映射为统一响应：unauthorized 401 / forbidden 403 / not_found 404 / conflict 409。
func fail(c *gin.Context, err error) {
	de, ok := err.(*entity.DomainError)
	status := http.StatusBadRequest
	code, message := "invalid_argument", "请求参数不合法"
	if ok {
		code, message = de.Code, de.Msg
		switch de.Code {
		case "unauthorized":
			status = http.StatusUnauthorized
		case "forbidden":
			status = http.StatusForbidden
		case "not_found":
			status = http.StatusNotFound
		case "conflict":
			status = http.StatusConflict
		}
	} else {
		status = http.StatusInternalServerError
		code, message = "internal_error", "服务端处理失败"
	}
	c.JSON(status, httpx.Response{
		RequestID: httpx.RequestID(c),
		Error:     &httpx.ErrorBody{Code: code, Message: message},
	})
}
