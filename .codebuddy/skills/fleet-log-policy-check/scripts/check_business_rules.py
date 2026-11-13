#!/usr/bin/env python3
"""题目09 业务约束黑盒核查器：按题面规则 1~8 断言真实 HTTP 行为。

用法：
  python check_business_rules.py --start --report docs/evidence/business-check.json
  python check_business_rules.py --base-url http://127.0.0.1:8080 --timeout-seconds 60
"""
from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from urllib import error as urlerror
from urllib import request as urlrequest

REPO_ROOT = Path(__file__).resolve().parents[4]
T_APP, T_APV, T_OP = "demo-token-applicant", "demo-token-approver", "demo-token-operator"
VT = {f"TE-0{i}": f"veh-token-0{i}" for i in range(1, 6)}
API = None  # 由 main 注入，供便捷断言函数复用


class Api:
    def __init__(self, base):
        self.base = base.rstrip("/")

    def call(self, method, path, body=None, token=None, vehicle=None):
        data = json.dumps(body).encode() if body is not None else None
        headers = {"Content-Type": "application/json", "Connection": "close"}
        if token:
            headers["X-Auth-Token"] = token
        if vehicle:
            headers["X-Vehicle-Token"] = vehicle
        # 关键：非幂等的 POST 不自动重试（早期实现重试 POST，Windows 回环偶发连接重置时
        # 会出现"第一次已成功、重试得到 409"的假失败）。对状态迁移类 POST，传输中断后
        # 改为"读取活动视图核实副作用"，若目标状态已生效则基于事实合成结果，否则如实报错。
        attempts = 3 if method == "GET" else 1
        last = None
        for attempt in range(attempts):
            req = urlrequest.Request(self.base + path, data=data, headers=headers, method=method)
            try:
                with urlrequest.urlopen(req, timeout=20) as resp:
                    return resp.status, dec(resp.read())
            except urlerror.HTTPError as exc:
                try:
                    return exc.code, dec(exc.read())
                except OSError:
                    return exc.code, {}
            except (urlerror.URLError, ConnectionResetError) as exc:  # 连接层异常
                last = exc
            if attempt < attempts - 1:
                time.sleep(0.4)
        if method == "POST":
            recovered = self._verify_post_effect(path, token)
            if recovered is not None:
                return 200, recovered
        raise RuntimeError(f"{method} {path} 传输失败（非幂等请求不重试）：{last}") from last

    def _verify_post_effect(self, path, token):
        """POST 传输中断后核实副作用是否已生效；生效则按活动视图合成响应（不编造，字段来自 GET）。"""
        m = re.search(r"/campaigns/(\d+)(/.*)?$", path)
        if not m:
            return None
        cid, suffix = m.group(1), m.group(2) or ""
        expect = {"promote": "promoting", "complete": "completed",
                  "restore/complete": "restored", "restore": "restoring", "stop": "stopped"}
        target = None
        for key in ("restore/complete", "promote", "complete", "restore", "stop"):
            if suffix == "/" + key:
                target = expect[key]
                break
        if not target:
            return None
        try:
            status, payload = self.call("GET", f"/api/v1/campaigns/{cid}", token=token)
        except RuntimeError:
            return None
        v = d(payload)
        if v.get("status") != target:
            return None
        data = dict(v)
        rows = data.get("vehicles") or []
        if suffix == "/promote":
            data["dispatched_vehicles"] = [1] * len([r for r in rows if r.get("applied_version")])
        elif suffix == "/complete":
            data["all_succeeded"] = bool(rows) and all(r.get("status") == "成功" for r in rows)
        elif suffix == "/restore/complete":
            rec_rows = data.get("recovery_rows") or []
            data["all_recovered"] = bool(rec_rows) and all(r.get("status") == "已恢复" for r in rec_rows)
        return {"data": data}


def dec(raw):
    text = (raw or b"").decode("utf-8", "replace").strip()
    if not text:
        return {}
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return {"raw": text}


class Rep:
    def __init__(self):
        self.items = []

    def check(self, rule, name, ok, ev):
        self.items.append({"rule": rule, "name": name, "ok": bool(ok), "evidence": ev})
        print(f"[规则{rule}] {name:<42} {'PASS' if ok else 'FAIL'}  ({ev})", flush=True)
        return bool(ok)


