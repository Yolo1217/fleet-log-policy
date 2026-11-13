// Package model 定义 Gorm 持久化结构，字段与题面「期望值/生效值分开保存」要求一一对应。
package model

import "time"

// Account 预置账号（含服务端会话令牌，令牌不参与前端提交）。
type Account struct {
	ID          uint   `gorm:"primaryKey"`
	Username    string `gorm:"uniqueIndex;size:64;not null"`
	DisplayName string `gorm:"size:64;not null"`
	Role        string `gorm:"size:32;not null"`
	Token       string `gorm:"uniqueIndex;size:128;not null"`
}

// TableName 固定表名，避免复数推断差异。
func (Account) TableName() string { return "account" }

// Vehicle 预置车辆与当前生效状态。
type Vehicle struct {
	ID              uint   `gorm:"primaryKey" json:"id"`
	Code            string `gorm:"uniqueIndex;size:32;not null" json:"code"`
	Name            string `gorm:"size:64;not null" json:"name"`
	Token           string `gorm:"uniqueIndex;size:128;not null" json:"token"`
	InitialInterval int    `gorm:"not null" json:"initial_interval"`
	AppliedInterval int    `gorm:"not null" json:"applied_interval"`
	AppliedVersion  int    `gorm:"not null;default:0" json:"applied_version"`
	LatestVersion   int    `gorm:"not null;default:0" json:"latest_version"`
}

// TableName 表名。
func (Vehicle) TableName() string { return "vehicle" }

// Application 配置申请，版本号随退回重提递增。
type Application struct {
	ID           uint      `gorm:"primaryKey"`
	ApplicantID  uint      `gorm:"index;not null"`
	ApproverID   uint      `gorm:"not null;default:0"`
	Reason       string    `gorm:"size:512;not null"`
	Interval     int       `gorm:"not null"`
	Status       string    `gorm:"index;size:32;not null"`
	VersionNo    int       `gorm:"not null;default:1"`
	RejectReason string    `gorm:"size:512"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// TableName 表名。
func (Application) TableName() string { return "application" }

// ApplicationVersion 申请历史版本（退回再提交保留）。
type ApplicationVersion struct {
	ID            uint      `gorm:"primaryKey"`
	ApplicationID uint      `gorm:"index;not null"`
	VersionNo     int       `gorm:"not null"`
	Reason        string    `gorm:"size:512;not null"`
	Interval      int       `gorm:"not null"`
	CreatedAt     time.Time
}

// TableName 表名。
func (ApplicationVersion) TableName() string { return "application_version" }

// Campaign 发布活动，同车队同时只允许一个未结束活动。
type Campaign struct {
	ID                uint       `gorm:"primaryKey"`
	ApplicationID     uint       `gorm:"index;not null"`
	Status            string     `gorm:"index;size:32;not null"`
	PilotVehicles     string     `gorm:"size:128;not null"` // 逗号分隔车辆编号
	FleetKey          string     `gorm:"index;size:32;not null"`
	PilotStartedAt    *time.Time
	StoppedAt         *time.Time
	StopReason        string `gorm:"size:512"`
	CompletedAt       *time.Time
	RestoreStartedAt  *time.Time
	RestoredCompleted *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// TableName 表名。
func (Campaign) TableName() string { return "campaign" }

// Release 每车发布版本：一次活动对每车最多两个版本（活动版本、恢复版本）。
type Release struct {
	ID             uint   `gorm:"primaryKey"`
	CampaignID     uint   `gorm:"index;not null"`
	VehicleID      uint   `gorm:"index:idx_vehicle_version,priority:1;not null"`
	VersionNo      int    `gorm:"index:idx_vehicle_version,priority:2;not null"`
	Kind           string `gorm:"size:16;not null"`
	TargetInterval int    `gorm:"not null"`
	PriorInterval  int    `gorm:"not null"`
	Status         string `gorm:"index;size:16;not null"`
	DispatchedAt   *time.Time
	DeadlineAt     *time.Time
	ReceiptAt      *time.Time
	ResultNote     string `gorm:"size:512"`
	LateSuccessAt  *time.Time
	Superseded     bool   `gorm:"not null;default:false"`
	CreatedAt      time.Time
}

// TableName 表名。
func (Release) TableName() string { return "vehicle_release" }

// Receipt 车端回执原始记录（幂等与审计依据）。
type Receipt struct {
	ID         uint   `gorm:"primaryKey"`
	ReleaseID  uint   `gorm:"index;not null"`
	VehicleID  uint   `gorm:"index;not null"`
	VersionNo  int    `gorm:"not null"`
	Success    bool   `gorm:"not null"`
	Reason     string `gorm:"size:512"`
	RequestKey string `gorm:"index;size:64"`
	Duplicate  bool   `gorm:"not null;default:false"`
	Ignored    bool   `gorm:"not null;default:false"`
	CreatedAt  time.Time
}

// TableName 表名。
func (Receipt) TableName() string { return "receipt" }

// ExceptionRecord 异常留痕（越权、跨车、未知版本、旧版本、重复回执等）。
type ExceptionRecord struct {
	ID         uint   `gorm:"primaryKey"`
	Kind       string `gorm:"index;size:32;not null"`
	VehicleID  uint   `gorm:"not null;default:0"`
	CampaignID uint   `gorm:"not null;default:0"`
	Detail     string `gorm:"size:512;not null"`
	CreatedAt  time.Time
}

// TableName 表名。
func (ExceptionRecord) TableName() string { return "exception_record" }

// AuditLog 操作审计。
type AuditLog struct {
	ID        uint   `gorm:"primaryKey"`
	Actor     string `gorm:"size:64;not null"`
	Role      string `gorm:"size:32;not null"`
	Action    string `gorm:"size:64;not null"`
	Target    string `gorm:"size:64;not null"`
	Detail    string `gorm:"size:512"`
	CreatedAt time.Time
}

// TableName 表名。
func (AuditLog) TableName() string { return "audit_log" }
