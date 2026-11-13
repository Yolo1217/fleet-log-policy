package repo

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"fleet-log-policy/internal/fleetlog/model"
)

// ErrNotFound 表示记录不存在。
var ErrNotFound = errors.New("记录不存在")

// AccountByToken 按会话令牌查账号。
func (s *Store) AccountByToken(token string) (model.Account, error) {
	var a model.Account
	if token == "" {
		return a, ErrNotFound
	}
	err := s.DB.Where("token = ?", token).First(&a).Error
	return a, wrap(err)
}

// VehicleByToken 按车辆凭证查车辆。
func (s *Store) VehicleByToken(token string) (model.Vehicle, error) {
	var v model.Vehicle
	if token == "" {
		return v, ErrNotFound
	}
	err := s.DB.Where("token = ?", token).First(&v).Error
	return v, wrap(err)
}

// Vehicles 返回全部车辆（按编号）。
func (s *Store) Vehicles() ([]model.Vehicle, error) {
	var list []model.Vehicle
	err := s.DB.Order("code asc").Find(&list).Error
	return list, err
}

// VehicleByID 按主键查车辆。
func (s *Store) VehicleByID(id uint) (model.Vehicle, error) {
	var v model.Vehicle
	err := s.DB.First(&v, id).Error
	return v, wrap(err)
}

// SaveVehicle 更新车辆状态。
func (s *Store) SaveVehicle(tx *gorm.DB, v *model.Vehicle) error {
	return s.db(tx).Save(v).Error
}

// CreateApplication 创建申请与首个历史版本。
func (s *Store) CreateApplication(tx *gorm.DB, app *model.Application) error {
	db := s.db(tx)
	if err := db.Create(app).Error; err != nil {
		return err
	}
	return db.Create(&model.ApplicationVersion{
		ApplicationID: app.ID, VersionNo: app.VersionNo, Reason: app.Reason, Interval: app.Interval,
	}).Error
}

// AddApplicationVersion 追加历史版本。
func (s *Store) AddApplicationVersion(tx *gorm.DB, v *model.ApplicationVersion) error {
	return s.db(tx).Create(v).Error
}

// Application 按主键查申请。
func (s *Store) Application(id uint) (model.Application, error) {
	var a model.Application
	err := s.DB.First(&a, id).Error
	return a, wrap(err)
}

// UpdateApplication 保存申请。
func (s *Store) UpdateApplication(tx *gorm.DB, a *model.Application) error {
	return s.db(tx).Save(a).Error
}

// Applications 返回申请（新到旧）。
func (s *Store) Applications() ([]model.Application, error) {
	var list []model.Application
	err := s.DB.Order("id desc").Find(&list).Error
	return list, err
}

// ApplicationVersions 返回申请历史版本。
func (s *Store) ApplicationVersions(appID uint) ([]model.ApplicationVersion, error) {
	var list []model.ApplicationVersion
	err := s.DB.Where("application_id = ?", appID).Order("version_no asc").Find(&list).Error
	return list, err
}

// OpenCampaign 查同车队未结束活动。
func (s *Store) OpenCampaign(tx *gorm.DB, fleetKey string, openStatuses []string) (model.Campaign, error) {
	var c model.Campaign
	err := s.db(tx).Where("fleet_key = ? AND status IN ?", fleetKey, openStatuses).First(&c).Error
	return c, wrap(err)
}

// CreateCampaign 新建活动。
func (s *Store) CreateCampaign(tx *gorm.DB, c *model.Campaign) error {
	return s.db(tx).Create(c).Error
}

// Campaign 按主键查活动。
func (s *Store) Campaign(id uint) (model.Campaign, error) {
	var c model.Campaign
	err := s.DB.First(&c, id).Error
	return c, wrap(err)
}

// UpdateCampaign 保存活动。
func (s *Store) UpdateCampaign(tx *gorm.DB, c *model.Campaign) error {
	return s.db(tx).Save(c).Error
}

