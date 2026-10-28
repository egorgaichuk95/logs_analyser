#!/usr/bin/env python3
"""
tools/synthgen/generate_incident.py

Генератор синтетического мультисервисного каскадного инцидента на 6 микросервисах.
Генерирует файлы в формате JSON-массива SchemaRawScript ([{ "timestamp": "...", "date": "...", "line": "{...}", "fields": {...} }]):
1) billing-db-proxy_part-0001.json (ROOT_CAUSE): pool exhausted / panic в pool_manager.go:84
2) crm-service_part-0001.json (PROPAGATION): context deadline exceeded к billing-db-proxy
3) chat-session_part-0001.json (PROPAGATION): false detected_level="error" на 204 и PEER_5XX к crm-service со связкой traceparent/request-id
4) auth-service_part-0001.json (SYMPTOM): context canceled
5) api-gateway_part-0001.json (SYMPTOM): 502 Bad Gateway с PII (email, remote_ip, jwt token)
6) chat-com_part-0001.json (NOISE + CLOCK_SKEW): фоновый шум оператора с clock skew +7200s (+2 часа)
"""

import json
import os
import sys
from datetime import datetime, timezone, timedelta


def make_record(dt, line_obj_or_str, fields):
    dt_utc = dt.replace(tzinfo=timezone.utc)
    ts_ns = str(int(dt_utc.timestamp() * 1_000_000_000))
    date_str = dt_utc.strftime("%Y-%m-%dT%H:%M:%S.000000Z")
    if isinstance(line_obj_or_str, dict):
        line_str = json.dumps(line_obj_or_str, separators=(",", ":"))
    else:
        line_str = str(line_obj_or_str)
    return {
        "timestamp": ts_ns,
        "date": date_str,
        "line": line_str,
        "fields": fields,
    }


