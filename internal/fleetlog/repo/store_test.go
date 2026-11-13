package repo

import (
	"path/filepath"
	"testing"
	"time"

	"fleet-log-policy/internal/fleetlog/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "store.db"))
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
	return store
}

func TestOpenAndSeedIdempotent(t *testing.T) {
	store := newTestStore(t)
	for i := 0; i < 2; i++ {
		if err := store.Seed(); err != nil {
			t.Fatalf("seed #%d: %v", i, err)
		}
	}
	vehicles, err := store.Vehicles()
	if err != nil || len(vehicles) != 5 {
		t.Fatalf("预置车辆应为 5 台: %v %d", err, len(vehicles))
	}
	accounts, err := store.Accounts()
	if err != nil || len(accounts) != 3 {
		t.Fatalf("预置账号应为 3 个: %v %d", err, len(accounts))
	}
	if _, err := store.AccountByToken(""); err != ErrNotFound {
		t.Fatalf("空令牌应 ErrNotFound: %v", err)
	}
	if _, err := store.AccountByToken("nope"); err != ErrNotFound {
		t.Fatalf("未知令牌应 ErrNotFound: %v", err)
	}
	if _, err := store.VehicleByToken(""); err != ErrNotFound {
		t.Fatalf("空车辆凭证应 ErrNotFound: %v", err)
	}
	if _, err := store.VehicleByToken("nope"); err != ErrNotFound {
		t.Fatalf("未知车辆凭证应 ErrNotFound: %v", err)
	}
}

func TestReleaseQueriesAndTimeouts(t *testing.T) {
	store := newTestStore(t)
	vehicles, err := store.Vehicles()
	if err != nil {
		t.Fatalf("vehicles: %v", err)
	}
	now := time.Now()
	deadline := now.Add(time.Second)
	rel := &model.Release{
		CampaignID: 1, VehicleID: vehicles[0].ID, VersionNo: 1, Kind: "activity",
		TargetInterval: 30, PriorInterval: 60, Status: "dispatched", DispatchedAt: &now, DeadlineAt: &deadline,
	}
	if err := store.CreateRelease(nil, rel); err != nil {
		t.Fatalf("create release: %v", err)
	}
	latest, err := store.LatestReleaseByVehicle(vehicles[0].ID)
	if err != nil || latest.VersionNo != 1 {
		t.Fatalf("最新版本查询失败: %v %+v", err, latest)
	}
	if _, err := store.ReleaseByVehicleVersion(vehicles[0].ID, 2); err != ErrNotFound {
		t.Fatalf("不存在的版本应 ErrNotFound: %v", err)
	}
	byVersion, err := store.ReleasesByVersion(1)
	if err != nil || len(byVersion) != 1 {
		t.Fatalf("按版本查询失败: %v %d", err, len(byVersion))
	}
	if _, err := store.LatestReleaseByVehicle(vehicles[1].ID); err != ErrNotFound {
		t.Fatalf("无发布记录应 ErrNotFound: %v", err)
	}
	ids, err := store.MarkTimeouts(now.Add(2 * time.Second))
	if err != nil || len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("超时标记失败: %v %+v", err, ids)
	}
	after, err := store.ReleaseByVehicleVersion(vehicles[0].ID, 1)
	if err != nil || after.Status != "timeout" {
		t.Fatalf("超时状态未落库: %v %+v", err, after)
	}
	// 重复扫描不应重复返回活动。
	ids, err = store.MarkTimeouts(now.Add(3 * time.Second))
	if err != nil || len(ids) != 0 {
		t.Fatalf("重复扫描应无新增: %v %+v", err, ids)
	}
}

func TestExceptionAndAuditQueries(t *testing.T) {
	store := newTestStore(t)
	if err := store.AddException("cross_vehicle", 1, 1, "跨车"); err != nil {
		t.Fatalf("add exception: %v", err)
	}
	list, err := store.Exceptions(10)
	if err != nil || len(list) != 1 {
		t.Fatalf("异常查询失败: %v %d", err, len(list))
	}
	if _, err := store.Exceptions(0); err != nil {
		t.Fatalf("不限制条数查询失败: %v", err)
	}
	if err := store.AddAudit(nil, "tester", "approver", "approve", "app:1", "ok"); err != nil {
		t.Fatalf("add audit: %v", err)
	}
	audits, err := store.Audits(0)
	if err != nil || len(audits) != 1 {
		t.Fatalf("审计查询失败: %v %d", err, len(audits))
	}
	if audits[0].Actor != "tester" {
		t.Fatalf("审计内容错误: %+v", audits[0])
	}
}

func TestCampaignQueries(t *testing.T) {
	store := newTestStore(t)
	c := &model.Campaign{ApplicationID: 1, Status: "approved", PilotVehicles: "TE-01,TE-02", FleetKey: "TE-FLEET-1"}
	if err := store.CreateCampaign(nil, c); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	open, err := store.OpenCampaign(nil, "TE-FLEET-1", []string{"approved"})
	if err != nil || open.ID != c.ID {
		t.Fatalf("未结束活动查询失败: %v %+v", err, open)
	}
	if _, err := store.OpenCampaign(nil, "OTHER", []string{"approved"}); err != ErrNotFound {
		t.Fatalf("其他车队应无活动: %v", err)
	}
	latest, err := store.LatestCampaign()
	if err != nil || latest.ID != c.ID {
		t.Fatalf("最新活动查询失败: %v %+v", err, latest)
	}
	if _, err := store.Campaign(999); err != ErrNotFound {
		t.Fatalf("不存在活动应 ErrNotFound: %v", err)
	}
}

