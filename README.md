# fleet-log-policy：题目 09 测试车队日志策略的申请、审批、分批发布与结果核查

对应《Go 项目业务场景题》题目 09：一次非安全日志配置（日志上报间隔 10～300 秒）从申请、审批、试点发布、推广、停止到恢复的完整运营场景。

## 脚手架来源说明

- 脚手架来源：本地 `bw-cli` 脚手架（源码 `D:\Desktop\bw-cli-main`，来自 `github.com/BwCloudWeGo/bw-cli`），使用 `bw-cli new --source` 生成干净框架（Gin + gRPC + Gorm，DDD 分层）。
- 本题新增内容：`AGENTS.md`（AI 约束）、`docs/09-*` 需求分析文档、题目 09 业务代码与测试、模拟车辆服务、CI/CD 流水线配置（开发阶段逐步新增，见 `dev/09` 分支提交历史）。
- 未使用脚手架自带 user/note 示例业务；`bw-cli new` 已自动移除。

## 分支约定

- `main`：仅包含初始化记录及经 PR 合并的验收版本，禁止直接推送。
- `dev/09`：本题全部业务开发、测试、文档提交均在此分支进行。

## 快速启动

安装依赖并验证：

```bash
make tidy
make proto
make test
```

Makefile 只调用 Go 命令，不依赖 `find`、`sed`、`if [ ... ]` 等 Unix shell 语法；`make proto` 会通过 `tools/protogen` 自动适配 Windows、macOS、Linux。
Windows 仍需要安装 GNU Make；如果没有 `make`，可以直接执行等价的 Go 命令，例如 `go run ./tools/protogen`、`go test ./...`、`go run ./cmd/gateway`。

启动 HTTP gateway：

```bash
make run-gateway
```

端口监听成功后，控制台会输出：

```text
[Gateway Started]
  service: gateway
  env: local
  listen: 0.0.0.0:8080
  http: http://127.0.0.1:8080
  health: http://127.0.0.1:8080/healthz
  api: http://127.0.0.1:8080/api/v1
```

如果端口被占用，控制台会输出 `[Gateway Start Failed]`，并显示失败的监听地址和系统错误。

健康检查：

```bash
curl http://localhost:8080/healthz
```

## 题目 09 业务功能（已实现）

启动后打开页面：

- 业务操作页：<http://127.0.0.1:8080/web/index.html>（申请、审批、试点、推广、停止、恢复、逐车核查、异常留痕）
- 模拟车辆控制台：<http://127.0.0.1:8080/web/vehicle.html>（拉取配置、成功/失败/重复/跨车/未确认回执）
- 交互原型（静态）：`docs/09-原型.html`

预置账号（演示用，令牌仅本机演示）：研发申请人 `demo-token-applicant`、审批负责人 `demo-token-approver`、发布运营 `demo-token-operator`；车辆凭证 `veh-token-01`～`veh-token-05`。鉴权均通过请求头 `X-Auth-Token` / `X-Vehicle-Token`，服务端按令牌解析身份，不接受请求体中的用户或车辆编号。

运行参数（环境变量）：

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `FLEETLOG_DB_PATH` | `data/fleetlog.db` | SQLite 文件位置（纯 Go 驱动，无需 CGO） |
| `FLEETLOG_TIMEOUT_SECONDS` | `60` | 教学超时阈值；演示缩短时须在讲解与截图中注明 |

主要接口：

```text
POST /api/v1/applications                 # 创建申请（申请人）
POST /api/v1/applications/:id/resubmit    # 退回后重提（保留历史版本）
POST /api/v1/applications/:id/decide      # 批准/退回（负责人，审批人≠申请人）
GET  /api/v1/applications[?/:id]
GET  /api/v1/campaigns/latest             # 活动总览 + 逐车核查 + 恢复结果
POST /api/v1/campaigns/:id/pilot          # 试点 2 台（运营）
POST /api/v1/campaigns/:id/promote        # 推广剩余 3 台（试点未完成拒绝并留痕）
POST /api/v1/campaigns/:id/stop           # 停止（负责人，保留已发/未发范围）
POST /api/v1/campaigns/:id/restore        # 批准恢复（更高版本，目标=活动前值）
POST /api/v1/campaigns/:id/complete       # 结案（全部成功才允许）
POST /api/v1/campaigns/:id/restore/complete  # 恢复完成（全部确认才允许）
GET  /api/v1/vehicle/config               # 车辆拉取（仅本车最新已发布版本）
POST /api/v1/vehicle/receipts             # 车辆回执（幂等、版本单调、跨车/未知拒绝）
GET  /api/v1/exceptions | /api/v1/audits  # 异常留痕与审计
```

数据模型要点：`vehicle` 保存 `applied_interval/applied_version`（生效值）与 `latest_version`（每车版本单调游标）；`vehicle_release` 保存期望值 `target_interval/version_no`、下发与回执时间、超时与迟到成功标记；失败回执不修改生效值；恢复是新版本下发而非撤销记录。

## 目录结构

```text
api/proto      # 新增 gRPC proto 时放这里
api/gen        # protoc 生成代码
cmd/gateway    # Gin HTTP 网关入口
configs        # YAML 配置
internal       # 业务代码，按服务拆分
pkg            # 公共工具包
docs           # 使用和架构文档
```

新增业务服务时使用脚手架命令：

```bash
bw-cli service order --tidy
```

命令会生成 `cmd/order`、`api/proto/order`、`internal/order/entity`、`internal/order/model`、`internal/order/dto`、`internal/order/service/service.go`、`repo/gorm_repository.go`、`repo/mongo_repository.go`、`handler`、gateway request/handler/router 和 `docs/services/order.md`。生成后的服务默认带 Create/Get/List/Update/Delete 基础 CRUD，并会把服务名、gRPC 端口和 gateway target 写入 `configs/config.yaml`。默认启动使用 Gorm，MongoDB 仓储已通过 `mongox.NewDocumentStore[T]` 预先接好。

需要指定端口时使用：

```bash
bw-cli service order --port 9103 --tidy
```

启动后控制台会输出服务名、环境、监听地址、gRPC 地址和配置项。

绑定已有单表时使用：

```bash
bw-cli service order --table orders --tidy
```

配置单表或多表关联时，可以打开本地可视化设计器：

```bash
bw-cli designer --dir . --addr 127.0.0.1:6060
```

设计器默认使用 `mysql`，DSN 默认示例为：

```text
账号:密码@tcp(服务器IP:3306)/数据库?charset=utf8mb4&parseTime=True&loc=Local
```

界面保存的计划位于 `scaffold-plans/<service>.json`，也可以通过命令行生成：

```bash
bw-cli service product --plan scaffold-plans/product.json --tidy
```

单表计划只选择一张表即可。多表计划会额外生成 `internal/<service>/repo/relationships.go`，包含所选表、表关系和 Gorm Join 查询入口。

如果项目包含 gateway，HTTP CRUD 路由也会自动挂载：

```text
POST   /api/v1/orders
GET    /api/v1/orders
GET    /api/v1/orders/:id
PUT    /api/v1/orders/:id
DELETE /api/v1/orders/:id
```

需要演示项目时请使用：

```bash
bw-cli demo demo-service --module github.com/acme/demo-service --tidy
```
