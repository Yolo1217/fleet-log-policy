# 独立测试 Agent 会话报告（题目 09）

> 上下文独立性声明：本报告的产出者是**独立上下文的测试 Agent 子代理**（与开发 Agent 不共享上下文），只接收"仓库路径 + 题面路径 + 复核要求"，不接收开发 Agent 的完成声明；禁止修改文件、禁止 git 写操作、禁止启停服务。
> 复核对象：`D:\Desktop\fleet-log-policy` @ `dev/09` / `c2b2a51`（工作区干净）。
> 复核时间：2026-11-13。工具：CodeBuddy 子代理（模型版本字段不可获取）。

## 一、执行的真实命令与结果

| # | 命令 | 关键输出 |
| --- | --- | --- |
| 1 | `git status` / `git branch --show-current` / `git log --oneline -5` | 分支 `dev/09`，工作区干净，HEAD `c2b2a51` |
| 2 | `go vet ./...` | 无输出，exit 0 |
| 3 | `go test -count=1 ./internal/fleetlog/... -coverpkg=./internal/fleetlog/... -coverprofile=%TEMP%\agent-cov.out` | 5 包全部 `ok`，exit 0 |
| 4 | 同上，改为不带 `-coverpkg` 落盘对照 | 带 `-coverpkg` 时 profile 未落盘（本机 Go 1.27.1 该参数组合问题），不带时可落盘 → 判定为**本机工具链现象，非仓库缺陷**，并据此改用"先落盘再聚合"的取证方式 |
| 5 | `go test -count=1 ./internal/fleetlog/... -coverprofile=%TEMP%\agent-cov3.out` + `go tool cover -func` | total **87.8%**（未含 `-coverpkg` 口径），用于交叉验证 |
| 6 | `go test -count=1 -v ./internal/fleetlog/service/` | 14 个 `--- PASS`，0 FAIL |
| 7 | 统计 `func Test` / 并发命名 / `t.Run` | fleetlog **32 个测试函数**，2 个并发用例，仅 1 处 `t.Run`；全仓 18 个测试文件 / 77 个 `func Test` |
| 8 | 结构化核对 `docs/evidence/business-check-2026-11-13.json` 与 `business-check-mysql.json` | 两侧均 `total=49 / failed=0`，逐条 `rule/name/ok/evidence` 差异条数 **0** |
| 9 | 只读 HTTP 探测（GET，不改数据） | `/healthz` ok；**匿名**取得 `demo-token-*` 三个角色令牌与 `veh-token-01..05` |
| 10 | 外部核验 GitHub Actions `runs/43375442566` | 存在，`11185e4`，`dev/09`，**success**，4 个 job 全绿 |

## 二、发现的问题（已按严重度排）

| # | 问题 | 证据 | 影响 | 处置 |
| --- | --- | --- | --- | --- |
| T-1 | **演示凭据接口未限制来源**：`/api/v1/demo/accounts`、`/api/v1/demo/vehicles` 匿名即可取得全部角色令牌 | 只读 HTTP 实测 | 等于放弃"身份只认令牌"，任意来源可"领取"审批人身份 | 已修复：`handler.localOnly` 仅回环地址可访问（不信任 `X-Forwarded-For`），补 2 条 HTTP 测试 |
| T-2 | **覆盖率证据口径冲突**：`coverage.txt` 88.5%、`unit-test-output.txt`（entity 2.3%/service 65.4%）、测试报告（89.9%/86.1%）三套数值并存 | 三份文件对读 | 验收方无法判断真实覆盖率，属于数据自洽缺陷 | 已修复：统一"按代码块去重求和"口径，新增 `coverage-per-package.txt`，报告数值改为 100/100/89.8/89.0/87.0、合计 88.5% |
| T-3 | 边界 15（服务重启后持久化）无自动断言，仅开发记录里的手工复查 | `docs/09-开发记录.md`；业务核查 JSON 无对应项 | 重启类回归靠人工，容易漏 | 记入风险清单（本轮未补自动化，理由：需真实进程重启，属部署脚本职责） |
| T-4 | 业务核查 JSON 未记录被测提交版本 | `business-check-*.json` 字段 | 证据与代码版本无法绑定 | 记入风险清单，报告内以提交号人工绑定 |
| T-5 | 49 项断言未与需求分析的 15 条边界逐条映射 | 报告与 JSON 对照 | 边界覆盖靠"规则"维度而非"边界"维度自证 | 记入风险清单 |

## 三、独立设计的边界用例与覆盖判定

| 用例 | 现有测试（真实函数名） | 判定 |
| --- | --- | --- |
| 重复推送同一版本 | `TestReceiptRules`、`TestConcurrentReceiptsIdempotent` | PASS |
| 试点车超时后迟到成功 | `TestLateSuccessAfterTimeout`、`TestPilotTimeoutPausesCampaign` | PASS |
| 停止/恢复范围与目标 | `TestStopAndRestore`、`TestFullRecoveryNeedsActivity` | PASS |
| 旧版本回执单调性 | `TestOldVersionReceiptIgnored` | PASS |
| 跨车回执、未知版本回执 | `TestReceiptRules`（含 403/404 分支）+ 业务核查「跨车回执拒绝」「未知版本拒绝」 | PASS |
| 审批并发只保留一个未结束活动 | `TestConcurrentApproveKeepsSingleOpenCampaign` | PASS |
| 结案需全部确认 | `TestCompleteRequiresAllSucceeded` | PASS |
| 试点未成功即推广被拒 | `TestPilotAndPromote` + 业务核查「试点未成功即推广被拒且留痕」 | PASS |
| 服务重启后状态持久化 | 无自动断言 | **GAP**（T-3） |
| 演示接口被非本机来源调用 | 修复前无 | 修复后新增 2 条断言（回环 200 / 非回环 403） |
| 停止未填原因 | 业务核查「停止未填原因应 400」 | PASS |

## 四、结论

**可接受，但需带条件**：业务规则与测试主体通过（5 包全绿、32 个测试函数、49/49 双库一致、CI `-race` 绿），并已按本报告修复 T-1（安全）与 T-2（证据自洽）；T-3/T-4/T-5 属证据完善类风险，已在测试报告中如实登记，未编造为已解决。