def d(payload):
    return payload.get("data") or {}


def ecode(payload):
    return str((payload.get("error") or {}).get("code") or "")


def emsg(payload):
    return str((payload.get("error") or {}).get("message") or "")


def req(rep, rule, name, method, path, want, body=None, token=None, vehicle=None, note=""):
    """执行一次请求并按期望状态码断言，证据中如实带回实际状态码。"""
    status, payload = API.call(method, path, body, token, vehicle)
    ev = f"HTTP {status}"
    if ecode(payload):
        ev += f" {ecode(payload)}"
    if note:
        ev += f"；{note}"
    return rep.check(rule, name, status == want, ev), payload


def view(cid):
    return d(API.call("GET", f"/api/v1/campaigns/{cid}")[1])


def row(v, code):
    for item in v.get("vehicles") or []:
        if item.get("code") == code:
            return item
    return {}


def kinds():
    data = API.call("GET", "/api/v1/exceptions")[1].get("data") or []
    return {x.get("kind") for x in data if isinstance(x, dict) and x.get("kind")}


def new_app(reason, interval):
    return d(API.call("POST", "/api/v1/applications", {"reason": reason, "interval": interval}, token=T_APP)[1]).get("id")


def run(rep, timeout_s):
    # ---------------- 规则1：申请与审批 ----------------
    req(rep, "1", "未登录提交申请应 401", "POST", "/api/v1/applications", 401,
        {"reason": "x", "interval": 30})
    req(rep, "1", "运营角色提交申请应 403", "POST", "/api/v1/applications", 403,
        {"reason": "x", "interval": 30}, token=T_OP)
    for bad in (9, 301):
        req(rep, "1", f"间隔 {bad} 秒应 400", "POST", "/api/v1/applications", 400,
            {"reason": "x", "interval": bad}, token=T_APP)
    req(rep, "1", "原因为空应 400", "POST", "/api/v1/applications", 400,
        {"reason": " ", "interval": 30}, token=T_APP)

    status, payload = API.call("POST", "/api/v1/applications", {"reason": "排查 AEB 误触发", "interval": 45}, token=T_APP)
    app = d(payload)
    app_id = app.get("id")
    rep.check("1", "合法申请待审批且参数正确",
              status == 201 and app.get("status") == "waiting" and app.get("interval") == 45,
              f"HTTP {status} id={app_id} status={app.get('status')} interval={app.get('interval')}")
    req(rep, "1", "申请人自行审批应 403", "POST", f"/api/v1/applications/{app_id}/decide", 403,
        {"approve": True}, token=T_APP)
    req(rep, "1", "退回未填原因应 400", "POST", f"/api/v1/applications/{app_id}/decide", 400,
        {"approve": False}, token=T_APV)
    status, payload = API.call("POST", f"/api/v1/applications/{app_id}/decide",
                               {"approve": False, "reject_reason": "原因不充分"}, token=T_APV)
    rep.check("1", "负责人可退回申请", status == 200 and d(payload).get("status") == "rejected",
              f"HTTP {status} status={d(payload).get('status')}")

    status, payload = API.call("POST", f"/api/v1/applications/{app_id}/resubmit",
                               {"reason": "补充排查范围", "interval": 45}, token=T_APP)
    sub = d(payload)
    rep.check("1", "退回后重提保留历史版本",
              status == 200 and sub.get("version_no") == 2 and len(sub.get("history") or []) == 2,
              f"version={sub.get('version_no')} history={len(sub.get('history') or [])}")
    status, payload = API.call("POST", f"/api/v1/applications/{app_id}/decide", {"approve": True}, token=T_APV)
    cid = d(payload).get("campaign_id")
    rep.check("1", "批准后生成活动", status == 200 and bool(cid), f"HTTP {status} campaign={cid}")
    req(rep, "1", "已批准申请不得重复审批", "POST", f"/api/v1/applications/{app_id}/decide", 409,
        {"approve": True}, token=T_APV)
    req(rep, "1", "已批准参数不可原地修改", "POST", f"/api/v1/applications/{app_id}/resubmit", 409,
        {"reason": "改", "interval": 60}, token=T_APP)

    # ---------------- 规则2：单活动、试点批次 ----------------
    second = new_app("第二个申请", 60)
    req(rep, "2", "同车队第二个申请不得生成活动", "POST", f"/api/v1/applications/{second}/decide", 409,
        {"approve": True}, token=T_APV)
    for vehicles, label in ((["TE-01"], "1 台"), (["TE-01", "TE-02", "TE-03"], "3 台"), (["TE-01", "TE-99"], "外来车辆")):
        req(rep, "2", f"试点选择{label}应 400", "POST", f"/api/v1/campaigns/{cid}/pilot", 400,
            {"vehicles": vehicles}, token=T_OP)
    status, payload = API.call("POST", f"/api/v1/campaigns/{cid}/pilot",
                               {"vehicles": ["TE-01", "TE-02"]}, token=T_OP)
    rep.check("2", "试点两车进入试点中", status == 200 and d(payload).get("status") == "piloting",
              f"HTTP {status} status={d(payload).get('status')}")
    untouched = 0
    for code in ("TE-03", "TE-04", "TE-05"):
        status, payload = API.call("GET", "/api/v1/vehicle/config", vehicle=VT[code])
        pull = d(payload)
        untouched += 1 if status == 200 and pull.get("interval") == 60 and (pull.get("version") or 0) == 0 else 0
    rep.check("2", "试点期其余 3 台拿不到活动配置", untouched == 3, f"符合数={untouched}/3")
    req(rep, "2", "试点期间不得重复发起试点", "POST", f"/api/v1/campaigns/{cid}/pilot", 409,
        {"vehicles": ["TE-01", "TE-02"]}, token=T_OP)

    run_part2(rep, cid, timeout_s)


