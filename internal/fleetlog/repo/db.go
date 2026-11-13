// Package repo 提供题目 09 的持久化访问：SQLite（纯 Go 驱动，无需 CGO）与 Gorm。
package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"fleet-log-policy/internal/fleetlog/model"
)

// Store 封装数据库句柄。
type Store struct {
	DB *gorm.DB
}

// Open 打开（必要时创建）SQLite 数据库并完成迁移。
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据库目录失败: %w", err)
		}
	}
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite 失败: %w", err)
	}
	if err := db.AutoMigrate(
		&model.Account{}, &model.Vehicle{}, &model.Application{}, &model.ApplicationVersion{},
		&model.Campaign{}, &model.Release{}, &model.Receipt{}, &model.ExceptionRecord{}, &model.AuditLog{},
	); err != nil {
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}
	return &Store{DB: db}, nil
}

// Seed 写入预置账号、5 台测试车；已存在时跳过，保证重启后数据不丢失。
func (s *Store) Seed() error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		accounts := []model.Account{
			{Username: "research", DisplayName: "张研（研发申请人）", Role: "applicant", Token: "demo-token-applicant"},
			{Username: "lead", DisplayName: "李责（审批负责人）", Role: "approver", Token: "demo-token-approver"},
			{Username: "ops", DisplayName: "王运（发布运营）", Role: "operator", Token: "demo-token-operator"},
		}
		for _, a := range accounts {
			var count int64
			if err := tx.Model(&model.Account{}).Where("username = ?", a.Username).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				if err := tx.Create(&a).Error; err != nil {
					return err
				}
			}
		}
		vehicles := []model.Vehicle{
			{Code: "TE-01", Name: "试点车-在线", Token: "veh-token-01", InitialInterval: 60, AppliedInterval: 60},
			{Code: "TE-02", Name: "试点车-离线", Token: "veh-token-02", InitialInterval: 60, AppliedInterval: 60},
			{Code: "TE-03", Name: "推广车-甲", Token: "veh-token-03", InitialInterval: 60, AppliedInterval: 60},
			{Code: "TE-04", Name: "推广车-乙", Token: "veh-token-04", InitialInterval: 60, AppliedInterval: 60},
			{Code: "TE-05", Name: "推广车-丙", Token: "veh-token-05", InitialInterval: 60, AppliedInterval: 60},
		}
		for _, v := range vehicles {
			var count int64
			if err := tx.Model(&model.Vehicle{}).Where("code = ?", v.Code).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				if err := tx.Create(&v).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// now 统一时间来源，便于测试。
var now = func() time.Time { return time.Now() }
