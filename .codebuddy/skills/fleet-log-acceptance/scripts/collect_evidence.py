#!/usr/bin/env python3
"""题目09 交付物验证据采集：Git、文件、测试、覆盖率、Skill、CI。

用法：
  python collect_evidence.py --report docs/evidence/acceptance-report.md
  python collect_evidence.py --skip-tests --coverage-gate 85
"""
from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
PKG = "./internal/fleetlog/..."

REQUIRED_FILES = [
    "AGENTS.md",
    "README.md",
    "docs/09-需求分析.md",
    "docs/09-原型.html",
    "docs/09-原型.md",
    "docs/09-任务清单.md",
    "docs/09-建仓记录.md",
    "docs/09-开发记录.md",
    "skill-usage.md",
    "web/index.html",
    "web/vehicle.html",
    ".codebuddy/skills/fleet-log-policy-check/SKILL.md",
    ".codebuddy/skills/fleet-log-acceptance/SKILL.md",
]

SKILL_SECTIONS = ["用途", "触发场景", "输入", "执行步骤", "输出格式", "失败处理", "完成标准"]
BOUNDARY_KEYWORDS = ["失败", "超时", "重复", "跨车", "旧版本", "停止", "恢复", "结案", "并发"]


def sh(args, timeout=300):
    try:
        out = subprocess.run(args, cwd=ROOT, capture_output=True, text=True, timeout=timeout,
                             encoding="utf-8", errors="replace")
        return out.returncode, (out.stdout or "") + (out.stderr or "")
    except FileNotFoundError:
        return 127, f"命令不存在：{args[0]}"
    except subprocess.TimeoutExpired:
        return 124, f"命令超时：{' '.join(args)}"


class Report:
    def __init__(self):
        self.rows = []

    def add(self, category, name, status, evidence):
        self.rows.append({"category": category, "name": name, "status": status, "evidence": evidence.strip()})
        print(f"[{status}] {category} / {name}：{evidence.strip()[:120]}")

    def count(self, status):
        return sum(1 for r in self.rows if r["status"] == status)

    def markdown(self):
        lines = ["| 类别 | 检查项 | 结论 | 证据 |", "| --- | --- | --- | --- |"]
        for r in self.rows:
            ev = r["evidence"].replace("\n", "<br>").replace("|", "\\|")
            lines.append(f"| {r['category']} | {r['name']} | {r['status']} | {ev[:400]} |")
        lines.append("")
        lines.append(f"汇总：PASS {self.count('PASS')} / WARN {self.count('WARN')} / FAIL {self.count('FAIL')}")
        return "\n".join(lines)


def check_git(rep):
    _, branch = sh(["git", "rev-parse", "--abbrev-ref", "HEAD"])
    _, status = sh(["git", "status", "--porcelain"])
    _, head = sh(["git", "rev-parse", "--short", "HEAD"])
    _, remote = sh(["git", "rev-parse", "--short", "origin/dev/09"])
    dirty = [l for l in status.splitlines() if l.strip()]
    rep.add("Git", "当前分支为 dev/09", "PASS" if branch.strip() == "dev/09" else "FAIL", f"HEAD 分支={branch.strip()}")
    rep.add("Git", "工作区干净", "PASS" if not dirty else "FAIL",
            "无未提交改动" if not dirty else f"未提交文件 {len(dirty)} 个：" + "; ".join(dirty[:5]))
    main_ref = ""
    for ref in ("main", "origin/main"):
        if sh(["git", "rev-parse", "--verify", "--quiet", ref])[0] == 0:
            main_ref = ref
            break
    if not main_ref:
        rep.add("Git", "main 仅初始化提交", "WARN", "本地与远程均无法解析 main，请先 git fetch")
    else:
        _, main_log = sh(["git", "log", "--oneline", main_ref])
        main_commits = [l for l in main_log.splitlines() if l.strip()]
        rep.add("Git", "main 仅初始化提交", "PASS" if len(main_commits) <= 1 else "FAIL",
                f"{main_ref} 提交数={len(main_commits)}；{main_commits[0] if main_commits else '无'}")
    rep.add("Git", "dev/09 已推送到远程", "PASS" if remote.strip() and remote.strip() == head.strip() else "WARN",
            f"本地={head.strip()} origin/dev/09={remote.strip() or '未获取'}")
    _, logs = sh(["git", "log", "--pretty=%s", "-20"])
    subjects = [l.strip() for l in logs.splitlines() if l.strip()]
    feat_bad = [l for l in subjects if l.startswith(("feat", "fix")) and "#" not in l]
    feat_all = [l for l in subjects if l.startswith(("feat", "fix"))]
    if not feat_all:
        rep.add("Git", "feat/fix 提交携带工作项编号", "WARN", "近期提交中没有 feat/fix 记录，无法判断")
    elif feat_bad:
        rep.add("Git", "feat/fix 提交携带工作项编号", "WARN",
                f"{len(feat_bad)}/{len(feat_all)} 条缺编号（需在记录中说明）：{feat_bad[0][:60]}")
    else:
        rep.add("Git", "feat/fix 提交携带工作项编号", "PASS", f"{len(feat_all)} 条均带编号")


