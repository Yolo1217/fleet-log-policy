# 题目 09 验收证据

| 类别 | 检查项 | 结论 | 证据 |
| --- | --- | --- | --- |
| Git | 当前分支为 dev/09 | PASS | HEAD 分支=dev/09 |
| Git | 工作区干净 | PASS | 无未提交改动 |
| Git | main 仅初始化提交 | PASS | main 提交数=1；9e83732 docs v0.1.0 初始化 bw-cli 脚手架与项目说明 |
| Git | dev/09 已推送到远程 | WARN | 本地=9e9b6ba origin/dev/09=f7a29e2 |
| Git | feat/fix 提交携带工作项编号 | WARN | 7/7 条缺编号（需在记录中说明）：feat v0.1.0 新增部署与回滚脚本并支持部署端口覆盖 |
| 交付物 | 必需文件齐全 | PASS | 全部存在 |
| Skill | fleet-log-policy-check 结构完整（七要素） | PASS | 七要素齐全 |
| Skill | fleet-log-acceptance 结构完整（七要素） | PASS | 七要素齐全 |
| Skill | skill-usage.md 覆盖自编 Skill | PASS | 两个自编 Skill 均有记录 |
| 测试 | go build ./... 通过 | PASS | exit 0 |
| 测试 | go vet ./... 通过 | PASS | exit 0 |
| 测试 | 单元测试全部通过 | PASS | 全部包 ok |
| 覆盖率 | 整体覆盖率 ≥85.0% | PASS | 88.5%；entity 2.3%；handler 77.0%；model 0.9%；repo 14.3%；service 65.4%；0% 函数 1 个（如 	TimeoutSeconds） |
| 测试 | 竞态检测 -race | WARN | 环境不支持（需 CGO/gcc）：go: -race requires cgo; enable cgo by setting CGO_ENABLED=1 |
| 测试 | 边界用例覆盖 | PASS | 测试函数 32 个；命中边界关键词 9/9：失败, 超时, 重复, 跨车, 旧版本, 停止, 恢复, 结案, 并发 |
| 流程 | CI 工作流存在 | PASS | ci.yml |
| 流程 | CI 包含测试与覆盖率门禁 | PASS | go test=True coverage=True |
| 流程 | 自动部署证据 | WARN | 需人工提供部署链接与流水线运行记录；本脚本不判定部署成功 |

汇总：PASS 14 / WARN 4 / FAIL 0
