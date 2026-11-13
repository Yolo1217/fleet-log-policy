# Skill 使用记录（题目 09）

> 只记录本项目中真实调用过的 Skill。未实际调用的不列入。调用时间：2026-11-13（本地时区 UTC+8）。

## 1. fleet-log-policy-check（自编：业务专项）

| 说明项 | 内容 |
| --- | --- |
| 名称、来源与版本 | `fleet-log-policy-check` v0.1.0，自编。文件：`.codebuddy/skills/fleet-log-policy-check/`（`SKILL.md`、`references/business-rules.md`、`scripts/check_business_rules.py`） |
| 使用位置 | 开发阶段收尾：题目 09 规则改造与缺陷修复后的业务回归；本次会话（桌面副本 `D:\Desktop\fleet-log-policy`，分支 `dev/09`） |
| 使用原因 | 单元测试只能验证包内行为，无法证明"真实 HTTP 流程 + 时序"下题面规则 1～8 同时成立（尤其超时、迟到成功、停止后恢复、未确认项口径）。需要一个可重复执行、只看接口事实的核查器，避免"看代码猜行为" |
| 使用过程 | 输入：`--start`（脚本自行构建网关，临时配置改到 18080 端口、临时 SQLite、教学超时 3 秒）+ `--report docs/evidence/business-check-2026-11-13.json`。脚本按"异常申请 → 审批 → 试点 → 回执失败/成功/重复/跨车/未知 → 超时 → 迟到成功 → 停止 → 恢复 → 正常推广 → 结案"推进 49 条断言 |
| 实际帮助 | ① 首轮运行暴露真实缺陷：被拒绝的请求若带请求体，服务端未读完 body 即断开，客户端偶发 `ConnectionResetError`（同一请求重试 3 次仍失败；无请求体时 6/6 成功），导致 401/403 响应丢失；定位后在 `handler.fail` 增加 `drainBody`（消费未读 body 再返回错误），复检通过。② 修正了脚本自身 3 处错误断言口径（`applied`→`applied_interval`、恢复目标字段、第二批试点车的版本预期），避免用错误期望"证明"通过 |
| 证据及局限 | 输出：`docs/evidence/business-check-2026-11-13.json`（SQLite 隔离库 49/49 PASS）、`docs/evidence/business-check-mysql.json`（真实 MySQL 独立 schema `questions_check` 49/49 PASS，核查后已删除该 schema）；修复提交见 `dev/09` 的 `fix v0.1.0 ...`；修复前失败输出在会话记录中。局限：脚本单线程串行、不覆盖并发竞争（并发由 Go 用例验证）；`--start` 依赖本机端口可用；对已在运行的服务需人工保证 `--timeout-seconds` 与服务端一致；不替代 `-race` |

## 2. fleet-log-acceptance（自编：验收检查）

| 说明项 | 内容 |
| --- | --- |
| 名称、来源与版本 | `fleet-log-acceptance` v0.1.0，自编。文件：`.codebuddy/skills/fleet-log-acceptance/`（`SKILL.md`、`references/acceptance-checklist.md`、`scripts/collect_evidence.py`） |
| 使用位置 | 提交前自检（T17/T18 之间的门禁）；本次会话同一分支 `dev/09` |
| 使用原因 | 题面验收涉及 Git 流程、必需文件、测试与覆盖率、边界用例、Skill 记录、CI 等分散项，人工逐条核对易漏；需要一个"同一口径、可复算"的证据采集器，也便于整改后复检 |
| 使用过程 | 输入：`--report docs/evidence/acceptance-report.md`（默认门禁 85%）。脚本执行：分支/工作区/main 提交数/推送一致性 → 必需文件 13 项 → 两个 Skill 的七要素 → `go build`/`go vet`/`go test -coverpkg` 覆盖率 → 边界关键词 → `.github/workflows` 与部署证据 |
| 实际帮助 | 查出 3 个真实问题：① 缺少 `skill-usage.md`；② 缺少 CI 工作流（`.github/workflows` 不存在）；③ 工作区有未提交改动。整改：补齐本文件、新增 `.github/workflows/ci.yml`（test + 覆盖率门禁 + 业务 Skill 黑盒 job）、提交全部改动后复跑。另发现脚本自身两处误判：`git log main` 取不到时被判 PASS（假通过）、`subprocess` 默认 GBK 解码导致提交信息解析为空而假 PASS；已改为解析 `main`/`origin/main` 并显式 UTF-8 解码 |
| 证据及局限 | 整改前报告：`docs/evidence/acceptance-report-before-fix.md`；整改后报告：`docs/evidence/acceptance-report.md`。局限：不判定"自动部署成功"，只标记 WARN 要求人工补证；`-race` 在本机不可用（无 CGO/gcc）只能记 WARN；覆盖率按语句统计，不评价测试有效性 |