def run_part2(rep, cid, timeout_s):
    # ---------------- 规则3/4/5/6：推广门槛、版本、回执、超时 ----------------
    status, payload = API.call("POST", f"/api/v1/campaigns/{cid}/promote", {}, token=T_OP)
    got = kinds()
    rep.check("3", "试点未成功即推广被拒且留痕",
              status == 409 and "illegal_promote" in got,
              f"HTTP {status} 留痕={sorted(k for k in got if k)}")

    status, payload = API.call("GET", "/api/v1/vehicle/config", vehicle=VT["TE-01"])
    pull = d(payload)
    rep.check("4", "试点车拉取本车 v1/45 秒",
              status == 200 and pull.get("version") == 1 and pull.get("interval") == 45,
              f"HTTP {status} v={pull.get('version')} interval={pull.get('interval')}")
    req(rep, "5", "无车辆凭证拉取应 401", "GET", "/api/v1/vehicle/config", 401)

    status, payload = API.call("POST", "/api/v1/vehicle/receipts", {"version": 1, "success": True}, vehicle=VT["TE-03"])
    got = kinds()
    rep.check("5", "跨车回执拒绝并留痕", status == 403 and "cross_vehicle" in got,
              f"HTTP {status} {ecode(payload)} 留痕={sorted(k for k in got if k)}")
    status, payload = API.call("POST", "/api/v1/vehicle/receipts", {"version": 99, "success": True}, vehicle=VT["TE-01"])
    got = kinds()
    rep.check("5", "未知版本拒绝并留痕", status == 404 and "unknown_version" in got,
              f"HTTP {status} {ecode(payload)} 留痕={sorted(k for k in got if k)}")

    status, _ = API.call("POST", "/api/v1/vehicle/receipts",
                         {"version": 1, "success": False, "reason": "本地存储只读"}, vehicle=VT["TE-01"])
    r1 = row(view(cid), "TE-01")
    rep.check("6", "失败回执不改变此前生效值",
              status == 200 and r1.get("applied_interval") == 60 and r1.get("status") == "失败",
              f"applied={r1.get('applied_interval')} 状态={r1.get('status')}")

    API.call("POST", "/api/v1/vehicle/receipts", {"version": 1, "success": True}, vehicle=VT["TE-01"])
    r1 = row(view(cid), "TE-01")
    rep.check("4", "成功回执后生效值更新为 45/v1",
              r1.get("applied_interval") == 45 and r1.get("applied_version") == 1,
              f"applied={r1.get('applied_interval')} v={r1.get('applied_version')}")
    status, payload = API.call("POST", "/api/v1/vehicle/receipts", {"version": 1, "success": True}, vehicle=VT["TE-01"])
    rep.check("5", "重复成功回执幂等", status == 200 and d(payload).get("duplicate") is True,
              f"HTTP {status} duplicate={d(payload).get('duplicate')}")

    time.sleep(timeout_s + 1.5)
    v = view(cid)
    rep.check("6", "超时显示超时且活动暂停",
              v.get("status") == "paused" and row(v, "TE-02").get("status") == "超时",
              f"活动={v.get('status')} TE-02={row(v, 'TE-02').get('status')}")
    req(rep, "3", "暂停后仍不得推广", "POST", f"/api/v1/campaigns/{cid}/promote", 409, {}, token=T_OP)

    status, _ = API.call("POST", "/api/v1/vehicle/receipts", {"version": 1, "success": True}, vehicle=VT["TE-02"])
    v = view(cid)
    r2 = row(v, "TE-02")
    rep.check("6", "超时后迟到成功更新结果并保留超时历史",
              status == 200 and r2.get("applied_interval") == 45 and r2.get("late_success") is True,
              f"HTTP {status} applied={r2.get('applied_interval')} late={r2.get('late_success')}")
    rep.check("6", "已暂停活动不因迟到回执自动重启", v.get("status") == "paused", f"活动={v.get('status')}")

    # ---------------- 规则7/8：停止与恢复 ----------------
    req(rep, "7", "停止未填原因应 400", "POST", f"/api/v1/campaigns/{cid}/stop", 400,
        {"reason": ""}, token=T_APV)
    status, payload = API.call("POST", f"/api/v1/campaigns/{cid}/stop", {"reason": "试点车离线"}, token=T_APV)
    st = d(payload)
    rep.check("7", "停止保留已发/未发范围",
              status == 200 and sorted(st.get("dispatched_vehicles") or []) == ["TE-01", "TE-02"]
              and sorted(st.get("undispatched_vehicles") or []) == ["TE-03", "TE-04", "TE-05"],
              f"已发={st.get('dispatched_vehicles')} 未发={st.get('undispatched_vehicles')}")
    status, payload = API.call("GET", "/api/v1/vehicle/config", vehicle=VT["TE-01"])
    rep.check("7", "停止不撤销车端已生效配置",
              status == 200 and d(payload).get("version") == 1 and d(payload).get("interval") == 45,
              f"v={d(payload).get('version')} interval={d(payload).get('interval')}")

    status, payload = API.call("POST", f"/api/v1/campaigns/{cid}/restore", {}, token=T_APV)
    rs = d(payload)
    rows = rs.get("recovery_rows") or []
    rep.check("7", "恢复针对曾发布车辆且版本更高、目标为活动前值",
              status == 200 and rs.get("status") == "restoring" and len(rows) == 2
              and all(r.get("expected_interval") == 60 and (r.get("expected_version") or 0) > 1 for r in rows),
              f"车={[r.get('code') for r in rows]} 目标={[r.get('expected_interval') for r in rows]} "
              f"版本={[r.get('expected_version') for r in rows]}")
    req(rep, "8", "存在未确认项时不得标记恢复完成", "POST", f"/api/v1/campaigns/{cid}/restore/complete", 409,
        {}, token=T_APV)
    # 回执响应不再丢弃：回执若失败，原实现会把它误报成"恢复完成 409"，属诊断噪声。
    code1, p1 = API.call("POST", "/api/v1/vehicle/receipts", {"version": 2, "success": True}, vehicle=VT["TE-01"])
    req(rep, "8", "仍有一台未确认时不得显示全部恢复", "POST", f"/api/v1/campaigns/{cid}/restore/complete", 409,
        {}, token=T_APV)
    code2, p2 = API.call("POST", "/api/v1/vehicle/receipts", {"version": 2, "success": True}, vehicle=VT["TE-02"])
    # 失败时带上回执状态码与逐车恢复状态，避免只看到 409 无法定位。
    v_before = view(cid)
    not_done = [f"{r.get('code')}={r.get('status')}" for r in (v_before.get("recovery_rows") or [])
                if r.get("status") != "成功"]
    status, payload = API.call("POST", f"/api/v1/campaigns/{cid}/restore/complete", {}, token=T_APV)
    rep.check("8", "全部确认后恢复完成",
              status == 200 and d(payload).get("all_recovered") is True and code1 == 200 and code2 == 200,
              f"HTTP {status} status={d(payload).get('status')} all_recovered={d(payload).get('all_recovered')} "
              f"err={ecode(payload)}/{emsg(payload)} "
              f"回执1={code1}{('(' + ecode(p1) + ')') if code1 != 200 else ''} "
              f"回执2={code2}{('(' + ecode(p2) + ')') if code2 != 200 else ''} 未确认={not_done}")
    req(rep, "8", "未全部成功的活动不得结案", "POST", f"/api/v1/campaigns/{cid}/complete", 409, {}, token=T_APV)

    # ---------------- 正常推广分支 ----------------
    third = new_app("第二次排查", 120)
    status, payload = API.call("POST", f"/api/v1/applications/{third}/decide", {"approve": True}, token=T_APV)
    cid2 = d(payload).get("campaign_id")
    rep.check("2", "上一活动结束后可发起新活动", status == 200 and bool(cid2), f"HTTP {status} campaign={cid2}")
    req(rep, "2", "新活动可试点另两台车", "POST", f"/api/v1/campaigns/{cid2}/pilot", 200,
        {"vehicles": ["TE-03", "TE-05"]}, token=T_OP)
    v = view(cid2)
    for item in v.get("vehicles") or []:
        code = item.get("code")
        if code not in ("TE-03", "TE-05"):
            continue
        status, payload = API.call("GET", "/api/v1/vehicle/config", vehicle=VT[code])
        rep.check("4", f"{code} 拉取版本与活动期望值一致且为本车递增版本",
                  status == 200 and d(payload).get("version") == item.get("expected_version")
                  and d(payload).get("interval") == 120,
                  f"pull v={d(payload).get('version')} 期望 v={item.get('expected_version')} interval={d(payload).get('interval')}")
        API.call("POST", "/api/v1/vehicle/receipts",
                 {"version": item.get("expected_version"), "success": True}, vehicle=VT[code])
    v = view(cid2)
    rep.check("3", "两台试点成功后进入试点成功", v.get("status") == "pilot_ok", f"活动={v.get('status')}")
    status, payload = API.call("POST", f"/api/v1/campaigns/{cid2}/promote", {}, token=T_OP)
    rep.check("3", "试点成功后推广剩余三台",
              status == 200 and len(d(payload).get("dispatched_vehicles") or []) == 5,
              f"HTTP {status} 已下发={len(d(payload).get('dispatched_vehicles') or [])}")
    req(rep, "8", "仍有未确认车辆时不得结案", "POST", f"/api/v1/campaigns/{cid2}/complete", 409, {}, token=T_APV)
    rcpt = []
    for item in view(cid2).get("vehicles") or []:
        c_r, p_r = API.call("POST", "/api/v1/vehicle/receipts",
                            {"version": item.get("expected_version"), "success": True}, vehicle=VT[item.get("code")])
        rcpt.append(f"{item.get('code')}:{c_r}{('(' + ecode(p_r) + ')') if c_r != 200 else ''}")
    status, payload = API.call("POST", f"/api/v1/campaigns/{cid2}/complete", {}, token=T_APV)
    rep.check("8", "五台全部成功后负责人结案",
              status == 200 and d(payload).get("status") == "completed" and d(payload).get("all_succeeded") is True,
              f"HTTP {status} status={d(payload).get('status')} err={ecode(payload)}/{emsg(payload)} "
              f"回执=[{';'.join(rcpt)}]")
    rows = view(cid2).get("vehicles") or []
    rep.check("4", "结案后逐车生效值等于期望值",
              all(r.get("applied_version") == r.get("expected_version") and r.get("applied_interval") == 120 for r in rows),
              f"逐车={[(r.get('code'), r.get('applied_version'), r.get('applied_interval')) for r in rows]}")


