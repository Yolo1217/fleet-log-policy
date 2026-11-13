// Package service 实现题目 09 的业务规则：申请审批、试点推广、逐车版本、回执与恢复。
package service

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"fleet-log-policy/internal/fleetlog/entity"
	"fleet-log-policy/internal/fleetlog/model"
	"fleet-log-policy/internal/fleetlog/repo"
)

const fleetKey = "TE-FLEET-1"

// Service 是题目 09 的业务服务。互斥锁保证状态迁移在并发请求下串行，数据库为主要事实来源。
type Service struct {
	store   *repo.Store
	timeout time.Duration
	mu      sync.Mutex
	now     func() time.Time
}

// New 创建服务；timeoutSeconds 为教学超时阈值（题面 60 秒，演示可缩短）。
func New(store *repo.Store, timeoutSeconds int) *Service {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 60
	}
	return &Service{store: store, timeout: time.Duration(timeoutSeconds) * time.Second, now: time.Now}
}

// TimeoutSeconds 返回当前教学超时阈值（秒），用于页面注明。
func (s *Service) TimeoutSeconds() int { return int(s.timeout / time.Second) }

// Tick 扫描超时并推进活动状态；由后台循环与每次业务调用前触发。
func (s *Service) Tick() error {
	if _, err := s.store.MarkTimeouts(s.now()); err != nil {
		return err
	}
	return s.reconcileCampaigns()
}

