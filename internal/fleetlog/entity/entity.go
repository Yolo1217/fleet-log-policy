// Package entity 定义题目 09 的领域常量与对外视图结构，不依赖持久化实现。
package entity

import "time"

// 角色。
const (
	RoleApplicant = "applicant" // 研发申请人
	RoleApprover  = "approver"  // 审批负责人
	RoleOperator  = "operator"  // 发布运营
)

// 申请状态。
const (
	ApplicationWaiting  = "waiting"  // 待审批
	ApplicationApproved = "approved" // 已批准
	ApplicationRejected = "rejected" // 已退回
)

// 活动状态。
const (
	CampaignApproved   = "approved"   // 已批准未发布
	CampaignPiloting   = "piloting"   // 试点中
	CampaignPilotOK    = "pilot_ok"   // 试点成功
	CampaignPaused     = "paused"     // 已暂停（试点失败/超时）
	CampaignPromoting  = "promoting"  // 推广中
	CampaignPendingEnd = "pending_end"// 待结案
	CampaignCompleted  = "completed"  // 已完成
	CampaignStopped    = "stopped"    // 已停止
	CampaignRestoring  = "restoring"  // 恢复中
	CampaignRestored   = "restored"   // 已恢复
)

// 发布版本状态（期望侧）。
const (
	ReleaseDispatched = "dispatched" // 已下发待回执
	ReleaseSucceeded  = "succeeded"  // 回执成功（已生效）
	ReleaseFailed     = "failed"     // 回执失败（生效值不变）
	ReleaseTimeout    = "timeout"    // 超时未回执
)

// 发布类型。
const (
	KindActivity = "activity" // 活动版本
	KindRecovery = "recovery" // 恢复版本
)

// 异常记录类型（题面边界 1～15 的可查留痕）。
const (
	ExcSelfApproval   = "self_approval"    // 申请人=审批人
	ExcOpenCampaign   = "open_campaign"    // 同车队已有未结束活动
	ExcIllegalPromote = "illegal_promote"  // 试点未完成即推广
	ExcCrossVehicle   = "cross_vehicle"    // 跨车回执
	ExcUnknownVersion = "unknown_version"  // 未知版本回执
	ExcOldVersion     = "old_version"      // 旧版本迟到回执
	ExcDuplicate      = "duplicate_receipt"// 重复回执
	ExcUnauthorized   = "unauthorized"     // 未鉴权/越权
	ExcForbidden      = "forbidden"        // 角色越权
	ExcPullDenied     = "pull_denied"      // 非法拉取
)

// DomainError 是服务层返回的业务错误，Code 用于 HTTP 层映射。
type DomainError struct {
	Code string
	Msg  string
}

func (e *DomainError) Error() string { return e.Code + ": " + e.Msg }

// Err 构造业务错误。
func Err(code, msg string) *DomainError { return &DomainError{Code: code, Msg: msg} }

// Account 是预置账号。
type Account struct {
	ID          uint
	Username    string
	DisplayName string
	Role        string
}

// Vehicle 是预置车辆及其当前生效状态。
type Vehicle struct {
	ID              uint
	Code            string
	Name            string
	InitialInterval int // 活动前设置（预置 60 秒）
	AppliedInterval int // 已生效值
	AppliedVersion  int // 已生效版本（0 表示从未生效过活动/恢复版本）
	LatestVersion   int // 每车版本单调递增游标
}

// VehicleRow 是逐车核查行，期望值与生效值分开保存。
type VehicleRow struct {
	Code            string     `json:"code"`
	Name            string     `json:"name"`
	PreActivity     int        `json:"pre_activity_interval"`
	ExpectedVersion int        `json:"expected_version"`
	Expected        int        `json:"expected_interval"`
	AppliedVersion  int        `json:"applied_version"`
	Applied         int        `json:"applied_interval"`
	Status          string     `json:"status"` // 待下发/已下发/成功/失败/超时/未确认
	Reason          string     `json:"reason"`
	DispatchedAt    *time.Time `json:"dispatched_at,omitempty"`
	ReceiptAt       *time.Time `json:"receipt_at,omitempty"`
	TimedOut        bool       `json:"timed_out"`
	LateSuccess     bool       `json:"late_success"`
	ReleaseKind     string     `json:"release_kind,omitempty"`
}