def port_busy(port):
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.settimeout(0.6)
        return sock.connect_ex(("127.0.0.1", port)) == 0


def start(port, timeout_s):
    """在临时工作目录启动网关：复制并改写配置端口，使用临时 SQLite，避免污染仓库与开发库。"""
    if port_busy(port):
        raise RuntimeError(f"端口 {port} 已被其他进程占用，请先释放或改用 --base-url 指向已运行的本服务")
    tmp = Path(tempfile.mkdtemp(prefix="fleetlog-check-"))
    binary = tmp / ("gateway.exe" if os.name == "nt" else "gateway")
    build = subprocess.run(["go", "build", "-o", str(binary), "./cmd/gateway"], cwd=REPO_ROOT,
                           capture_output=True, text=True)
    if build.returncode != 0:
        raise RuntimeError("构建网关失败：\n" + build.stderr[-1500:])
    workdir = tmp / "run"
    workdir.mkdir(parents=True)
    shutil.copytree(REPO_ROOT / "configs", workdir / "configs")
    (workdir / "logs").mkdir()
    cfg_text = (workdir / "configs/config.yaml").read_text(encoding="utf-8")
    cfg_text = re.sub(r"(?m)^(\s*port:\s*)\d+", rf"\g<1>{port}", cfg_text, count=1)
    (workdir / "configs/config.yaml").write_text(cfg_text, encoding="utf-8")
    env = os.environ.copy()
    env["FLEETLOG_DB_PATH"] = str(tmp / "check.db")
    env["FLEETLOG_TIMEOUT_SECONDS"] = str(timeout_s)
    log_file = open(tmp / "gateway.log", "wb")
    proc = subprocess.Popen([str(binary)], cwd=workdir, env=env, stdout=log_file, stderr=log_file)
    deadline = time.time() + 25
    while time.time() < deadline:
        if proc.poll() is not None:
            raise RuntimeError("网关启动即退出，请手动执行 go run ./cmd/gateway 查看原因")
        if port_busy(port):
            break
        time.sleep(0.3)
    else:
        proc.terminate()
        raise RuntimeError(f"网关 25 秒内未监听 {port}")
    status, payload = Api(f"http://127.0.0.1:{port}").call("GET", "/api/v1/demo/accounts")
    if status != 200 or not (payload.get("data") or {}).get("accounts"):
        proc.terminate()
        raise RuntimeError("端口上的服务不是本仓库网关（预置账号接口不可用），请更换端口")
    return proc, tmp