def generate_cascade(out_dir: str):
    os.makedirs(out_dir, exist_ok=True)
    print(f"[*] Generating synthetic 6-service cascade in: {out_dir}")

    # Timeline bounds
    # Baseline: 08:00:00Z .. 08:19:59Z (1200 seconds)
    # Incident: 08:20:00Z .. 08:25:00Z (300 seconds)
    base_start = datetime(2026, 9, 30, 8, 0, 0, tzinfo=timezone.utc)
    inc_start = datetime(2026, 9, 30, 8, 20, 0, tzinfo=timezone.utc)

    # ------------------------------------------------------------------
    # 1. billing-db-proxy_part-0001.json (ROOT_CAUSE)
    # ------------------------------------------------------------------
    billing_records = []
    # Baseline: healthy info logs every 30s
    for sec in range(0, 1200, 30):
        cur_dt = base_start + timedelta(seconds=sec)
        dt_str = cur_dt.strftime("%Y-%m-%dT%H:%M:%S.000000Z")
        rec = make_record(
            cur_dt,
            {
                "time": dt_str,
                "level": "info",
                "source": {
                    "function": "pool.Status",
                    "file": "/app/internal/db/pool_manager.go",
                    "line": 42,
                },
                "msg": "HikariPool-1 - Connection pool healthy, active=3, idle=17, max=20",
                "trace_id": f"00000000000000000000000000{sec:06x}",
            },
            {
                "service_name": "billing-db-proxy",
                "pod": "billing-db-proxy-7f9c",
                "container": "billing-db-proxy",
                "deployment_environment": "production",
                "service_version": "v1.4.0",
                "detected_level": "info",
            },
        )
        billing_records.append(rec)

    # Incident: every 1s pool exhausted error (OWN_DEFECT) with source pool_manager.go:84
    for sec in range(0, 301):
        cur_dt = inc_start + timedelta(seconds=sec)
        dt_str = cur_dt.strftime("%Y-%m-%dT%H:%M:%S.000000Z")
        trace_id = f"a1b2c3d4e5f67890{sec:016x}"
        session_id = f"b8c4d9e0f1a234567890abcdef{sec:06x}"
        rec = make_record(
            cur_dt,
            {
                "time": dt_str,
                "level": "error",
                "source": {
                    "function": "pool.Acquire",
                    "file": "/app/internal/db/pool_manager.go",
                    "line": 84,
                },
                "msg": f"HikariPool-1 - Connection is not available, request timed out visit-session-id={session_id}",
                "error": "panic: runtime error: pool exhausted",
                "trace_id": trace_id,
                "visit-session-id": session_id,
            },
            {
                "service_name": "billing-db-proxy",
                "pod": "billing-db-proxy-7f9c",
                "container": "billing-db-proxy",
                "deployment_environment": "production",
                "service_version": "v1.4.0",
                "detected_level": "error",
            },
        )
        billing_records.append(rec)

    # ------------------------------------------------------------------
    # 2. crm-service_part-0001.json (PROPAGATION)
    # ------------------------------------------------------------------
    crm_records = []
    # Baseline: healthy info logs every 30s
    for sec in range(0, 1200, 30):
        cur_dt = base_start + timedelta(seconds=sec)
        dt_str = cur_dt.strftime("%Y-%m-%dT%H:%M:%S.000000Z")
        rec = make_record(
            cur_dt,
            {
                "time": dt_str,
                "level": "info",
                "source": {
                    "function": "crm.Query",
                    "file": "/app/crm_client.go",
                    "line": 30,
                },
                "msg": "upstream query completed in 4ms",
                "trace_id": f"00000000000000000000000000{sec:06x}",
            },
            {
                "service_name": "crm-service",
                "pod": "crm-service-59bc",
                "container": "crm-service",
                "deployment_environment": "production",
                "service_version": "v2.1.0",
                "detected_level": "info",
            },
        )
        crm_records.append(rec)

    # Incident: starting at 08:20:02Z (+2s lag from billing)
    for sec in range(2, 301):
        cur_dt = inc_start + timedelta(seconds=sec)
        dt_str = cur_dt.strftime("%Y-%m-%dT%H:%M:%S.000000Z")
        trace_id = f"a1b2c3d4e5f67890{sec:016x}"
        rec = make_record(
            cur_dt,
            {
                "time": dt_str,
                "level": "error",
                "source": {
                    "function": "crm.Query",
                    "file": "/app/crm_client.go",
                    "line": 52,
                },
                "msg": "upstream call failed",
                "error": 'Post "http://billing-db-proxy.chat.svc.cluster.local:5432/query": context deadline exceeded',
                "trace_id": trace_id,
            },
            {
                "service_name": "crm-service",
                "pod": "crm-service-59bc",
                "container": "crm-service",
                "deployment_environment": "production",
                "service_version": "v2.1.0",
                "detected_level": "error",
            },
        )
        crm_records.append(rec)

    # ------------------------------------------------------------------
    # 3. chat-session_part-0001.json (PROPAGATION)
    # ------------------------------------------------------------------
    session_records = []
    # Baseline and incident: 204 access logs with false detected_level="error" and error=""
    for sec in range(0, 1500, 15):
        cur_dt = base_start + timedelta(seconds=sec)
        dt_str = cur_dt.strftime("%Y-%m-%dT%H:%M:%S.000000Z")
        rec = make_record(
            cur_dt,
            {
                "time": dt_str,
                "id": f"d9f74895-ccc5-9ecb-9c38-{sec:012x}",
                "remote_ip": "5.191.119.115",
                "host": "chat-com.pin-up.talkwus.com",
                "method": "OPTIONS",
                "uri": "/api/v1/licenses/com/auth/",
                "status": 204,
                "error": "",
                "latency": 9658,
            },
            {
                "service_name": "chat-session",
                "pod": "chat-session-84f9",
                "container": "chat-session",
                "deployment_environment": "production",
                "service_version": "v1.10.2",
                "detected_level": "error",
            },
        )
        session_records.append(rec)

    # Incident: starting at 08:20:04Z (+4s lag) interceptor log with PEER_5XX and X-Request-Id <-> Traceparent
    for sec in range(4, 301):
        cur_dt = inc_start + timedelta(seconds=sec)
        dt_str = cur_dt.strftime("%Y-%m-%dT%H:%M:%S.000000Z")
        trace_id = f"a1b2c3d4e5f67890{sec:016x}"
        req_id = f"d9f74895-ccc5-9ecb-9c38-{sec:012x}"
        traceparent = f"00-{trace_id}-0123456789abcdef-01"
        rec = make_record(
            cur_dt,
            {
                "time": dt_str,
                "level": "error",
                "msg": f"upstream call failed status 502: X-Request-Id: [{req_id}] Traceparent: [{traceparent}]",
                "error": "status 502 Bad Gateway from http://crm-service.chat.svc.cluster.local:8080",
                "peer_service": "crm-service",
                "request_id": req_id,
                "trace_id": trace_id,
                "headers": {
                    "X-Request-Id": [req_id],
                    "Traceparent": [traceparent],
                },
            },
            {
                "service_name": "chat-session",
                "pod": "chat-session-84f9",
                "container": "chat-session",
                "deployment_environment": "production",
                "service_version": "v1.10.2",
                "detected_level": "error",
            },
        )
        session_records.append(rec)

    # ------------------------------------------------------------------
    # 4. auth-service_part-0001.json (SYMPTOM)
    # ------------------------------------------------------------------
    auth_records = []
    # Baseline: healthy info logs every 30s
    for sec in range(0, 1200, 30):
        cur_dt = base_start + timedelta(seconds=sec)
        dt_str = cur_dt.strftime("%Y-%m-%dT%H:%M:%S.000000Z")
        rec = make_record(
            cur_dt,
            {
                "time": dt_str,
                "level": "info",
                "source": {
                    "function": "auth.Authenticate",
                    "file": "/app/auth.go",
                    "line": 35,
                },
                "msg": "session token verified successfully",
                "trace_id": f"00000000000000000000000000{sec:06x}",
            },
            {
                "service_name": "auth-service",
                "pod": "auth-service-5c6d",
                "container": "auth-service",
                "deployment_environment": "production",
                "service_version": "v1.8.0",
                "detected_level": "info",
            },
        )
        auth_records.append(rec)

    # Incident: starting at 08:20:05Z (+5s lag) writes context canceled
    for sec in range(5, 301):
        cur_dt = inc_start + timedelta(seconds=sec)
        dt_str = cur_dt.strftime("%Y-%m-%dT%H:%M:%S.000000Z")
        trace_id = f"a1b2c3d4e5f67890{sec:016x}"
        req_id = f"d9f74895-ccc5-9ecb-9c38-{sec:012x}"
        rec = make_record(
            cur_dt,
            {
                "time": dt_str,
                "level": "error",
                "source": {
                    "function": "auth.Authenticate",
                    "file": "/app/auth.go",
                    "line": 75,
                },
                "msg": "request processing aborted: context canceled",
                "error": "context canceled",
                "trace_id": trace_id,
                "request_id": req_id,
            },
            {
                "service_name": "auth-service",
                "pod": "auth-service-5c6d",
                "container": "auth-service",
                "deployment_environment": "production",
                "service_version": "v1.8.0",
                "detected_level": "error",
            },
        )
        auth_records.append(rec)

    # ------------------------------------------------------------------
    # 5. api-gateway_part-0001.json (SYMPTOM)
    # ------------------------------------------------------------------
    gateway_records = []
    jwt_token = "Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature1234567890"
    # Baseline: 200 OK access logs every 15s
    for sec in range(0, 1200, 15):
        cur_dt = base_start + timedelta(seconds=sec)
        dt_str = cur_dt.strftime("%Y-%m-%dT%H:%M:%S.000000Z")
        rec = make_record(
            cur_dt,
            {
                "time": dt_str,
                "id": f"d9f74895-ccc5-9ecb-9c38-{sec:012x}",
                "remote_ip": "5.191.119.115",
                "method": "POST",
                "uri": "/api/v1/chat/messages",
                "status": 200,
                "error": "",
                "latency": 15000000,
                "email": "test.user@example.com",
                "token": jwt_token,
            },
            {
                "service_name": "api-gateway",
                "pod": "api-gateway-3a1b",
                "container": "api-gateway",
                "deployment_environment": "production",
                "service_version": "v3.0.1",
                "detected_level": "info",
            },
        )
        gateway_records.append(rec)

    # Incident: starting at 08:20:05Z (+5s lag) 502 access logs with PII
    for sec in range(5, 301):
        cur_dt = inc_start + timedelta(seconds=sec)
        dt_str = cur_dt.strftime("%Y-%m-%dT%H:%M:%S.000000Z")
        req_id = f"d9f74895-ccc5-9ecb-9c38-{sec:012x}"
        rec = make_record(
            cur_dt,
            {
                "time": dt_str,
                "id": req_id,
                "remote_ip": "5.191.119.115",
                "method": "POST",
                "uri": "/api/v1/chat/messages",
                "status": 502,
                "error": "status 502 Bad Gateway: upstream http://chat-session.chat.svc.cluster.local:8080 timed out",
                "latency": 5000000000,
                "email": "test.user@example.com",
                "token": jwt_token,
            },
            {
                "service_name": "api-gateway",
                "pod": "api-gateway-3a1b",
                "container": "api-gateway",
                "deployment_environment": "production",
                "service_version": "v3.0.1",
                "detected_level": "error",
            },
        )
        gateway_records.append(rec)

    # ------------------------------------------------------------------
    # 6. chat-com_part-0001.json (NOISE + CLOCK_SKEW)
    # ------------------------------------------------------------------
    chatcom_records = []
    # Stable 30 events per minute (every 2 seconds) for the whole 25 minutes
    # Clock skew +7200s (+2 hours) inside line vs raw ts
    for sec in range(0, 1500, 2):
        cur_dt = base_start + timedelta(seconds=sec)
        app_dt = cur_dt + timedelta(seconds=7200)
        app_time_str = app_dt.strftime("%y%m%d %H:%M:%S +0000")
        rec = make_record(
            cur_dt,
            {
                "msg": "Operator (operator_id=55754) was not found in cache",
                "time": app_time_str,
                "level": "ERROR",
                "line": "cache:98",
                "type": "app",
            },
            {
                "service_name": "chat-com",
                "pod": "chat-com-64dbb8949-4dmwd",
                "container": "chat-com",
                "deployment_environment": "production",
                "service_version": "v2.9.13-chat",
                "detected_level": "error",
            },
        )
        chatcom_records.append(rec)

    # Write all 6 files
    files_map = {
        "billing-db-proxy_part-0001.json": billing_records,
        "crm-service_part-0001.json": crm_records,
        "chat-session_part-0001.json": session_records,
        "auth-service_part-0001.json": auth_records,
        "api-gateway_part-0001.json": gateway_records,
        "chat-com_part-0001.json": chatcom_records,
    }

    total_records = 0
    for filename, records in files_map.items():
        file_path = os.path.join(out_dir, filename)
        with open(file_path, "w", encoding="utf-8") as f:
            json.dump(records, f, indent=2)
        size_bytes = os.path.getsize(file_path)
        total_records += len(records)
        print(f"  [+] Wrote {filename}: {len(records)} events, {size_bytes} bytes")

    print(f"[OK] Generated {len(files_map)} files with {total_records} total events in {out_dir}\n")


def main():
    repo_root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    default_out = os.path.join(repo_root, "import", "synth_cascade")
    out_dir = sys.argv[1] if len(sys.argv) > 1 else default_out
    generate_cascade(out_dir)


if __name__ == "__main__":
    main()