// CampaignView 是活动总览。
type CampaignView struct {
	ID            uint         `json:"id"`
	ApplicationID uint         `json:"application_id"`
	Status        string       `json:"status"`
	StatusText    string       `json:"status_text"`
	PilotVehicles []string     `json:"pilot_vehicles"`
	Interval      int          `json:"interval"`
	Reason        string       `json:"reason"`
	Vehicles      []VehicleRow `json:"vehicles"`
	Dispatched    []string     `json:"dispatched_vehicles"`
	Undispatched  []string     `json:"undispatched_vehicles"`
	RecoveryRows  []VehicleRow `json:"recovery_rows"`
	StopReason    string       `json:"stop_reason,omitempty"`
	AllSucceeded  bool         `json:"all_succeeded"`
	AllRecovered  bool         `json:"all_recovered"`
}

// ApplicationView 是申请详情。
type ApplicationView struct {
	ID              uint             `json:"id"`
	Applicant       string           `json:"applicant"`
	ApplicantRole   string           `json:"applicant_role"`
	Reason          string           `json:"reason"`
	Interval        int              `json:"interval"`
	Status          string           `json:"status"`
	StatusText      string           `json:"status_text"`
	VersionNo       int              `json:"version_no"`
	Approver        string           `json:"approver,omitempty"`
	History         []ApplicationVer `json:"history"`
	CreatedAt       time.Time        `json:"created_at"`
	CampaignID      uint             `json:"campaign_id,omitempty"`
	CampaignStatus  string           `json:"campaign_status,omitempty"`
}

// ApplicationVer 是申请历史版本。
type ApplicationVer struct {
	VersionNo int       `json:"version_no"`
	Reason    string    `json:"reason"`
	Interval  int       `json:"interval"`
	CreatedAt time.Time `json:"created_at"`
}

// ExceptionView 是异常留痕行。
type ExceptionView struct {
	ID        uint      `json:"id"`
	Kind      string    `json:"kind"`
	Vehicle   string    `json:"vehicle,omitempty"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// StatusText 把活动状态转为中文展示。
func StatusText(status string) string {
	switch status {
	case CampaignApproved:
		return "已批准（未发布）"
	case CampaignPiloting:
		return "试点中"
	case CampaignPilotOK:
		return "试点成功"
	case CampaignPaused:
		return "已暂停"
	case CampaignPromoting:
		return "推广中"
	case CampaignPendingEnd:
		return "待结案"
	case CampaignCompleted:
		return "已完成"
	case CampaignStopped:
		return "已停止"
	case CampaignRestoring:
		return "恢复中"
	case CampaignRestored:
		return "已恢复"
	}
	return status
}

// ApplicationStatusText 把申请状态转为中文展示。
func ApplicationStatusText(status string) string {
	switch status {
	case ApplicationWaiting:
		return "待审批"
	case ApplicationApproved:
		return "已批准"
	case ApplicationRejected:
		return "已退回"
	}
	return status
}

// OpenCampaignStatuses 是「未结束」活动集合：同车队同时只允许一个。
var OpenCampaignStatuses = []string{
	CampaignApproved, CampaignPiloting, CampaignPilotOK, CampaignPaused,
	CampaignPromoting, CampaignPendingEnd, CampaignRestoring,
}

// IsOpenCampaign 判断活动是否未结束。
func IsOpenCampaign(status string) bool {
	for _, s := range OpenCampaignStatuses {
		if s == status {
			return true
		}
	}
	return false
}