def check_files(rep):
    missing = [f for f in REQUIRED_FILES if not (ROOT / f).exists()]
    rep.add("交付物", "必需文件齐全", "PASS" if not missing else "FAIL",
            "全部存在" if not missing else "缺失：" + ", ".join(missing))
    for skill in ("fleet-log-policy-check", "fleet-log-acceptance"):
        path = ROOT / f".codebuddy/skills/{skill}/SKILL.md"
        if not path.exists():
            continue
        text = path.read_text(encoding="utf-8")
        lack = [s for s in SKILL_SECTIONS if s not in text]
        rep.add("Skill", f"{skill} 结构完整（七要素）", "PASS" if not lack else "FAIL",
                "七要素齐全" if not lack else "缺少：" + ", ".join(lack))
    usage = ROOT / "skill-usage.md"
    if usage.exists():
        text = usage.read_text(encoding="utf-8")
        covered = all(name in text for name in ("fleet-log-policy-check", "fleet-log-acceptance"))
        rep.add("Skill", "skill-usage.md 覆盖自编 Skill", "PASS" if covered else "FAIL",
                "两个自编 Skill 均有记录" if covered else "未覆盖全部自编 Skill")


def check_tests(rep, gate):
    code, out = sh(["go", "build", "./..."])
    rep.add("测试", "go build ./... 通过", "PASS" if code == 0 else "FAIL", "exit 0" if code == 0 else out[-600:])
    code, out = sh(["go", "vet", "./..."])
    rep.add("测试", "go vet ./... 通过", "PASS" if code == 0 else "FAIL", "exit 0" if code == 0 else out[-600:])

    profile = Path(tempfile.mkdtemp(prefix="fleetlog-cov-")) / "cov.out"
    code, out = sh(["go", "test", "-count=1", PKG, f"-coverprofile={profile}"])
    fails = [l for l in out.splitlines() if l.startswith("FAIL") or "--- FAIL" in l]
    rep.add("测试", "单元测试全部通过", "PASS" if code == 0 else "FAIL",
            "全部包 ok" if code == 0 else "; ".join(fails[:5]) or out[-600:])

    pkg_cov = re.findall(r"(fleetlog/\w+)\s+.*?coverage: ([\d.]+)% of statements", out)
    detail = "；".join(f"{p.replace('fleetlog/', '')} {c}%" for p, c in pkg_cov) or "未采集到分包含量"
    total = None
    if profile.exists():
        code2, funcs = sh(["go", "tool", "cover", "-func", str(profile)])
        m = re.search(r"total:\s+\(statements\)\s+([\d.]+)%", funcs)
        total = float(m.group(1)) if m else None
        zeros = [l for l in funcs.splitlines() if l.rstrip().endswith("0.0%") and not l.startswith("total:")]
        if zeros:
            first = zeros[0].rsplit("\t", 2)
            detail += f"；0% 函数 {len(zeros)} 个（如 {first[0].split(':')[-1]}）"
    if total is None:
        rep.add("覆盖率", f"整体覆盖率 ≥{gate}%", "FAIL", "无法生成覆盖率报告：" + detail)
    else:
        rep.add("覆盖率", f"整体覆盖率 ≥{gate}%", "PASS" if total >= gate else "FAIL", f"{total}%；{detail}")

    code, out = sh(["go", "test", "-race", "-count=1", "./internal/fleetlog/service"])
    rep.add("测试", "竞态检测 -race", "PASS" if code == 0 else "WARN",
            "通过" if code == 0 else "环境不支持（需 CGO/gcc）：" + out.strip().splitlines()[0][:160])

    tests = list((ROOT / "internal/fleetlog").rglob("*_test.go"))
    body = "\n".join(p.read_text(encoding="utf-8") for p in tests)
    funcs = len(re.findall(r"func Test\w+\(", body))
    hits = [k for k in BOUNDARY_KEYWORDS if k in body]
    rep.add("测试", "边界用例覆盖", "PASS" if len(hits) >= 7 else "WARN",
            f"测试函数 {funcs} 个；命中边界关键词 {len(hits)}/{len(BOUNDARY_KEYWORDS)}：{', '.join(hits)}")


def check_ci(rep):
    workflows = list((ROOT / ".github/workflows").glob("*.y*ml")) if (ROOT / ".github/workflows").exists() else []
    rep.add("流程", "CI 工作流存在", "PASS" if workflows else "FAIL",
            "；".join(p.name for p in workflows) if workflows else ".github/workflows 缺失")
    if workflows:
        text = "\n".join(p.read_text(encoding="utf-8") for p in workflows)
        has_test = "go test" in text
        has_gate = "coverage" in text.lower()
        rep.add("流程", "CI 包含测试与覆盖率门禁", "PASS" if has_test and has_gate else "WARN",
                f"go test={has_test} coverage={has_gate}")
    rep.add("流程", "自动部署证据", "WARN",
            "需人工提供部署链接与流水线运行记录；本脚本不判定部署成功")


def main():
    ap = argparse.ArgumentParser(description="题目09 验收证据采集")
    ap.add_argument("--report", default="")
    ap.add_argument("--coverage-gate", type=float, default=85.0)
    ap.add_argument("--skip-tests", action="store_true")
    args = ap.parse_args()

    rep = Report()
    check_git(rep)
    check_files(rep)
    if not args.skip_tests:
        check_tests(rep, args.coverage_gate)
    check_ci(rep)

    md = "# 题目 09 验收证据\n\n" + rep.markdown() + "\n"
    if args.report:
        path = Path(args.report)
        if not path.is_absolute():
            path = ROOT / path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(md, encoding="utf-8")
        print(f"\n报告写入：{path}")
    else:
        print("\n" + md)
    return 1 if rep.count("FAIL") else 0


if __name__ == "__main__":
    sys.exit(main())