## 3. skill-creator（既有：CodeBuddy 内置）

| 说明项 | 内容 |
| --- | --- |
| 名称、来源与版本 | `skill-creator`，CodeBuddy 内置既有 Skill；版本号工具未暴露（不可获取）。位置：工具内置目录，本次调用记录在会话中（`use_skill("skill-creator")`） |
| 使用位置 | 设计上述两个自编 Skill 之前 |
| 使用原因 | 需要确认 Skill 的目录规范（用户级 `~/.codebuddy/skills/<name>/` 与项目级 `.codebuddy/skills/<name>/`）、`SKILL.md` frontmatter 必填字段，以及"元数据常驻、正文按需、资源按需"的分层写法，避免写成一段角色提示词 |
| 使用过程 | 输入：题目 09 的规则与验收要求 + 需要同时交付业务/验收两个 Skill；按其指引确定 `SKILL.md`（name/description + 用途/触发/输入/步骤/输出/失败处理/完成标准）+ `references/`（规则清单、验收清单）+ `scripts/`（可执行核查脚本）结构 |
| 实际帮助 | 统一了两个 Skill 的结构与存放位置，明确了"项目级 Skill 入库、其余 `.codebuddy` 本地状态忽略"的处理方式（见 `.gitignore`）；避免把脚本与规则全部塞进 SKILL.md 造成上下文膨胀 |
| 证据及局限 | 证据：会话中两次 `use_skill` 调用及 `SKILL.md` 文件；局限：它只提供写法规范，不含本题业务口径，业务规则完全来自题面与实测，不能替代调试 |

## 4. 调用方式（工具中的配置）

- 项目级（推荐，随仓库分发）：Skill 位于 `.codebuddy/skills/fleet-log-policy-check/` 与 `.codebuddy/skills/fleet-log-acceptance/`，在仓库根目录打开 CodeBuddy 即可被识别。
- 用户级（本机立即生效）：将两个目录复制到 `C:\Users\Yolo\.codebuddy\skills\`，本次即以此方式通过 `use_skill` 实际加载成功。
- 纯命令行执行（不依赖 AI 工具）：`python .codebuddy/skills/<skill>/scripts/*.py`，参数见各 `SKILL.md`。
- 实际调用记录：本会话中 `use_skill("skill-creator")`、`use_skill("fleet-log-policy-check")`、`use_skill("fleet-log-acceptance")` 均有工具返回；脚本运行输出见 `docs/evidence/`。

## 5. 一次完整的"发现问题 → 修正 → 复检"链路

1. 发现问题（业务 Skill）：`check_business_rules.py --start` 在 403 断言处连续 3 次 `ConnectionResetError`，同时网关日志显示 3 次请求均已返回 403 → 判定为服务端未读 body 提前断开。
2. 学生指导修正：确认是服务端问题（客户端无请求体时 6/6 成功），要求修服务端而不是改断言。
3. 修正：`internal/fleetlog/handler/handler.go` 增加 `drainBody`，在 `fail()` 返回错误前消费未读请求体。
4. 复检：同一命令重跑，49 项全 PASS（`docs/evidence/business-check-2026-11-13.json`），`go test ./internal/fleetlog/...` 仍全部通过。
5. 验收 Skill 复检：由 FAIL（缺 `skill-usage.md`、缺 CI、工作区脏）整改为仅剩 WARN（`-race` 环境限制、部署证据需人工提供）。
