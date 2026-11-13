# 部署说明（题目 09）

本目录提供**可实际执行的部署与回滚脚本**，用于本机教学环境发布；CI 的 `deploy` job 复用同一脚本（自托管 runner）。

## 前置条件

- Windows + PowerShell（本机教学环境，`deploy.ps1`）
- 本地 MySQL 可用（默认 `root:root@tcp(127.0.0.1:3306)/questions`，取自 `configs/config.yaml`）
- Go 1.25（部署前会跑 `go vet` 与单元测试作为质量门禁）
- 目标端口空闲（脚本默认 18080；本机 8080 被其他应用占用）

## 一键部署

```powershell
powershell -ExecutionPolicy Bypass -File deploy/deploy.ps1 -Port 18080
```

脚本按顺序执行，任一步失败即中止：

1. 质量门禁：`go vet ./...`、`go test ./internal/fleetlog/...`（不通过禁止部署）
2. 构建制品 `deploy/releases/gateway-<commit>-<时间戳>.exe`
3. 停止上一版本进程（仅停止本目录部署的进程，端口被其他程序占用时直接报错）
4. 启动新版本（`FLEETLOG_DB_DSN` 指向本地 MySQL，`FLEETLOG_TIMEOUT_SECONDS` 默认 60）
5. 健康检查 `/healthz`（最长等 15 秒），失败自动回滚并退出
6. 业务冒烟：预置账号、申请列表、业务页面、活动总览；失败停止新版本
7. 写部署记录 `docs/evidence/deploy-<版本标识>.md`，并更新 `deploy/releases/current.json`、`history.json`

## 回滚

```powershell
powershell -ExecutionPolicy Bypass -File deploy/rollback.ps1
```

读取 `history.json` 中上一版本 → 停当前进程 → 拉起上一版本 → 健康检查 → 记录到 `docs/evidence/deploy-rollback.log`。

## 首次部署失败时如何撤销

1. 脚本在健康检查或冒烟失败时会自行停止新进程，并把上一版本重新拉起（若存在）。
2. 若进程残留：`Get-NetTCPConnection -LocalPort 18080 -State Listen` 找到 PID 后 `Stop-Process -Id <pid> -Force`。
3. 若数据库迁移引入问题：`db/schema.sql` 为幂等建表脚本，业务表可用 `deploy` 前备份恢复；本业务无破坏性变更。

## 制品与记录

- 制品/日志：`deploy/releases/`（`.gitignore` 已排除，不进仓库）
- 部署记录：`docs/evidence/deploy-*.md`（入库，含提交、版本、地址、冒烟结果、回滚方式）
