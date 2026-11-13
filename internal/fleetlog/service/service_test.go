package service

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"fleet-log-policy/internal/fleetlog/entity"
	"fleet-log-policy/internal/fleetlog/model"
	"fleet-log-policy/internal/fleetlog/repo"
)

// newTestService 构建使用临时 SQLite 的服务，测试间完全隔离。
func newTestService(t *testing.T) (*Service, *repo.Store) {
	t.Helper()
	store, err := repo.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Seed(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc := New(store, 60)
	svc.now = func() time.Time { return time.Date(2026, 11, 13, 9, 0, 0, 0, time.Local) }
	t.Cleanup(func() {
		if sqlDB, err := store.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return svc, store
}

func accounts(t *testing.T, s *Service) map[string]model.Account {
	t.Helper()
	list, err := s.Accounts()
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	out := map[string]model.Account{}
	for _, a := range list {
		out[a.Role] = a
	}
	return out
}

// approvedCampaign 创建并批准一个申请，返回活动编号。
func approvedCampaign(t *testing.T, s *Service) (uint, map[string]model.Account) {
	t.Helper()
	acc := accounts(t, s)
	app, err := s.CreateApplication(acc[entity.RoleApplicant], "排查 AEB 误触发", 30)
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	view, err := s.Decide(acc[entity.RoleApprover], app.ID, true, "")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	return view.CampaignID, acc
}

func TestCreateApplicationRules(t *testing.T) {
	s, _ := newTestService(t)
	acc := accounts(t, s)
	applicant := acc[entity.RoleApplicant]

	cases := []struct {
		name     string
		actor    model.Account
		reason   string
		interval int
		wantErr  bool
	}{
		{"非申请人角色", acc[entity.RoleOperator], "原因", 30, true},
		{"原因为空", applicant, "  ", 30, true},
		{"间隔过小", applicant, "原因", 9, true},
		{"间隔过大", applicant, "原因", 301, true},
		{"合法申请", applicant, "原因", 10, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			view, err := s.CreateApplication(c.actor, c.reason, c.interval)
			if c.wantErr {
				if err == nil {
					t.Fatalf("期望失败但成功: %+v", view)
				}
				return
			}
			if err != nil {
				t.Fatalf("期望成功但失败: %v", err)
			}
			if view.Status != entity.ApplicationWaiting || view.VersionNo != 1 {
				t.Fatalf("初始状态错误: %+v", view)
			}
		})
	}
}

func TestDecideRules(t *testing.T) {
	s, _ := newTestService(t)
	acc := accounts(t, s)
	app, err := s.CreateApplication(acc[entity.RoleApplicant], "排查问题", 30)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := s.Decide(acc[entity.RoleOperator], app.ID, true, ""); err == nil {
		t.Fatal("运营审批应被拒绝")
	}
	if _, err := s.Decide(acc[entity.RoleApprover], 999, true, ""); err == nil {
		t.Fatal("不存在的申请应报错")
	}
	if _, err := s.Decide(acc[entity.RoleApprover], app.ID, false, ""); err == nil {
		t.Fatal("退回未填原因应被拒绝")
	}
	rejected, err := s.Decide(acc[entity.RoleApprover], app.ID, false, "原因不充分")
	if err != nil || rejected.Status != entity.ApplicationRejected {
		t.Fatalf("退回失败: %v %+v", err, rejected)
	}
	if _, err := s.Decide(acc[entity.RoleApprover], app.ID, true, ""); err == nil {
		t.Fatal("已退回申请不能直接批准")
	}

	resubmitted, err := s.Resubmit(acc[entity.RoleApplicant], app.ID, "补充原因", 45)
	if err != nil {
		t.Fatalf("重提失败: %v", err)
	}
	if resubmitted.VersionNo != 2 || len(resubmitted.History) != 2 {
		t.Fatalf("历史版本未保留: %+v", resubmitted)
	}
	if _, err := s.Resubmit(acc[entity.RoleApplicant], app.ID, "再次", 45); err == nil {
		t.Fatal("待审批状态不应允许再次重提")
	}
	if _, err := s.Resubmit(acc[entity.RoleOperator], app.ID, "越权", 45); err == nil {
		t.Fatal("非本人重提应被拒绝")
	}

	approved, err := s.Decide(acc[entity.RoleApprover], app.ID, true, "")
	if err != nil || approved.Status != entity.ApplicationApproved || approved.CampaignID == 0 {
		t.Fatalf("批准失败: %v %+v", err, approved)
	}
	// 同车队已有未结束活动时再次批准必须拒绝。
	second, err := s.CreateApplication(acc[entity.RoleApplicant], "第二个申请", 40)
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if _, err := s.Decide(acc[entity.RoleApprover], second.ID, true, ""); err == nil {
		t.Fatal("同车队已有未结束活动时应拒绝")
	}
}

func TestSelfApprovalBlocked(t *testing.T) {
	s, store := newTestService(t)
	// 构造一条申请人即负责人的申请，验证「审批人不能是申请人」。
	approver, err := store.AccountByToken("demo-token-approver")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	app := &model.Application{ApplicantID: approver.ID, Reason: "负责人自提", Interval: 30, Status: entity.ApplicationWaiting, VersionNo: 1}
	if err := store.CreateApplication(nil, app); err != nil {
		t.Fatalf("create app: %v", err)
	}
	if _, err := s.Decide(approver, app.ID, true, ""); err == nil {
		t.Fatal("审批本人申请应被拒绝")
	}
	exc, _ := s.Exceptions()
	if len(exc) == 0 || exc[0].Kind != entity.ExcSelfApproval {
		t.Fatalf("应记录自审异常: %+v", exc)
	}
}

func TestPilotAndPromote(t *testing.T) {
	s, _ := newTestService(t)
	campaignID, acc := approvedCampaign(t, s)
	operator := acc[entity.RoleOperator]

	if _, err := s.StartPilot(acc[entity.RoleApprover], campaignID, []string{"TE-01", "TE-02"}); err == nil {
		t.Fatal("非运营发起试点应被拒绝")
	}
	if _, err := s.StartPilot(operator, campaignID, []string{"TE-01"}); err == nil {
		t.Fatal("试点必须 2 台")
	}
	if _, err := s.StartPilot(operator, campaignID, []string{"TE-01", "TE-99"}); err == nil {
		t.Fatal("非法车辆应被拒绝")
	}
	if _, err := s.StartPilot(operator, campaignID, []string{"TE-01", "TE-01"}); err == nil {
		t.Fatal("重复车辆应被拒绝")
	}
	if _, err := s.Promote(operator, campaignID); err == nil {
		t.Fatal("未试点即推广应被拒绝")
	}
	view, err := s.StartPilot(operator, campaignID, []string{"TE-01", "TE-02"})
	if err != nil {
		t.Fatalf("试点失败: %v", err)
	}
	if view.Status != entity.CampaignPiloting {
		t.Fatalf("试点状态错误: %s", view.Status)
	}
	if len(view.Undispatched) != 3 {
		t.Fatalf("试点期其余 3 台不应下发: %+v", view.Undispatched)
	}
	if _, err := s.StartPilot(operator, campaignID, []string{"TE-01", "TE-02"}); err == nil {
		t.Fatal("重复发起试点应被拒绝")
	}

	vehicles := mustVehicles(t, s)
	for _, code := range []string{"TE-01", "TE-02"} {
		if _, err := s.Receipt(vehicles[code], 1, true, "", "p-"+code); err != nil {
			t.Fatalf("%s 回执失败: %v", code, err)
		}
	}
	view, err = s.CampaignView(campaignID)
	if err != nil {
		t.Fatalf("campaign view: %v", err)
	}
	if view.Status != entity.CampaignPilotOK {
		t.Fatalf("两台试点成功后应为试点成功: %s", view.Status)
	}

	if _, err := s.Promote(acc[entity.RoleApprover], campaignID); err == nil {
		t.Fatal("非运营推广应被拒绝")
	}
	view, err = s.Promote(operator, campaignID)
	if err != nil {
		t.Fatalf("推广失败: %v", err)
	}
	if view.Status != entity.CampaignPromoting || len(view.Dispatched) != 5 {
		t.Fatalf("推广结果错误: %+v", view)
	}
	if _, err := s.Promote(operator, campaignID); err == nil {
		t.Fatal("推广后再次推广应被拒绝")
	}
}

func TestPilotTimeoutPausesCampaign(t *testing.T) {
	s, _ := newTestService(t)
	campaignID, acc := approvedCampaign(t, s)
	if _, err := s.StartPilot(acc[entity.RoleOperator], campaignID, []string{"TE-01", "TE-02"}); err != nil {
		t.Fatalf("pilot: %v", err)
	}
	// 教学时钟前进超过阈值。
	base := s.now()
	s.now = func() time.Time { return base.Add(61 * time.Second) }
	if err := s.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	view, err := s.CampaignView(campaignID)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if view.Status != entity.CampaignPaused {
		t.Fatalf("试点超时后应暂停: %s", view.Status)
	}
	if _, err := s.Promote(acc[entity.RoleOperator], campaignID); err == nil {
		t.Fatal("暂停状态不得推广")
	}
	exc, _ := s.Exceptions()
	found := false
	for _, e := range exc {
		if e.Kind == entity.ExcIllegalPromote {
			found = true
		}
	}
	if !found {
		t.Fatal("非法推广应留痕")
	}
}

func TestReceiptRules(t *testing.T) {
	s, _ := newTestService(t)
	campaignID, acc := approvedCampaign(t, s)
	if _, err := s.StartPilot(acc[entity.RoleOperator], campaignID, []string{"TE-01", "TE-02"}); err != nil {
		t.Fatalf("pilot: %v", err)
	}
	vehicles := mustVehicles(t, s)

	if _, err := s.Receipt(vehicles["TE-01"], 9, true, "", "u1"); err == nil {
		t.Fatal("未知版本应被拒绝")
	}
	if _, err := s.Receipt(vehicles["TE-03"], 1, true, "", "u2"); err == nil {
		t.Fatal("跨车回执应被拒绝")
	}
	failed, err := s.Receipt(vehicles["TE-01"], 1, false, "本地存储只读", "f1")
	if err != nil || failed.Accepted != true {
		t.Fatalf("失败回执应被受理: %v %+v", err, failed)
	}
	after, _ := s.CampaignView(campaignID)
	if after.Vehicles[0].Applied != 60 || after.Vehicles[0].Status != "失败" {
		t.Fatalf("失败不应改变生效值: %+v", after.Vehicles[0])
	}
	ok, err := s.Receipt(vehicles["TE-01"], 1, true, "", "s1")
	if err != nil || !ok.Accepted {
		t.Fatalf("同版本成功回执: %v %+v", err, ok)
	}
	dup, err := s.Receipt(vehicles["TE-01"], 1, true, "", "s1-again")
	if err != nil || !dup.Duplicate {
		t.Fatalf("重复回执应幂等: %v %+v", err, dup)
	}
	pull, err := s.Pull(vehicles["TE-01"])
	if err != nil || pull.Version != 1 || pull.Interval != 30 {
		t.Fatalf("拉取结果错误: %v %+v", err, pull)
	}
}

func TestOldVersionReceiptIgnored(t *testing.T) {
	s, _ := newTestService(t)
	campaignID, acc := approvedCampaign(t, s)
	operator, approver := acc[entity.RoleOperator], acc[entity.RoleApprover]
	if _, err := s.StartPilot(operator, campaignID, []string{"TE-01", "TE-02"}); err != nil {
		t.Fatalf("pilot: %v", err)
	}
	vehicles := mustVehicles(t, s)
	for _, code := range []string{"TE-01", "TE-02"} {
		if _, err := s.Receipt(vehicles[code], 1, true, "", code); err != nil {
			t.Fatalf("receipt: %v", err)
		}
	}
	if _, err := s.Promote(operator, campaignID); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if _, err := s.Stop(approver, campaignID, "停止后恢复"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := s.ApproveRestore(approver, campaignID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	// TE-03 的活动版本 v1 未回执，恢复版本 v2 已下发；此时 v1 回执为旧版本，不得覆盖生效值。
	res, err := s.Receipt(vehicles["TE-03"], 1, true, "", "late-old")
	if err != nil {
		t.Fatalf("旧版本回执应被受理但忽略: %v", err)
	}
	if !res.Ignored {
		t.Fatalf("旧版本回执应标记忽略: %+v", res)
	}
	view, err := s.CampaignView(campaignID)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	for _, row := range view.Vehicles {
		if row.Code == "TE-03" && row.AppliedVersion >= 1 {
			t.Fatalf("旧版本回执不得写入生效值: %+v", row)
		}
	}
	exc, _ := s.Exceptions()
	found := false
	for _, e := range exc {
		if e.Kind == entity.ExcOldVersion {
			found = true
		}
	}
	if !found {
		t.Fatal("旧版本回执应留痕")
	}
}

func TestLateSuccessAfterTimeout(t *testing.T) {
	s, _ := newTestService(t)
	campaignID, acc := approvedCampaign(t, s)
	if _, err := s.StartPilot(acc[entity.RoleOperator], campaignID, []string{"TE-01", "TE-02"}); err != nil {
		t.Fatalf("pilot: %v", err)
	}
	base := s.now()
	s.now = func() time.Time { return base.Add(61 * time.Second) }
	if err := s.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	vehicles := mustVehicles(t, s)
	res, err := s.Receipt(vehicles["TE-01"], 1, true, "", "late-ok")
	if err != nil {
		t.Fatalf("迟到成功: %v", err)
	}
	if res.Message == "" {
		t.Fatalf("应返回迟到成功说明")
	}
	view, err := s.CampaignView(campaignID)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	row := view.Vehicles[0]
	if row.Status != "成功" || !row.LateSuccess || row.Applied != 30 {
		t.Fatalf("迟到成功应更新结果并保留超时历史: %+v", row)
	}
	if view.Status != entity.CampaignPaused {
		t.Fatalf("已暂停活动不应因迟到回执重启: %s", view.Status)
	}
}

func TestStopAndRestore(t *testing.T) {
	s, _ := newTestService(t)
	campaignID, acc := approvedCampaign(t, s)
	operator, approver := acc[entity.RoleOperator], acc[entity.RoleApprover]
	if _, err := s.StartPilot(operator, campaignID, []string{"TE-01", "TE-02"}); err != nil {
		t.Fatalf("pilot: %v", err)
	}
	vehicles := mustVehicles(t, s)
	if _, err := s.Receipt(vehicles["TE-01"], 1, true, "", "s"); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if _, err := s.Stop(operator, campaignID, "原因"); err == nil {
		t.Fatal("非负责人停止应被拒绝")
	}
	if _, err := s.Stop(approver, campaignID, ""); err == nil {
		t.Fatal("停止必须填原因")
	}
	stopped, err := s.Stop(approver, campaignID, "试点车离线，暂停推进")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(stopped.Dispatched) != 2 || len(stopped.Undispatched) != 3 {
		t.Fatalf("停止后必须保留已发/未发范围: %+v", stopped)
	}
	if _, err := s.Stop(approver, campaignID, "重复停止"); err == nil {
		t.Fatal("已停止活动不能重复停止")
	}
	if _, err := s.Complete(approver, campaignID); err == nil {
		t.Fatal("已停止活动不能结案完成")
	}
	if _, err := s.ApproveRestore(operator, campaignID); err == nil {
		t.Fatal("非负责人恢复应被拒绝")
	}
	restoring, err := s.ApproveRestore(approver, campaignID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restoring.Status != entity.CampaignRestoring || len(restoring.RecoveryRows) != 2 {
		t.Fatalf("恢复范围应为曾发布车辆: %+v", restoring)
	}
	for _, row := range restoring.RecoveryRows {
		if row.Expected != 60 || row.ExpectedVersion != 2 {
			t.Fatalf("恢复目标应为活动前 60 秒并创建更高版本: %+v", row)
		}
	}
	if _, err := s.CompleteRestore(approver, campaignID); err == nil {
		t.Fatal("仍有未确认时不得标记全部恢复")
	}
	if _, err := s.Receipt(vehicles["TE-01"], 2, true, "", "r1"); err != nil {
		t.Fatalf("恢复回执: %v", err)
	}
	if _, err := s.Receipt(vehicles["TE-02"], 2, true, "", "r2"); err != nil {
		t.Fatalf("恢复回执: %v", err)
	}
	done, err := s.CompleteRestore(approver, campaignID)
	if err != nil || done.Status != entity.CampaignRestored || !done.AllRecovered {
		t.Fatalf("恢复完成失败: %v %+v", err, done)
	}
}

func TestCompleteRequiresAllSucceeded(t *testing.T) {
	s, _ := newTestService(t)
	campaignID, acc := approvedCampaign(t, s)
	operator, approver := acc[entity.RoleOperator], acc[entity.RoleApprover]
	if _, err := s.StartPilot(operator, campaignID, []string{"TE-01", "TE-02"}); err != nil {
		t.Fatalf("pilot: %v", err)
	}
	vehicles := mustVehicles(t, s)
	for _, code := range []string{"TE-01", "TE-02"} {
		if _, err := s.Receipt(vehicles[code], 1, true, "", code); err != nil {
			t.Fatalf("receipt: %v", err)
		}
	}
	if _, err := s.Promote(operator, campaignID); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if _, err := s.Complete(approver, campaignID); err == nil {
		t.Fatal("仍有未确认车辆时不得结案")
	}
	if _, err := s.Complete(operator, campaignID); err == nil {
		t.Fatal("非负责人结案应被拒绝")
	}
	for _, code := range []string{"TE-03", "TE-04", "TE-05"} {
		if _, err := s.Receipt(vehicles[code], 1, true, "", code); err != nil {
			t.Fatalf("receipt %s: %v", code, err)
		}
	}
	done, err := s.Complete(approver, campaignID)
	if err != nil || done.Status != entity.CampaignCompleted || !done.AllSucceeded {
		t.Fatalf("结案失败: %v %+v", err, done)
	}
	if _, err := s.Stop(approver, campaignID, "已完成再停止"); err == nil {
		t.Fatal("已完成活动不能停止")
	}
}

func TestApplicationListAndViews(t *testing.T) {
	s, _ := newTestService(t)
	campaignID, _ := approvedCampaign(t, s)
	list, err := s.ApplicationList()
	if err != nil || len(list) != 1 {
		t.Fatalf("申请列表: %v %d", err, len(list))
	}
	if list[0].CampaignID != campaignID || list[0].CampaignStatus == "" {
		t.Fatalf("申请应关联活动: %+v", list[0])
	}
	if _, err := s.ApplicationView(999); err == nil {
		t.Fatal("不存在的申请应报错")
	}
	if _, err := s.CampaignView(999); err == nil {
		t.Fatal("不存在的活动应报错")
	}
	empty, err := s.LatestCampaignView()
	if err != nil {
		t.Fatalf("latest view: %v", err)
	}
	if empty.ID != campaignID {
		t.Fatalf("最新活动编号错误: %+v", empty)
	}
	audits, err := s.AuditList()
	if err != nil || len(audits) == 0 {
		t.Fatalf("审计日志为空: %v", err)
	}
	vehicles, err := s.Vehicles()
	if err != nil || len(vehicles) != 5 {
		t.Fatalf("预置车辆应为 5 台: %v %d", err, len(vehicles))
	}
	if _, err := s.AccountByToken("bad"); err == nil {
		t.Fatal("无效令牌应报错")
	}
	if _, err := s.VehicleByToken("bad"); err == nil {
		t.Fatal("无效车辆凭证应报错")
	}
}

func TestFullRecoveryNeedsActivity(t *testing.T) {
	s, _ := newTestService(t)
	campaignID, acc := approvedCampaign(t, s)
	// 未发布任何车辆即停止，恢复应被拒绝（无曾发布车辆）。
	if _, err := s.Stop(acc[entity.RoleApprover], campaignID, "未发布即停止"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := s.ApproveRestore(acc[entity.RoleApprover], campaignID); err == nil {
		t.Fatal("没有已下发车辆时恢复应被拒绝")
	}
	if _, err := s.ApproveRestore(acc[entity.RoleApprover], 999); err == nil {
		t.Fatal("不存在的活动应报错")
	}
	if _, err := s.Stop(acc[entity.RoleApprover], 999, "x"); err == nil {
		t.Fatal("不存在的活动停止应报错")
	}
	if _, err := s.Promote(acc[entity.RoleOperator], 999); err == nil {
		t.Fatal("不存在的活动推广应报错")
	}
	if _, err := s.StartPilot(acc[entity.RoleOperator], 999, []string{"TE-01", "TE-02"}); err == nil {
		t.Fatal("不存在的活动试点应报错")
	}
	if _, err := s.CompleteRestore(acc[entity.RoleApprover], campaignID); err == nil {
		t.Fatal("非恢复中活动不能标记恢复完成")
	}
	if _, err := s.CompleteRestore(acc[entity.RoleApprover], 999); err == nil {
		t.Fatal("不存在的活动应报错")
	}
}

// TestConcurrentApproveKeepsSingleOpenCampaign 并发批准同一车队多个申请，只能产生一个未结束活动。
func TestConcurrentApproveKeepsSingleOpenCampaign(t *testing.T) {
	s, _ := newTestService(t)
	acc := accounts(t, s)
	const n = 6
	ids := make([]uint, 0, n)
	for i := 0; i < n; i++ {
		app, err := s.CreateApplication(acc[entity.RoleApplicant], "并发申请", 30)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		ids = append(ids, app.ID)
	}
	var wg sync.WaitGroup
	results := make(chan error, n)
	for _, id := range ids {
		wg.Add(1)
		go func(appID uint) {
			defer wg.Done()
			_, err := s.Decide(acc[entity.RoleApprover], appID, true, "")
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("并发批准应只成功一次，实际 %d", success)
	}
	campaigns, err := s.campaigns()
	if err != nil || len(campaigns) != 1 {
		t.Fatalf("只应存在一个活动: %v %d", err, len(campaigns))
	}
}

// TestConcurrentReceiptsIdempotent 同一版本并发成功回执只能记账一次，生效值唯一。
func TestConcurrentReceiptsIdempotent(t *testing.T) {
	s, _ := newTestService(t)
	campaignID, acc := approvedCampaign(t, s)
	if _, err := s.StartPilot(acc[entity.RoleOperator], campaignID, []string{"TE-01", "TE-02"}); err != nil {
		t.Fatalf("pilot: %v", err)
	}
	v := mustVehicles(t, s)["TE-01"]
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Receipt(v, 1, true, "", "same-key")
		}()
	}
	wg.Wait()
	releases, err := s.store.ReleasesByVehicle(v.ID)
	if err != nil || len(releases) != 1 {
		t.Fatalf("发布记录唯一性被破坏: %v %d", err, len(releases))
	}
	receipts, err := s.store.ReceiptsByRelease(releases[0].ID)
	if err != nil {
		t.Fatalf("receipts: %v", err)
	}
	duplicates := 0
	for _, r := range receipts {
		if r.Duplicate {
			duplicates++
		}
	}
	if len(receipts) != 8 || duplicates != 7 {
		t.Fatalf("并发回执应 1 次记账 + 7 次幂等: 总 %d 重复 %d", len(receipts), duplicates)
	}
	after, _ := s.store.VehicleByID(v.ID)
	if after.AppliedVersion != 1 || after.AppliedInterval != 30 {
		t.Fatalf("生效值应唯一且正确: %+v", after)
	}
}

func mustVehicles(t *testing.T, s *Service) map[string]model.Vehicle {
	t.Helper()
	list, err := s.Vehicles()
	if err != nil {
		t.Fatalf("vehicles: %v", err)
	}
	out := map[string]model.Vehicle{}
	for _, v := range list {
		out[v.Code] = v
	}
	return out
}