// reconcileCampaigns 根据逐车结果推进活动状态（不覆盖终态）。
func (s *Service) reconcileCampaigns() error {
	campaigns, err := s.campaigns()
	if err != nil {
		return err
	}
	vehicles, err := s.store.Vehicles()
	if err != nil {
		return err
	}
	for _, c := range campaigns {
		if !entity.IsOpenCampaign(c.Status) {
			continue
		}
		releases, err := s.store.ReleasesByCampaign(c.ID)
		if err != nil {
			return err
		}
		activity := filterKind(releases, entity.KindActivity)
		switch c.Status {
		case entity.CampaignPiloting:
			pilot := pickByCodes(activity, vehicles, repo.SplitCSV(c.PilotVehicles))
			if allStatus(pilot, entity.ReleaseSucceeded) {
				c.Status = entity.CampaignPilotOK
			} else if anyStatus(pilot, entity.ReleaseTimeout) || anyStatus(pilot, entity.ReleaseFailed) {
				c.Status = entity.CampaignPaused
			}
		case entity.CampaignPromoting:
			if len(activity) == 5 && allStatus(activity, entity.ReleaseSucceeded) {
				c.Status = entity.CampaignPendingEnd
			}
		}
		if err := s.store.UpdateCampaign(nil, &c); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) campaigns() ([]model.Campaign, error) {
	var list []model.Campaign
	err := s.store.DB.Order("id asc").Find(&list).Error
	return list, err
}

// ---------------- 申请 ----------------

// CreateApplication 创建申请（规则 1：原因、5 台车、10～300 秒整数）。
func (s *Service) CreateApplication(actor model.Account, reason string, interval int) (entity.ApplicationView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actor.Role != entity.RoleApplicant {
		s.exception(entity.ExcForbidden, 0, 0, "非申请人角色尝试创建申请: "+actor.Username)
		return entity.ApplicationView{}, entity.Err("forbidden", "只有研发申请人可以创建申请")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return entity.ApplicationView{}, entity.Err("invalid_argument", "排查原因必填")
	}
	if interval < 10 || interval > 300 {
		return entity.ApplicationView{}, entity.Err("invalid_argument", "日志上报间隔必须为 10～300 的整数秒")
	}
	app := model.Application{ApplicantID: actor.ID, Reason: reason, Interval: interval, Status: entity.ApplicationWaiting, VersionNo: 1}
	if err := s.store.CreateApplication(nil, &app); err != nil {
		return entity.ApplicationView{}, err
	}
	_ = s.store.AddAudit(nil, actor.Username, actor.Role, "create_application", fmt.Sprintf("app:%d", app.ID), reason)
	return s.applicationView(&app)
}

// Resubmit 退回后修改再提交（保留历史版本，规则 1）。
func (s *Service) Resubmit(actor model.Account, appID uint, reason string, interval int) (entity.ApplicationView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	app, err := s.store.Application(appID)
	if err != nil {
		return entity.ApplicationView{}, entity.Err("not_found", "申请不存在")
	}
	if app.ApplicantID != actor.ID {
		s.exception(entity.ExcForbidden, 0, 0, "非本人申请尝试修改")
		return entity.ApplicationView{}, entity.Err("forbidden", "只能修改本人申请")
	}
	if app.Status != entity.ApplicationRejected {
		return entity.ApplicationView{}, entity.Err("conflict", "只有已退回的申请可以修改再提交")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return entity.ApplicationView{}, entity.Err("invalid_argument", "排查原因必填")
	}
	if interval < 10 || interval > 300 {
		return entity.ApplicationView{}, entity.Err("invalid_argument", "日志上报间隔必须为 10～300 的整数秒")
	}
	app.Reason, app.Interval = reason, interval
	app.VersionNo++
	app.Status = entity.ApplicationWaiting
	app.RejectReason = ""
	if err := s.store.UpdateApplication(nil, &app); err != nil {
		return entity.ApplicationView{}, err
	}
	if err := s.store.AddApplicationVersion(nil, &model.ApplicationVersion{
		ApplicationID: app.ID, VersionNo: app.VersionNo, Reason: reason, Interval: interval,
	}); err != nil {
		return entity.ApplicationView{}, err
	}
	_ = s.store.AddAudit(nil, actor.Username, actor.Role, "resubmit_application", fmt.Sprintf("app:%d", app.ID), reason)
	return s.applicationView(&app)
}

// Decide 负责人批准或退回；审批人不能是申请人（规则 1、边界 1）。
func (s *Service) Decide(actor model.Account, appID uint, approve bool, rejectReason string) (entity.ApplicationView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actor.Role != entity.RoleApprover {
		s.exception(entity.ExcForbidden, 0, 0, "非负责人角色尝试审批: "+actor.Username)
		return entity.ApplicationView{}, entity.Err("forbidden", "只有审批负责人可以审批")
	}
	app, err := s.store.Application(appID)
	if err != nil {
		return entity.ApplicationView{}, entity.Err("not_found", "申请不存在")
	}
	if app.ApplicantID == actor.ID {
		s.exception(entity.ExcSelfApproval, 0, 0, fmt.Sprintf("负责人审批本人申请 app:%d", app.ID))
		return entity.ApplicationView{}, entity.Err("forbidden", "审批人不能是申请人")
	}
	if app.Status != entity.ApplicationWaiting {
		return entity.ApplicationView{}, entity.Err("conflict", "该申请不处于待审批状态")
	}
	if !approve {
		app.Status = entity.ApplicationRejected
		app.RejectReason = strings.TrimSpace(rejectReason)
		if app.RejectReason == "" {
			return entity.ApplicationView{}, entity.Err("invalid_argument", "退回必须填写原因")
		}
		if err := s.store.UpdateApplication(nil, &app); err != nil {
			return entity.ApplicationView{}, err
		}
		_ = s.store.AddAudit(nil, actor.Username, actor.Role, "reject_application", fmt.Sprintf("app:%d", app.ID), app.RejectReason)
		return s.applicationView(&app)
	}
	if _, err := s.store.OpenCampaign(nil, fleetKey, entity.OpenCampaignStatuses); err == nil {
		s.exception(entity.ExcOpenCampaign, 0, 0, "同车队已存在未结束活动，拒绝再次发起")
		return entity.ApplicationView{}, entity.Err("conflict", "同车队同时只允许一个未结束发布活动")
	} else if err != repo.ErrNotFound {
		return entity.ApplicationView{}, err
	}
	err = s.store.DB.Transaction(func(tx *gorm.DB) error {
		app.Status = entity.ApplicationApproved
		app.ApproverID = actor.ID
		if err := s.store.UpdateApplication(tx, &app); err != nil {
			return err
		}
		pilots := defaultPilots()
		return s.store.CreateCampaign(tx, &model.Campaign{
			ApplicationID: app.ID, Status: entity.CampaignApproved,
			PilotVehicles: pilots, FleetKey: fleetKey,
		})
	})
	if err != nil {
		return entity.ApplicationView{}, err
	}
	_ = s.store.AddAudit(nil, actor.Username, actor.Role, "approve_application", fmt.Sprintf("app:%d", app.ID), "批准并生成活动")
	return s.applicationView(&app)
}

// ---------------- 发布 ----------------

// StartPilot 运营选择 2 台试点车下发（规则 2、3）。
func (s *Service) StartPilot(actor model.Account, campaignID uint, codes []string) (entity.CampaignView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actor.Role != entity.RoleOperator {
		s.exception(entity.ExcForbidden, 0, campaignID, "非运营角色尝试试点发布: "+actor.Username)
		return entity.CampaignView{}, entity.Err("forbidden", "只有发布运营可以执行发布")
	}
	if err := s.Tick(); err != nil {
		return entity.CampaignView{}, err
	}
	c, err := s.store.Campaign(campaignID)
	if err != nil {
		return entity.CampaignView{}, entity.Err("not_found", "活动不存在")
	}
	if c.Status != entity.CampaignApproved {
		return entity.CampaignView{}, entity.Err("conflict", "活动当前状态不允许发起试点")
	}
	if len(codes) != 2 {
		return entity.CampaignView{}, entity.Err("invalid_argument", "试点必须选择 2 台车辆")
	}
	vehicles, err := s.vehiclesByCodes(codes)
	if err != nil {
		return entity.CampaignView{}, err
	}
	app, err := s.store.Application(c.ApplicationID)
	if err != nil {
		return entity.CampaignView{}, err
	}
	now := s.now()
	err = s.store.DB.Transaction(func(tx *gorm.DB) error {
		for i := range vehicles {
			v := vehicles[i]
			if err := s.dispatch(tx, &c, &v, entity.KindActivity, app.Interval, now); err != nil {
				return err
			}
		}
		c.Status = entity.CampaignPiloting
		c.PilotVehicles = strings.Join(codes, ",")
		c.PilotStartedAt = &now
		return s.store.UpdateCampaign(tx, &c)
	})
	if err != nil {
		return entity.CampaignView{}, err
	}
	_ = s.store.AddAudit(nil, actor.Username, actor.Role, "start_pilot", fmt.Sprintf("campaign:%d", c.ID), strings.Join(codes, ","))
	return s.campaignView(&c)
}

// Promote 确认推广剩余 3 台；试点未完成一律拒绝并留痕（规则 3、边界 4）。
func (s *Service) Promote(actor model.Account, campaignID uint) (entity.CampaignView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actor.Role != entity.RoleOperator {
		s.exception(entity.ExcForbidden, 0, campaignID, "非运营角色尝试推广")
		return entity.CampaignView{}, entity.Err("forbidden", "只有发布运营可以执行推广")
	}
	if err := s.Tick(); err != nil {
		return entity.CampaignView{}, err
	}
	c, err := s.store.Campaign(campaignID)
	if err != nil {
		return entity.CampaignView{}, entity.Err("not_found", "活动不存在")
	}
	if c.Status != entity.CampaignPilotOK {
		s.exception(entity.ExcIllegalPromote, 0, c.ID, "试点未完成即尝试推广，状态="+c.Status)
		return entity.CampaignView{}, entity.Err("conflict", "两台试点车均成功后才可推广，当前不可推广")
	}
	app, err := s.store.Application(c.ApplicationID)
	if err != nil {
		return entity.CampaignView{}, err
	}
	vehicles, err := s.store.Vehicles()
	if err != nil {
		return entity.CampaignView{}, err
	}
	pilot := map[string]bool{}
	for _, code := range repo.SplitCSV(c.PilotVehicles) {
		pilot[code] = true
	}
	now := s.now()
	err = s.store.DB.Transaction(func(tx *gorm.DB) error {
		for i := range vehicles {
			if pilot[vehicles[i].Code] {
				continue
			}
			v := vehicles[i]
			if err := s.dispatch(tx, &c, &v, entity.KindActivity, app.Interval, now); err != nil {
				return err
			}
		}
		c.Status = entity.CampaignPromoting
		return s.store.UpdateCampaign(tx, &c)
	})
	if err != nil {
		return entity.CampaignView{}, err
	}
	_ = s.store.AddAudit(nil, actor.Username, actor.Role, "promote", fmt.Sprintf("campaign:%d", c.ID), "推广剩余 3 台")
	return s.campaignView(&c)
}

// Stop 负责人停止活动：只阻止未发布部分，保留已发与生效记录（规则 7）。
func (s *Service) Stop(actor model.Account, campaignID uint, reason string) (entity.CampaignView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actor.Role != entity.RoleApprover {
		s.exception(entity.ExcForbidden, 0, campaignID, "非负责人角色尝试停止活动")
		return entity.CampaignView{}, entity.Err("forbidden", "只有审批负责人可以停止活动")
	}
	if err := s.Tick(); err != nil {
		return entity.CampaignView{}, err
	}
	c, err := s.store.Campaign(campaignID)
	if err != nil {
		return entity.CampaignView{}, entity.Err("not_found", "活动不存在")
	}
	switch c.Status {
	case entity.CampaignApproved, entity.CampaignPiloting, entity.CampaignPilotOK,
		entity.CampaignPaused, entity.CampaignPromoting, entity.CampaignPendingEnd:
	default:
		return entity.CampaignView{}, entity.Err("conflict", "活动当前状态不允许停止")
	}
	if strings.TrimSpace(reason) == "" {
		return entity.CampaignView{}, entity.Err("invalid_argument", "停止活动必须填写原因")
	}
	now := s.now()
	c.Status = entity.CampaignStopped
	c.StoppedAt = &now
	c.StopReason = reason
	if err := s.store.UpdateCampaign(nil, &c); err != nil {
		return entity.CampaignView{}, err
	}
	_ = s.store.AddAudit(nil, actor.Username, actor.Role, "stop_campaign", fmt.Sprintf("campaign:%d", c.ID), reason)
	return s.campaignView(&c)
}

// ApproveRestore 负责人批准恢复：仅对曾下发过活动版本的车辆创建更高版本，目标为活动前值（规则 7）。
func (s *Service) ApproveRestore(actor model.Account, campaignID uint) (entity.CampaignView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actor.Role != entity.RoleApprover {
		s.exception(entity.ExcForbidden, 0, campaignID, "非负责人角色尝试批准恢复")
		return entity.CampaignView{}, entity.Err("forbidden", "只有审批负责人可以批准恢复")
	}
	if err := s.Tick(); err != nil {
		return entity.CampaignView{}, err
	}
	c, err := s.store.Campaign(campaignID)
	if err != nil {
		return entity.CampaignView{}, entity.Err("not_found", "活动不存在")
	}
	if c.Status != entity.CampaignStopped {
		return entity.CampaignView{}, entity.Err("conflict", "只有已停止的活动可以批准恢复")
	}
	releases, err := s.store.ReleasesByCampaign(c.ID)
	if err != nil {
		return entity.CampaignView{}, err
	}
	activity := filterKind(releases, entity.KindActivity)
	if len(activity) == 0 {
		return entity.CampaignView{}, entity.Err("conflict", "没有已下发的车辆，无需恢复")
	}
	now := s.now()
	err = s.store.DB.Transaction(func(tx *gorm.DB) error {
		for i := range activity {
			rel := activity[i]
			v, err := s.store.VehicleByID(rel.VehicleID)
			if err != nil {
				return err
			}
			target := rel.PriorInterval
			if target == 0 {
				target = v.InitialInterval
			}
			if err := s.dispatch(tx, &c, &v, entity.KindRecovery, target, now); err != nil {
				return err
			}
		}
		c.Status = entity.CampaignRestoring
		c.RestoreStartedAt = &now
		return s.store.UpdateCampaign(tx, &c)
	})
	if err != nil {
		return entity.CampaignView{}, err
	}
	_ = s.store.AddAudit(nil, actor.Username, actor.Role, "approve_restore", fmt.Sprintf("campaign:%d", c.ID), "为曾发布车辆创建恢复版本")
	return s.campaignView(&c)
}

// Complete 负责人记为完成：全部 5 台目标车确认成功（规则 8）。
func (s *Service) Complete(actor model.Account, campaignID uint) (entity.CampaignView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actor.Role != entity.RoleApprover {
		return entity.CampaignView{}, entity.Err("forbidden", "只有审批负责人可以结案")
	}
	if err := s.Tick(); err != nil {
		return entity.CampaignView{}, err
	}
	c, err := s.store.Campaign(campaignID)
	if err != nil {
		return entity.CampaignView{}, entity.Err("not_found", "活动不存在")
	}
	if c.Status != entity.CampaignPromoting && c.Status != entity.CampaignPendingEnd {
		return entity.CampaignView{}, entity.Err("conflict", "活动当前状态不允许结案")
	}
	ok, err := s.allActivitySucceeded(c.ID)
	if err != nil {
		return entity.CampaignView{}, err
	}
	if !ok {
		return entity.CampaignView{}, entity.Err("conflict", "仍有未确认或失败车辆，不能记为完成")
	}
	now := s.now()
	c.Status = entity.CampaignCompleted
	c.CompletedAt = &now
	if err := s.store.UpdateCampaign(nil, &c); err != nil {
		return entity.CampaignView{}, err
	}
	_ = s.store.AddAudit(nil, actor.Username, actor.Role, "complete_campaign", fmt.Sprintf("campaign:%d", c.ID), "全部目标车确认成功")
	return s.campaignView(&c)
}

// CompleteRestore 负责人记为恢复完成：所有需恢复车辆均确认（有未确认项不得显示全部恢复）。
func (s *Service) CompleteRestore(actor model.Account, campaignID uint) (entity.CampaignView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actor.Role != entity.RoleApprover {
		return entity.CampaignView{}, entity.Err("forbidden", "只有审批负责人可以确认恢复完成")
	}
	if err := s.Tick(); err != nil {
		return entity.CampaignView{}, err
	}
	c, err := s.store.Campaign(campaignID)
	if err != nil {
		return entity.CampaignView{}, entity.Err("not_found", "活动不存在")
	}
	if c.Status != entity.CampaignRestoring {
		return entity.CampaignView{}, entity.Err("conflict", "活动不在恢复中")
	}
	releases, err := s.store.ReleasesByCampaign(c.ID)
	if err != nil {
		return entity.CampaignView{}, err
	}
	recovery := filterKind(releases, entity.KindRecovery)
	if len(recovery) == 0 || !allStatus(recovery, entity.ReleaseSucceeded) {
		return entity.CampaignView{}, entity.Err("conflict", "仍有未确认的恢复车辆，不能标记全部恢复")
	}
	now := s.now()
	c.Status = entity.CampaignRestored
	c.RestoredCompleted = &now
	if err := s.store.UpdateCampaign(nil, &c); err != nil {
		return entity.CampaignView{}, err
	}
	_ = s.store.AddAudit(nil, actor.Username, actor.Role, "complete_restore", fmt.Sprintf("campaign:%d", c.ID), "全部需恢复车辆确认")
	return s.campaignView(&c)
}

// ---------------- 车端 ----------------

// PullResult 是车辆拉取结果。
type PullResult struct {
	Vehicle  string `json:"vehicle"`
	Version  int    `json:"version"`
	Interval int    `json:"interval"`
	Status   string `json:"status"`
	Note     string `json:"note"`
}

// Pull 车辆只取得面向自己的最新已发布版本（规则 4、边界 5）。
func (s *Service) Pull(v model.Vehicle) (PullResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Tick(); err != nil {
		return PullResult{}, err
	}
	rel, err := s.store.LatestReleaseByVehicle(v.ID)
	if err != nil {
		v2, verr := s.store.VehicleByID(v.ID)
		if verr != nil {
			return PullResult{}, verr
		}
		return PullResult{Vehicle: v2.Code, Version: v2.AppliedVersion, Interval: v2.AppliedInterval,
			Status: "当前生效", Note: "该车暂无活动版本，返回当前生效设置"}, nil
	}
	return PullResult{
		Vehicle: v.Code, Version: rel.VersionNo, Interval: rel.TargetInterval,
		Status: "期望值", Note: describeRelease(rel),
	}, nil
}

// ReceiptResult 是回执处理结果。
type ReceiptResult struct {
	Accepted bool   `json:"accepted"`
	Duplicate bool  `json:"duplicate"`
	Ignored  bool   `json:"ignored"`
	Message  string `json:"message"`
}

// Receipt 处理车端回执：鉴权、版本匹配、幂等、旧版本不覆盖新生效值（规则 5、6）。
func (s *Service) Receipt(v model.Vehicle, version int, success bool, reason, requestKey string) (ReceiptResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Tick(); err != nil {
		return ReceiptResult{}, err
	}
	rel, err := s.store.ReleaseByVehicleVersion(v.ID, version)
	if err != nil {
		if err == repo.ErrNotFound {
			// 若该版本号属于其他车辆，判定为跨车回执；否则为未知版本。
			others, _ := s.store.ReleasesByVersion(version)
			if len(others) > 0 {
				s.exception(entity.ExcCrossVehicle, v.ID, others[0].CampaignID,
					fmt.Sprintf("%s 回执他车版本 v%d（该版本属于其他车辆）", v.Code, version))
				return ReceiptResult{}, entity.Err("forbidden", "跨车回执：该版本不属于本车，已拒绝并记录")
			}
			s.exception(entity.ExcUnknownVersion, v.ID, 0, fmt.Sprintf("%s 回执未知版本 v%d", v.Code, version))
			return ReceiptResult{}, entity.Err("not_found", "未知版本：该车没有对应的已发布版本")
		}
		return ReceiptResult{}, err
	}
	latest, err := s.store.LatestReleaseByVehicle(v.ID)
	if err != nil {
		return ReceiptResult{}, err
	}
	now := s.now()
	res := ReceiptResult{Accepted: true}
	rec := &model.Receipt{ReleaseID: rel.ID, VehicleID: v.ID, VersionNo: version, Success: success, Reason: reason, RequestKey: requestKey}
	switch {
	case rel.Status == entity.ReleaseSucceeded:
		rec.Duplicate = true
		res.Duplicate = true
		res.Message = "重复回执：该版本已生效，不重复记账"
		_ = s.store.CreateReceipt(nil, rec)
		s.exception(entity.ExcDuplicate, v.ID, rel.CampaignID, fmt.Sprintf("%s 重复回执 v%d", v.Code, version))
		return res, nil
	case rel.VersionNo < latest.VersionNo:
		rec.Ignored = true
		res.Ignored = true
		res.Message = "旧版本回执：不覆盖新生效值，已保留审计"
		_ = s.store.CreateReceipt(nil, rec)
		s.exception(entity.ExcOldVersion, v.ID, rel.CampaignID, fmt.Sprintf("%s 旧版本回执 v%d（最新 v%d）", v.Code, version, latest.VersionNo))
		return res, nil
	case !success:
		rel.Status = entity.ReleaseFailed
		rel.ReceiptAt = &now
		rel.ResultNote = reason
		if err := s.store.SaveRelease(nil, &rel); err != nil {
			return ReceiptResult{}, err
		}
		_ = s.store.CreateReceipt(nil, rec)
		res.Message = "回执失败：生效值保持不变"
		_ = s.store.AddAudit(nil, v.Code, "vehicle", "receipt_failed", fmt.Sprintf("release:%d", rel.ID), reason)
		return res, nil
	}
	// 成功回执：更新生效值（含超时后的迟到成功，保留超时历史）。
	wasTimeout := rel.Status == entity.ReleaseTimeout
	rel.Status = entity.ReleaseSucceeded
	rel.ReceiptAt = &now
	rel.ResultNote = ""
	if wasTimeout {
		rel.LateSuccessAt = &now
	}
	vehicle, err := s.store.VehicleByID(v.ID)
	if err != nil {
		return ReceiptResult{}, err
	}
	err = s.store.DB.Transaction(func(tx *gorm.DB) error {
		if err := s.store.SaveRelease(tx, &rel); err != nil {
			return err
		}
		vehicle.AppliedInterval = rel.TargetInterval
		vehicle.AppliedVersion = rel.VersionNo
		if err := s.store.SaveVehicle(tx, &vehicle); err != nil {
			return err
		}
		return s.store.CreateReceipt(tx, rec)
	})
	if err != nil {
		return ReceiptResult{}, err
	}
	if wasTimeout {
		res.Message = "超时后迟到成功：已更新该车结果并保留超时历史"
	} else {
		res.Message = "回执成功：生效值已更新"
	}
	_ = s.store.AddAudit(nil, v.Code, "vehicle", "receipt_success", fmt.Sprintf("release:%d", rel.ID), res.Message)
	return res, nil
}

// ---------------- 查询视图 ----------------

// ApplicationView 返回申请详情。
func (s *Service) ApplicationView(id uint) (entity.ApplicationView, error) {
	app, err := s.store.Application(id)
	if err != nil {
		return entity.ApplicationView{}, entity.Err("not_found", "申请不存在")
	}
	return s.applicationView(&app)
}

// ApplicationList 返回全部申请。
func (s *Service) ApplicationList() ([]entity.ApplicationView, error) {
	apps, err := s.store.Applications()
	if err != nil {
		return nil, err
	}
	out := make([]entity.ApplicationView, 0, len(apps))
	for i := range apps {
		view, err := s.applicationView(&apps[i])
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}

// CampaignView 返回活动详情（含逐车核查与恢复行）。
func (s *Service) CampaignView(id uint) (entity.CampaignView, error) {
	if err := s.Tick(); err != nil {
		return entity.CampaignView{}, err
	}
	c, err := s.store.Campaign(id)
	if err != nil {
		return entity.CampaignView{}, entity.Err("not_found", "活动不存在")
	}
	return s.campaignView(&c)
}

// LatestCampaignView 返回最新活动；不存在时返回空视图。
func (s *Service) LatestCampaignView() (entity.CampaignView, error) {
	if err := s.Tick(); err != nil {
		return entity.CampaignView{}, err
	}
	c, err := s.store.LatestCampaign()
	if err == repo.ErrNotFound {
		return entity.CampaignView{}, nil
	}
	if err != nil {
		return entity.CampaignView{}, err
	}
	return s.campaignView(&c)
}

// Exceptions 返回异常留痕。
func (s *Service) Exceptions() ([]entity.ExceptionView, error) {
	list, err := s.store.Exceptions(100)
	if err != nil {
		return nil, err
	}
	out := make([]entity.ExceptionView, 0, len(list))
	for _, e := range list {
		code := ""
		if e.VehicleID != 0 {
			if v, err := s.store.VehicleByID(e.VehicleID); err == nil {
				code = v.Code
			}
		}
		out = append(out, entity.ExceptionView{ID: e.ID, Kind: e.Kind, Vehicle: code, Detail: e.Detail, CreatedAt: e.CreatedAt})
	}
	return out, nil
}

// AuditList 返回审计日志。
func (s *Service) AuditList() ([]model.AuditLog, error) {
	return s.store.Audits(200)
}

// Accounts 返回预置账号（供演示页切换角色）。
func (s *Service) Accounts() ([]model.Account, error) {
	return s.store.Accounts()
}

// AccountByToken 按会话令牌解析账号（鉴权中间件使用）。
func (s *Service) AccountByToken(token string) (model.Account, error) {
	return s.store.AccountByToken(token)
}

// VehicleByToken 按车辆凭证解析车辆。
func (s *Service) VehicleByToken(token string) (model.Vehicle, error) {
	return s.store.VehicleByToken(token)
}

// RecordException 由适配层记录鉴权类异常。
func (s *Service) RecordException(kind, detail string) {
	s.exception(kind, 0, 0, detail)
}

// Vehicles 返回车辆列表（下拉选择用）。
func (s *Service) Vehicles() ([]model.Vehicle, error) {
	return s.store.Vehicles()
}

// ---------------- 内部实现 ----------------

func (s *Service) dispatch(tx *gorm.DB, c *model.Campaign, v *model.Vehicle, kind string, target int, now time.Time) error {
	version := v.LatestVersion + 1
	deadline := now.Add(s.timeout)
	rel := &model.Release{
		CampaignID: c.ID, VehicleID: v.ID, VersionNo: version, Kind: kind,
		TargetInterval: target, PriorInterval: v.AppliedInterval,
		Status: entity.ReleaseDispatched, DispatchedAt: &now, DeadlineAt: &deadline,
	}
	if err := s.store.CreateRelease(tx, rel); err != nil {
		return err
	}
	v.LatestVersion = version
	return s.store.SaveVehicle(tx, v)
}

func (s *Service) allActivitySucceeded(campaignID uint) (bool, error) {
	releases, err := s.store.ReleasesByCampaign(campaignID)
	if err != nil {
		return false, err
	}
	activity := filterKind(releases, entity.KindActivity)
	if len(activity) != 5 || !allStatus(activity, entity.ReleaseSucceeded) {
		return false, nil
	}
	vehicles, err := s.store.Vehicles()
	if err != nil {
		return false, err
	}
	byID := map[uint]int{}
	for _, r := range activity {
		byID[r.VehicleID] = r.VersionNo
	}
	for _, v := range vehicles {
		if byID[v.ID] == 0 || v.AppliedVersion != byID[v.ID] {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) applicationView(app *model.Application) (entity.ApplicationView, error) {
	applicant, err := s.store.AccountByID(app.ApplicantID)
	if err != nil {
		return entity.ApplicationView{}, err
	}
	history, err := s.store.ApplicationVersions(app.ID)
	if err != nil {
		return entity.ApplicationView{}, err
	}
	view := entity.ApplicationView{
		ID: app.ID, Applicant: applicant.DisplayName, ApplicantRole: applicant.Username,
		Reason: app.Reason, Interval: app.Interval, Status: app.Status,
		StatusText: entity.ApplicationStatusText(app.Status), VersionNo: app.VersionNo,
		CreatedAt: app.CreatedAt,
	}
	if app.ApproverID != 0 {
		if approver, err := s.store.AccountByID(app.ApproverID); err == nil {
			view.Approver = approver.DisplayName
		}
	}
	for _, h := range history {
		view.History = append(view.History, entity.ApplicationVer{VersionNo: h.VersionNo, Reason: h.Reason, Interval: h.Interval, CreatedAt: h.CreatedAt})
	}
	var c model.Campaign
	if err := s.store.DB.Where("application_id = ?", app.ID).Order("id desc").First(&c).Error; err == nil {
		view.CampaignID = c.ID
		view.CampaignStatus = entity.StatusText(c.Status)
	}
	return view, nil
}

func (s *Service) campaignView(c *model.Campaign) (entity.CampaignView, error) {
	app, err := s.store.Application(c.ApplicationID)
	if err != nil {
		return entity.CampaignView{}, err
	}
	vehicles, err := s.store.Vehicles()
	if err != nil {
		return entity.CampaignView{}, err
	}
	releases, err := s.store.ReleasesByCampaign(c.ID)
	if err != nil {
		return entity.CampaignView{}, err
	}
	activity := filterKind(releases, entity.KindActivity)
	recovery := filterKind(releases, entity.KindRecovery)
	view := entity.CampaignView{
		ID: c.ID, ApplicationID: c.ApplicationID, Status: c.Status,
		StatusText: entity.StatusText(c.Status), PilotVehicles: repo.SplitCSV(c.PilotVehicles),
		Interval: app.Interval, Reason: app.Reason, StopReason: c.StopReason,
	}
	for _, v := range vehicles {
		view.Vehicles = append(view.Vehicles, s.rowFor(v, activity, entity.KindActivity))
	}
	for _, v := range vehicles {
		if row, ok := s.recoveryRowFor(v, recovery); ok {
			view.RecoveryRows = append(view.RecoveryRows, row)
		}
	}
	dispatched := map[uint]bool{}
	for _, r := range activity {
		dispatched[r.VehicleID] = true
	}
	for _, v := range vehicles {
		if dispatched[v.ID] {
			view.Dispatched = append(view.Dispatched, v.Code)
		} else {
			view.Undispatched = append(view.Undispatched, v.Code)
		}
	}
	all, _ := s.allActivitySucceeded(c.ID)
	view.AllSucceeded = all
	view.AllRecovered = len(recovery) > 0 && allStatus(recovery, entity.ReleaseSucceeded)
	return view, nil
}

func (s *Service) rowFor(v model.Vehicle, releases []model.Release, kind string) entity.VehicleRow {
	row := entity.VehicleRow{
		Code: v.Code, Name: v.Name, PreActivity: v.InitialInterval,
		AppliedVersion: v.AppliedVersion, Applied: v.AppliedInterval,
		Status: "待下发", ReleaseKind: kind,
	}
	rel, ok := latestForVehicle(releases, v.ID)
	if !ok {
		row.ExpectedVersion = 0
		row.Expected = v.AppliedInterval
		return row
	}
	row.ExpectedVersion = rel.VersionNo
	row.Expected = rel.TargetInterval
	row.DispatchedAt = rel.DispatchedAt
	row.ReceiptAt = rel.ReceiptAt
	row.Reason = rel.ResultNote
	row.TimedOut = rel.Status == entity.ReleaseTimeout || rel.LateSuccessAt != nil
	row.LateSuccess = rel.LateSuccessAt != nil
	switch rel.Status {
	case entity.ReleaseSucceeded:
		row.Status = "成功"
	case entity.ReleaseFailed:
		row.Status = "失败"
	case entity.ReleaseTimeout:
		row.Status = "超时"
	case entity.ReleaseDispatched:
		row.Status = "已下发（未确认）"
	}
	return row
}

func (s *Service) recoveryRowFor(v model.Vehicle, releases []model.Release) (entity.VehicleRow, bool) {
	rel, ok := latestForVehicle(releases, v.ID)
	if !ok {
		return entity.VehicleRow{}, false
	}
	row := entity.VehicleRow{
		Code: v.Code, Name: v.Name, PreActivity: v.InitialInterval,
		ExpectedVersion: rel.VersionNo, Expected: rel.TargetInterval,
		AppliedVersion: v.AppliedVersion, Applied: v.AppliedInterval,
		DispatchedAt: rel.DispatchedAt, ReceiptAt: rel.ReceiptAt,
		Reason: rel.ResultNote, ReleaseKind: entity.KindRecovery,
		TimedOut: rel.Status == entity.ReleaseTimeout || rel.LateSuccessAt != nil,
		LateSuccess: rel.LateSuccessAt != nil,
	}
	switch rel.Status {
	case entity.ReleaseSucceeded:
		row.Status = "已恢复"
	case entity.ReleaseFailed:
		row.Status = "恢复失败"
	case entity.ReleaseTimeout:
		row.Status = "未确认（超时）"
	default:
		row.Status = "未确认"
	}
	return row, true
}

func (s *Service) vehiclesByCodes(codes []string) ([]model.Vehicle, error) {
	vehicles, err := s.store.Vehicles()
	if err != nil {
		return nil, err
	}
	index := map[string]model.Vehicle{}
	for _, v := range vehicles {
		index[v.Code] = v
	}
	out := make([]model.Vehicle, 0, len(codes))
	seen := map[string]bool{}
	for _, code := range codes {
		code = strings.TrimSpace(code)
		v, ok := index[code]
		if !ok {
			return nil, entity.Err("invalid_argument", "试点车辆不属于本车队: "+code)
		}
		if seen[code] {
			return nil, entity.Err("invalid_argument", "试点车辆重复: "+code)
		}
		seen[code] = true
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) exception(kind string, vehicleID, campaignID uint, detail string) {
	_ = s.store.AddException(kind, vehicleID, campaignID, detail)
}

func defaultPilots() string { return "TE-01,TE-02" }

func filterKind(list []model.Release, kind string) []model.Release {
	out := make([]model.Release, 0, len(list))
	for _, r := range list {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// pickByCodes 按车辆编号筛选发布记录，用于试点车结果判定。
func pickByCodes(list []model.Release, vehicles []model.Vehicle, codes []string) []model.Release {
	want := map[uint]bool{}
	for _, code := range codes {
		for _, v := range vehicles {
			if v.Code == code {
				want[v.ID] = true
			}
		}
	}
	out := make([]model.Release, 0, len(codes))
	for _, r := range list {
		if want[r.VehicleID] {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VersionNo < out[j].VersionNo })
	return out
}

func allStatus(list []model.Release, status string) bool {
	if len(list) == 0 {
		return false
	}
	for _, r := range list {
		if r.Status != status {
			return false
		}
	}
	return true
}

func anyStatus(list []model.Release, status string) bool {
	for _, r := range list {
		if r.Status == status {
			return true
		}
	}
	return false
}

func latestForVehicle(list []model.Release, vehicleID uint) (model.Release, bool) {
	var best model.Release
	found := false
	for _, r := range list {
		if r.VehicleID != vehicleID {
			continue
		}
		if !found || r.VersionNo > best.VersionNo {
			best = r
			found = true
		}
	}
	return best, found
}

func describeRelease(r model.Release) string {
	switch r.Status {
	case entity.ReleaseSucceeded:
		return "该版本已回执成功"
	case entity.ReleaseFailed:
		return "该版本回执失败：" + r.ResultNote
	case entity.ReleaseTimeout:
		return "该版本超过教学超时阈值未回执"
	default:
		return "该版本已下发，等待回执"
	}
}