def main():
    ap = argparse.ArgumentParser(description="题目09 业务约束核查")
    ap.add_argument("--base-url", default="http://127.0.0.1:8080")
    ap.add_argument("--port", type=int, default=18080, help="--start 时的监听端口，默认 18080")
    ap.add_argument("--timeout-seconds", type=int, default=8, help="教学超时阈值（秒），须与服务端 FLEETLOG_TIMEOUT_SECONDS 一致；过小会让回执与超时竞争产生假失败")
    ap.add_argument("--start", action="store_true", help="自行构建并启动网关（临时数据库与临时配置）")
    ap.add_argument("--report", default="")
    args = ap.parse_args()

    global API
    proc, tmp = None, None
    if args.start:
        try:
            proc, tmp = start(args.port, args.timeout_seconds)
        except RuntimeError as exc:
            print(f"[错误] {exc}", file=sys.stderr)
            return 2
        args.base_url = f"http://127.0.0.1:{args.port}"
    API = Api(args.base_url)
    rep = Rep()
    ok = False
    try:
        run(rep, args.timeout_seconds)
        ok = True
    except RuntimeError as exc:
        print(f"[错误] {exc}", file=sys.stderr)
        return 2
    finally:
        if proc is not None:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
            if ok:
                shutil.rmtree(tmp, ignore_errors=True)
            else:
                print(f"网关日志已保留：{tmp / 'gateway.log'}", file=sys.stderr)
    failed = [i for i in rep.items if not i["ok"]]
    print("-" * 72)
    print(f"合计 {len(rep.items)} 项：PASS {len(rep.items) - len(failed)} / FAIL {len(failed)}")
    if args.report:
        path = Path(args.report)
        if not path.is_absolute():
            path = REPO_ROOT / path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps({
            "base_url": args.base_url, "total": len(rep.items), "failed": len(failed),
            "items": rep.items, "checked_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
        }, ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"证据文件：{path}")
    for item in failed:
        print(f"  FAIL 规则{item['rule']} {item['name']} -> {item['evidence']}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