func TestApplicationAndReceiptCRUD(t *testing.T) {
	store := newTestStore(t)
	app := &model.Application{ApplicantID: 1, Reason: "排查抖动", Interval: 30, Status: "waiting", VersionNo: 1}
	if err := store.CreateApplication(nil, app); err != nil {
		t.Fatalf("create application: %v", err)
	}
	if app.ID == 0 {
		t.Fatal("创建后应回填编号")
	}
	if err := store.AddApplicationVersion(nil, &model.ApplicationVersion{ApplicationID: app.ID, VersionNo: 2, Reason: "补充", Interval: 45}); err != nil {
		t.Fatalf("add version: %v", err)
	}
	versions, err := store.ApplicationVersions(app.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("历史版本应为 2 条: %v %d", err, len(versions))
	}
	app.Status = "approved"
	app.ApproverID = 2
	if err := store.UpdateApplication(nil, app); err != nil {
		t.Fatalf("update application: %v", err)
	}
	got, err := store.Application(app.ID)
	if err != nil || got.Status != "approved" || got.ApproverID != 2 {
		t.Fatalf("申请更新未生效: %v %+v", err, got)
	}
	list, err := store.Applications()
	if err != nil || len(list) != 1 {
		t.Fatalf("申请列表错误: %v %d", err, len(list))
	}
	if _, err := store.Application(999); err != ErrNotFound {
		t.Fatalf("不存在申请应 ErrNotFound: %v", err)
	}

	vehicles, err := store.Vehicles()
	if err != nil {
		t.Fatalf("vehicles: %v", err)
	}
	v := vehicles[0]
	v.AppliedInterval = 30
	v.AppliedVersion = 1
	if err := store.SaveVehicle(nil, &v); err != nil {
		t.Fatalf("save vehicle: %v", err)
	}
	if got, err := store.VehicleByID(v.ID); err != nil || got.AppliedVersion != 1 {
		t.Fatalf("车辆状态未保存: %v %+v", err, got)
	}
	if _, err := store.VehicleByID(9999); err != ErrNotFound {
		t.Fatalf("不存在车辆应 ErrNotFound: %v", err)
	}

	rel := &model.Release{CampaignID: 1, VehicleID: v.ID, VersionNo: 1, Kind: "activity",
		TargetInterval: 30, PriorInterval: 60, Status: "dispatched"}
	if err := store.CreateRelease(nil, rel); err != nil {
		t.Fatalf("create release: %v", err)
	}
	rel.Status = "succeeded"
	if err := store.SaveRelease(nil, rel); err != nil {
		t.Fatalf("save release: %v", err)
	}
	receipt := &model.Receipt{ReleaseID: rel.ID, VehicleID: v.ID, VersionNo: 1, Success: true, RequestKey: "k1"}
	if err := store.CreateReceipt(nil, receipt); err != nil {
		t.Fatalf("create receipt: %v", err)
	}
	receipts, err := store.ReceiptsByRelease(rel.ID)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("回执查询错误: %v %d", err, len(receipts))
	}
	releases, err := store.ReleasesByVehicle(v.ID)
	if err != nil || len(releases) != 1 {
		t.Fatalf("车辆发布记录错误: %v %d", err, len(releases))
	}
	byCampaign, err := store.ReleasesByCampaign(1)
	if err != nil || len(byCampaign) != 1 {
		t.Fatalf("活动发布记录错误: %v %d", err, len(byCampaign))
	}
	if _, err := store.AccountByID(1); err != nil {
		t.Fatalf("账号查询失败: %v", err)
	}
	if _, err := store.AccountByID(999); err != ErrNotFound {
		t.Fatalf("不存在账号应 ErrNotFound: %v", err)
	}
	camp := &model.Campaign{ApplicationID: app.ID, Status: "approved", PilotVehicles: "TE-01,TE-02", FleetKey: "TE-FLEET-1"}
	if err := store.CreateCampaign(nil, camp); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	camp.Status = "piloting"
	if err := store.UpdateCampaign(nil, camp); err != nil {
		t.Fatalf("update campaign: %v", err)
	}
	if got, err := store.Campaign(camp.ID); err != nil || got.Status != "piloting" {
		t.Fatalf("活动更新未生效: %v %+v", err, got)
	}
}

func TestSplitCSV(t *testing.T) {
	cases := map[string]int{"": 0, " ": 0, "TE-01": 1, "TE-01,TE-02": 2, " TE-01 , TE-02 , ": 2, ",,": 0}
	for input, want := range cases {
		if got := len(SplitCSV(input)); got != want {
			t.Fatalf("SplitCSV(%q)=%d, want %d", input, got, want)
		}
	}
}
