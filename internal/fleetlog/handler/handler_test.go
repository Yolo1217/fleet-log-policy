package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"fleet-log-policy/internal/fleetlog/repo"
	"fleet-log-policy/internal/fleetlog/service"
)

func newTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, err := repo.Open(filepath.Join(t.TempDir(), "handler.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.Seed(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := store.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	svc := service.New(store, 60)
	h := New(svc)
	r := gin.New()
	h.Register(r.Group("/api/v1"))
	return r
}

type apiResp struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func do(t *testing.T, r *gin.Engine, method, path string, body interface{}, headers map[string]string) (int, apiResp) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out apiResp
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func auth(token string) map[string]string { return map[string]string{"X-Auth-Token": token} }

func TestAuthAndRoleGuards(t *testing.T) {
	r := newTestRouter(t)
	if code, _ := do(t, r, http.MethodPost, "/api/v1/applications", map[string]interface{}{"reason": "x", "interval": 30}, nil); code != http.StatusUnauthorized {
		t.Fatalf("缺少令牌应 401，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/applications", map[string]interface{}{"reason": "x", "interval": 30}, auth("bad-token")); code != http.StatusUnauthorized {
		t.Fatalf("无效令牌应 401，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/applications", map[string]interface{}{"reason": "x", "interval": 30}, auth("demo-token-operator")); code != http.StatusForbidden {
		t.Fatalf("角色不符应 403，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodGet, "/api/v1/vehicle/config", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("缺少车辆凭证应 401，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodGet, "/api/v1/vehicle/config", nil, map[string]string{"X-Vehicle-Token": "bad"}); code != http.StatusUnauthorized {
		t.Fatalf("无效车辆凭证应 401，实际 %d", code)
	}
}

func TestApplicationHTTPFlow(t *testing.T) {
	r := newTestRouter(t)
	if code, _ := do(t, r, http.MethodPost, "/api/v1/applications", "not-json", auth("demo-token-applicant")); code != http.StatusBadRequest {
		t.Fatalf("非法请求体应 400，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/applications", map[string]interface{}{"reason": "", "interval": 30}, auth("demo-token-applicant")); code != http.StatusBadRequest {
		t.Fatalf("原因为空应 400，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/applications", map[string]interface{}{"reason": "原因", "interval": 5}, auth("demo-token-applicant")); code != http.StatusBadRequest {
		t.Fatalf("间隔越界应 400，实际 %d", code)
	}
	code, resp := do(t, r, http.MethodPost, "/api/v1/applications", map[string]interface{}{"reason": "排查抖动", "interval": 30}, auth("demo-token-applicant"))
	if code != http.StatusCreated {
		t.Fatalf("创建申请应 201，实际 %d", code)
	}
	var app struct {
		ID     uint `json:"id"`
		Status string
	}
	_ = json.Unmarshal(resp.Data, &app)
	if app.ID == 0 {
		t.Fatalf("返回编号无效: %s", string(resp.Data))
	}
	if code, _ := do(t, r, http.MethodGet, "/api/v1/applications/abc", nil, auth("demo-token-approver")); code != http.StatusBadRequest {
		t.Fatalf("非法编号应 400，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodGet, "/api/v1/applications/999", nil, auth("demo-token-approver")); code != http.StatusNotFound {
		t.Fatalf("不存在申请应 404，实际 %d", code)
	}
	if code, resp := do(t, r, http.MethodGet, "/api/v1/applications", nil, auth("demo-token-approver")); code != http.StatusOK || len(resp.Data) == 0 {
		t.Fatalf("申请列表应 200，实际 %d", code)
	}
	// 退回后重提。
	if code, _ := do(t, r, http.MethodPost, "/api/v1/applications/1/decide", map[string]interface{}{"approve": false}, auth("demo-token-approver")); code != http.StatusBadRequest {
		t.Fatalf("退回未填原因应 400，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/applications/1/decide", map[string]interface{}{"approve": false, "reject_reason": "原因不清"}, auth("demo-token-approver")); code != http.StatusOK {
		t.Fatalf("退回应 200，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/applications", map[string]interface{}{"reason": "x"}, auth("demo-token-approver")); code != http.StatusForbidden {
		t.Fatalf("负责人创建申请应 403，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/applications/1/resubmit", map[string]interface{}{"reason": "补充原因", "interval": 40}, auth("demo-token-applicant")); code != http.StatusOK {
		t.Fatalf("重提应 200，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/applications/1/decide", map[string]interface{}{}, auth("demo-token-approver")); code != http.StatusBadRequest {
		t.Fatalf("缺少 approve 应 400，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/applications/1/decide", map[string]interface{}{"approve": true}, auth("demo-token-operator")); code != http.StatusForbidden {
		t.Fatalf("运营审批应 403，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/applications/1/decide", map[string]interface{}{"approve": true}, auth("demo-token-approver")); code != http.StatusOK {
		t.Fatalf("批准应 200，实际 %d", code)
	}
}

func TestCampaignHTTPFlow(t *testing.T) {
	r := newTestRouter(t)
	do(t, r, http.MethodPost, "/api/v1/applications", map[string]interface{}{"reason": "排查抖动", "interval": 30}, auth("demo-token-applicant"))
	do(t, r, http.MethodPost, "/api/v1/applications/1/decide", map[string]interface{}{"approve": true}, auth("demo-token-approver"))

	code, resp := do(t, r, http.MethodGet, "/api/v1/campaigns/latest", nil, nil)
	if code != http.StatusOK || len(resp.Data) == 0 {
		t.Fatalf("最新活动应 200，实际 %d", code)
	}
	var camp struct {
		ID uint `json:"id"`
	}
	_ = json.Unmarshal(resp.Data, &camp)

	if code, _ := do(t, r, http.MethodGet, "/api/v1/campaigns/999", nil, nil); code != http.StatusNotFound {
		t.Fatalf("不存在活动应 404，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/1/pilot", "bad", auth("demo-token-operator")); code != http.StatusBadRequest {
		t.Fatalf("非法请求体应 400，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/1/pilot", map[string]interface{}{"vehicles": []string{"TE-01"}}, auth("demo-token-operator")); code != http.StatusBadRequest {
		t.Fatalf("试点非 2 台应 400，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/1/pilot", map[string]interface{}{"vehicles": []string{"TE-01", "TE-02"}}, auth("demo-token-approver")); code != http.StatusForbidden {
		t.Fatalf("非运营试点应 403，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/1/promote", nil, auth("demo-token-operator")); code != http.StatusConflict {
		t.Fatalf("未试点推广应 409，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/1/pilot", map[string]interface{}{"vehicles": []string{"TE-01", "TE-02"}}, auth("demo-token-operator")); code != http.StatusOK {
		t.Fatalf("试点应 200，实际 %d", code)
	}
	for _, tok := range []string{"veh-token-01", "veh-token-02"} {
		if code, _ := do(t, r, http.MethodPost, "/api/v1/vehicle/receipts", map[string]interface{}{"version": 1, "success": true}, map[string]string{"X-Vehicle-Token": tok}); code != http.StatusOK {
			t.Fatalf("回执应 200，实际 %d", code)
		}
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/1/promote", map[string]interface{}{}, auth("demo-token-operator")); code != http.StatusOK {
		t.Fatalf("推广应 200，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/1/complete", nil, auth("demo-token-approver")); code != http.StatusConflict {
		t.Fatalf("未全部成功结案应 409，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/1/stop", map[string]interface{}{"reason": "试点异常"}, auth("demo-token-operator")); code != http.StatusForbidden {
		t.Fatalf("运营停止应 403，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/1/stop", map[string]interface{}{"reason": "试点异常"}, auth("demo-token-approver")); code != http.StatusOK {
		t.Fatalf("停止应 200，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/1/restore", nil, auth("demo-token-approver")); code != http.StatusOK {
		t.Fatalf("恢复应 200，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/1/restore/complete", nil, auth("demo-token-approver")); code != http.StatusConflict {
		t.Fatalf("未确认恢复完成应 409，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/999/restore", nil, auth("demo-token-approver")); code != http.StatusNotFound {
		t.Fatalf("不存在活动恢复应 404，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/999/complete", nil, auth("demo-token-approver")); code != http.StatusNotFound {
		t.Fatalf("不存在活动结案应 404，实际 %d", code)
	}
}

func TestVehicleHTTPFlow(t *testing.T) {
	r := newTestRouter(t)
	code, resp := do(t, r, http.MethodGet, "/api/v1/vehicle/config", nil, map[string]string{"X-Vehicle-Token": "veh-token-05"})
	if code != http.StatusOK || len(resp.Data) == 0 {
		t.Fatalf("无活动时拉取应返回当前生效值，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/vehicle/receipts", "bad", map[string]string{"X-Vehicle-Token": "veh-token-01"}); code != http.StatusBadRequest {
		t.Fatalf("非法回执体应 400，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/vehicle/receipts", map[string]interface{}{"version": 7, "success": true}, map[string]string{"X-Vehicle-Token": "veh-token-01"}); code != http.StatusNotFound {
		t.Fatalf("未知版本应 404，实际 %d", code)
	}
}

func TestInvalidIDParameters(t *testing.T) {
	r := newTestRouter(t)
	cases := []struct {
		method, path, token string
		body                interface{}
	}{
		{http.MethodPost, "/api/v1/applications/abc/resubmit", "demo-token-applicant", map[string]interface{}{"reason": "x", "interval": 30}},
		{http.MethodPost, "/api/v1/applications/abc/decide", "demo-token-approver", map[string]interface{}{"approve": true}},
		{http.MethodPost, "/api/v1/campaigns/abc/pilot", "demo-token-operator", map[string]interface{}{"vehicles": []string{"TE-01", "TE-02"}}},
		{http.MethodPost, "/api/v1/campaigns/abc/promote", "demo-token-operator", nil},
		{http.MethodPost, "/api/v1/campaigns/abc/stop", "demo-token-approver", map[string]interface{}{"reason": "x"}},
		{http.MethodPost, "/api/v1/campaigns/abc/restore", "demo-token-approver", nil},
		{http.MethodPost, "/api/v1/campaigns/abc/complete", "demo-token-approver", nil},
		{http.MethodPost, "/api/v1/campaigns/abc/restore/complete", "demo-token-approver", nil},
		{http.MethodGet, "/api/v1/campaigns/abc", "", nil},
	}
	for _, c := range cases {
		var headers map[string]string
		if c.token != "" {
			headers = auth(c.token)
		}
		if code, _ := do(t, r, c.method, c.path, c.body, headers); code != http.StatusBadRequest {
			t.Fatalf("%s %s 应 400，实际 %d", c.method, c.path, code)
		}
	}
	// 停止活动缺少原因 → 400；非法请求体 → 400。
	do(t, r, http.MethodPost, "/api/v1/applications", map[string]interface{}{"reason": "排查抖动", "interval": 30}, auth("demo-token-applicant"))
	do(t, r, http.MethodPost, "/api/v1/applications/1/decide", map[string]interface{}{"approve": true}, auth("demo-token-approver"))
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/1/stop", "bad", auth("demo-token-approver")); code != http.StatusBadRequest {
		t.Fatalf("停止请求体非法应 400，实际 %d", code)
	}
	if code, _ := do(t, r, http.MethodPost, "/api/v1/campaigns/1/stop", map[string]interface{}{"reason": ""}, auth("demo-token-approver")); code != http.StatusBadRequest {
		t.Fatalf("停止无原因应 400，实际 %d", code)
	}
}

func TestQueryAndDemoEndpoints(t *testing.T) {
	r := newTestRouter(t)
	for _, path := range []string{"/api/v1/exceptions", "/api/v1/audits"} {
		if code, resp := do(t, r, http.MethodGet, path, nil, nil); code != http.StatusOK || len(resp.Data) == 0 {
			t.Fatalf("%s 应 200 且有数据，实际 %d", path, code)
		}
	}
	// 演示凭据接口只允许本机访问：回环地址 200，其他来源 403（防止匿名取得各角色令牌）。
	for _, path := range []string{"/api/v1/demo/accounts", "/api/v1/demo/vehicles"} {
		if code, resp := demoReq(t, r, path, "127.0.0.1:54321"); code != http.StatusOK || len(resp.Data) == 0 {
			t.Fatalf("%s 本机访问应 200，实际 %d", path, code)
		}
		if code, _ := demoReq(t, r, path, "10.1.2.3:5555"); code != http.StatusForbidden {
			t.Fatalf("%s 非本机来源应 403，实际 %d", path, code)
		}
	}
	_ = time.Now()
}

func demoReq(t *testing.T, r *gin.Engine, path, remote string) (int, apiResp) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remote
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out apiResp
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}