// CompareAndSetCampaignStatus 仅当库内状态仍为 from 时才更新为 to，返回是否命中。
// 后台 tick 的活动状态推进必须走这里：它读到的是快照，若在此期间请求路径已把活动推进，
// 直接 Save 会把状态写回旧值（丢失更新），表现为"活动不在恢复中""状态不允许结案"等偶发 409。
func (s *Store) CompareAndSetCampaignStatus(id uint, from, to string) (bool, error) {
	res := s.DB.Model(&model.Campaign{}).Where("id = ? AND status = ?", id, from).Update("status", to)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// LatestCampaign 返回最新活动（无则 ErrNotFound）。
func (s *Store) LatestCampaign() (model.Campaign, error) {
	var c model.Campaign
	err := s.DB.Order("id desc").First(&c).Error
	return c, wrap(err)
}

// CreateRelease 新建发布版本并推进车辆版本游标。
func (s *Store) CreateRelease(tx *gorm.DB, r *model.Release) error {
	db := s.db(tx)
	if err := db.Create(r).Error; err != nil {
		return err
	}
	return db.Model(&model.Vehicle{}).Where("id = ?", r.VehicleID).
		Update("latest_version", r.VersionNo).Error
}

// ReleasesByCampaign 返回活动全部发布版本。
func (s *Store) ReleasesByCampaign(campaignID uint) ([]model.Release, error) {
	var list []model.Release
	err := s.DB.Where("campaign_id = ?", campaignID).Order("version_no asc").Find(&list).Error
	return list, err
}

// ReleasesByVehicle 返回某车全部发布版本（按版本升序）。
func (s *Store) ReleasesByVehicle(vehicleID uint) ([]model.Release, error) {
	var list []model.Release
	err := s.DB.Where("vehicle_id = ?", vehicleID).Order("version_no asc").Find(&list).Error
	return list, err
}

// ReleaseByVehicleVersion 按（车辆, 版本）查发布记录。
func (s *Store) ReleaseByVehicleVersion(vehicleID uint, version int) (model.Release, error) {
	var r model.Release
	err := s.DB.Where("vehicle_id = ? AND version_no = ?", vehicleID, version).First(&r).Error
	return r, wrap(err)
}

// ReleasesByVersion 返回指定版本号的全部发布记录（用于识别跨车回执）。
func (s *Store) ReleasesByVersion(version int) ([]model.Release, error) {
	var list []model.Release
	err := s.DB.Where("version_no = ?", version).Find(&list).Error
	return list, err
}

// LatestReleaseByVehicle 返回某车最新发布版本。
func (s *Store) LatestReleaseByVehicle(vehicleID uint) (model.Release, error) {
	var r model.Release
	err := s.DB.Where("vehicle_id = ?", vehicleID).Order("version_no desc").First(&r).Error
	return r, wrap(err)
}

// SaveRelease 保存发布版本。
func (s *Store) SaveRelease(tx *gorm.DB, r *model.Release) error {
	return s.db(tx).Save(r).Error
}

// CreateReceipt 写入回执。
func (s *Store) CreateReceipt(tx *gorm.DB, r *model.Receipt) error {
	return s.db(tx).Create(r).Error
}

// ReceiptsByRelease 返回某发布版本的回执。
func (s *Store) ReceiptsByRelease(releaseID uint) ([]model.Receipt, error) {
	var list []model.Receipt
	err := s.DB.Where("release_id = ?", releaseID).Order("id asc").Find(&list).Error
	return list, err
}

// MarkTimeouts 把超过截止时间仍未回执的版本置为超时，返回受影响活动 ID。
func (s *Store) MarkTimeouts(at time.Time) ([]uint, error) {
	var list []model.Release
	if err := s.DB.Where("status = ? AND deadline_at IS NOT NULL AND deadline_at < ?", "dispatched", at).
		Find(&list).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(list))
	seen := map[uint]bool{}
	for i := range list {
		note := "超过教学超时阈值未收到有效回执"
		if err := s.DB.Model(&model.Release{}).Where("id = ? AND status = ?", list[i].ID, "dispatched").
			Updates(map[string]interface{}{"status": "timeout", "result_note": note}).Error; err != nil {
			return nil, err
		}
		if !seen[list[i].CampaignID] {
			seen[list[i].CampaignID] = true
			ids = append(ids, list[i].CampaignID)
		}
	}
	return ids, nil
}

// AddException 写入异常留痕。
func (s *Store) AddException(kind string, vehicleID, campaignID uint, detail string) error {
	return s.DB.Create(&model.ExceptionRecord{
		Kind: kind, VehicleID: vehicleID, CampaignID: campaignID, Detail: detail, CreatedAt: time.Now(),
	}).Error
}

// Exceptions 返回异常留痕（新到旧）。
func (s *Store) Exceptions(limit int) ([]model.ExceptionRecord, error) {
	var list []model.ExceptionRecord
	q := s.DB.Order("id desc")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&list).Error
	return list, err
}

// AddAudit 写入审计日志。
func (s *Store) AddAudit(tx *gorm.DB, actor, role, action, target, detail string) error {
	return s.db(tx).Create(&model.AuditLog{
		Actor: actor, Role: role, Action: action, Target: target, Detail: detail, CreatedAt: time.Now(),
	}).Error
}

// Audits 返回审计日志（新到旧）。
func (s *Store) Audits(limit int) ([]model.AuditLog, error) {
	var list []model.AuditLog
	q := s.DB.Order("id desc")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&list).Error
	return list, err
}

// AccountByID 按主键查账号。
func (s *Store) AccountByID(id uint) (model.Account, error) {
	var a model.Account
	err := s.DB.First(&a, id).Error
	return a, wrap(err)
}

// Accounts 返回全部账号。
func (s *Store) Accounts() ([]model.Account, error) {
	var list []model.Account
	err := s.DB.Order("id asc").Find(&list).Error
	return list, err
}

// db 支持在事务内复用句柄。
func (s *Store) db(tx *gorm.DB) *gorm.DB {
	if tx != nil {
		return tx
	}
	return s.DB
}

// wrap 统一把 gorm 的未找到错误转为 ErrNotFound。
func wrap(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

// SplitCSV 拆分逗号分隔编号。
func SplitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
