#!/usr/bin/env python3
"""
tools/synthgen/e2e_verify.py

Сквозной E2E-тест всей системы RCA (ClickHouse, ingest-service, rca-engine,
evidence-service, tilt-collector, gateway-api):
1. Ожидание готовности gateway-api (GET /api/v1/health и GET /api/v1/tilt/targets).
2. Создание кейса POST /api/v1/cases ("Synthetic 6-Service Cascade").
3. Запуск инжеста 6 сгенерированных файлов POST /api/v1/cases/{id}/ingest-local и ожидание COMPLETED.
4. Запуск анализа POST /api/v1/cases/{id}/analyze.
5. Проверка:
   - Гипотеза №1: role == "ROOT_CAUSE", service == "billing-db-proxy", source_place содержит "pool_manager.go:84";
   - crm-service и chat-session имеют role == "PROPAGATION";
   - В chat-com лог с оператором имеет role == "NOISE";
   - В clock_skew_findings присутствует chat-com со сдвигом около 7200000 мс;
   - evidence_items не пуст (E-01...).
6. Скачивание GET /api/v1/cases/{id}/reports/{run_id}/gemini и проверка PII / маскирования и E-01.
7. Очистка сырых логов POST /api/v1/storage/purge-raw и проверка raw_bytes == 0 при доступности отчёта и анализа.
8. Печать "ALL E2E CHECKS PASSED!".
"""

import json
import os
import sys
import time
import urllib.error
import urllib.request

BASE_URL = os.environ.get("GATEWAY_API_URL", "http://127.0.0.1:8080").rstrip("/")


def request_json(method: str, path: str, data: dict = None, timeout: float = 30.0):
    url = f"{BASE_URL}{path}"
    req_body = None
    headers = {"Accept": "application/json"}
    if data is not None:
        req_body = json.dumps(data).encode("utf-8")
        headers["Content-Type"] = "application/json"

    req = urllib.request.Request(url, data=req_body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8")
            content_type = resp.headers.get("Content-Type", "")
            if "application/json" in content_type:
                return resp.status, json.loads(raw)
            return resp.status, raw
    except urllib.error.HTTPError as err:
        err_body = err.read().decode("utf-8")
        try:
            parsed = json.loads(err_body)
            return err.code, parsed
        except Exception:
            return err.code, err_body


def request_raw(method: str, path: str, timeout: float = 30.0):
    url = f"{BASE_URL}{path}"
    req = urllib.request.Request(url, headers={"Accept": "*/*"}, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8")
            return resp.status, raw
    except urllib.error.HTTPError as err:
        return err.code, err.read().decode("utf-8")


def wait_for_gateway(max_wait_seconds: int = 120):
    print(f"[*] Waiting for Gateway API at {BASE_URL} to become healthy...")
    start_time = time.time()
    last_err = None

    while time.time() - start_time < max_wait_seconds:
        try:
            status, body = request_json("GET", "/api/v1/health", timeout=5.0)
            if status == 200 and isinstance(body, dict) and body.get("status") == "ok":
                print("  [+] GET /api/v1/health -> 200 OK")
                break
        except Exception as e:
            last_err = e
        time.sleep(2)
    else:
        print(f"[FAIL] Gateway API did not become healthy within {max_wait_seconds}s: {last_err}")
        sys.exit(1)

    print("[*] Checking Tilt targets discovery (tilt-collector integration)...")
    status, targets = request_json("GET", "/api/v1/tilt/targets", timeout=10.0)
    if status != 200:
        print(f"[FAIL] GET /api/v1/tilt/targets returned status {status}: {targets}")
        sys.exit(1)
    print(f"  [+] GET /api/v1/tilt/targets -> 200 OK (found {len(targets)} targets)")


def run_e2e_flow():
    wait_for_gateway()

    # Step 1: Create Case
    print("\n[*] Step 1: Creating synthetic incident case...")
    status, case_meta = request_json(
        "POST",
        "/api/v1/cases",
        {
            "title": "Synthetic 6-Service Cascade",
            "description": "E2E verification case for multi-service cascade, clock skew, noise and PII redaction",
        },
    )
    if status != 201 or not isinstance(case_meta, dict) or "id" not in case_meta:
        print(f"[FAIL] Failed to create case (status {status}): {case_meta}")
        sys.exit(1)

    case_id = case_meta["id"]
    print(f"  [+] Case created successfully: ID = {case_id}")

    # Step 2: Start ingest-local
    print("\n[*] Step 2: Ingesting 6 synthetic cascade files...")
    import_paths = [
        "/import/synth_cascade/billing-db-proxy_part-0001.json",
        "/import/synth_cascade/crm-service_part-0001.json",
        "/import/synth_cascade/chat-session_part-0001.json",
        "/import/synth_cascade/auth-service_part-0001.json",
        "/import/synth_cascade/api-gateway_part-0001.json",
        "/import/synth_cascade/chat-com_part-0001.json",
    ]
    status, ingest_resp = request_json(
        "POST",
        f"/api/v1/cases/{case_id}/ingest-local",
        {"file_paths": import_paths},
    )
    if status not in (200, 202) or not isinstance(ingest_resp, dict) or "job_id" not in ingest_resp:
        print(f"[FAIL] Failed to start ingest-local (status {status}): {ingest_resp}")
        sys.exit(1)

    job_id = ingest_resp["job_id"]
    print(f"  [+] Ingest job started: job_id = {job_id}")

    # Wait for job completion
    ingest_timeout = 90
    ingest_start = time.time()
    final_ingest_status = None
    while time.time() - ingest_start < ingest_timeout:
        status, status_data = request_json("GET", f"/api/v1/jobs/{job_id}", timeout=10.0)
        if status == 200 and isinstance(status_data, dict):
            job_state = status_data.get("status")
            if job_state == "COMPLETED":
                final_ingest_status = status_data
                break
            if job_state == "FAILED":
                print(f"[FAIL] Ingest job failed: {status_data.get('error')}")
                sys.exit(1)
        time.sleep(1)
    else:
        print(f"[FAIL] Ingest job timed out after {ingest_timeout}s")
        sys.exit(1)

    rows_ingested = final_ingest_status.get("rows_ingested", 0)
    files_done = final_ingest_status.get("files_done", 0)
    print(f"  [+] Ingest completed: {files_done} files, {rows_ingested} rows ingested")

    # Step 3: Trigger RCA Analysis
    print("\n[*] Step 3: Triggering RCA analysis POST /api/v1/cases/{id}/analyze...")
    analyze_payload = {
        "incident_start": "2026-09-30T08:20:00Z",
        "incident_end": "2026-09-30T08:25:00Z",
        "baseline_start": "2026-09-30T08:00:00Z",
        "baseline_end": "2026-09-30T08:20:00Z",
        "use_historical_baseline": False,
    }
    status, analysis = request_json("POST", f"/api/v1/cases/{case_id}/analyze", analyze_payload, timeout=45.0)
    if status != 200 or not isinstance(analysis, dict):
        print(f"[FAIL] POST /api/v1/cases/{case_id}/analyze returned {status}: {analysis}")
        sys.exit(1)

    run_id = analysis.get("run_id")
    hypotheses = analysis.get("hypotheses", [])
    clock_skew_findings = analysis.get("clock_skew_findings", [])
    evidence_items = analysis.get("evidence_items", [])

    print(f"  [+] Analysis succeeded: Run ID = {run_id}")
    print(f"  [+] Hypotheses count: {len(hypotheses)}")
    print(f"  [+] Clock skew findings count: {len(clock_skew_findings)}")
    print(f"  [+] Evidence items count: {len(evidence_items)}")

    # Step 4: Validate RCA Hypotheses and Roles
    print("\n[*] Step 4: Validating RCA hypotheses and roles...")
    if not hypotheses:
        print("[FAIL] Hypotheses list is empty!")
        sys.exit(1)

    # 4.1. Check Hypothesis #1 (rank == 1)
    h1 = hypotheses[0]
    print(f"  -> Hypothesis #1: rank={h1.get('rank')}, role={h1.get('role')}, service={h1.get('service')}, source_place={h1.get('source_place')}")
    if h1.get("role") != "ROOT_CAUSE":
        print(f"[FAIL] Expected Hypothesis #1 to have role == 'ROOT_CAUSE', got {h1.get('role')!r}")
        sys.exit(1)
    if h1.get("service") != "billing-db-proxy":
        print(f"[FAIL] Expected Hypothesis #1 service == 'billing-db-proxy', got {h1.get('service')!r}")
        sys.exit(1)
    if "pool_manager.go:84" not in h1.get("source_place", ""):
        print(f"[FAIL] Expected source_place to contain 'pool_manager.go:84', got {h1.get('source_place')!r}")
        sys.exit(1)
    print("  [OK] Hypothesis #1 accurately points to billing-db-proxy and pool_manager.go:84 as ROOT_CAUSE.")

    # 4.2. Check crm-service and chat-session roles (PROPAGATION)
    roles_by_service = {h.get("service"): h.get("role") for h in hypotheses}
    crm_role = roles_by_service.get("crm-service")
    chat_session_role = roles_by_service.get("chat-session")
    print(f"  -> crm-service role: {crm_role}")
    print(f"  -> chat-session role: {chat_session_role}")

    if crm_role != "PROPAGATION":
        print(f"[FAIL] Expected crm-service role == 'PROPAGATION', got {crm_role!r}")
        sys.exit(1)
    if chat_session_role != "PROPAGATION":
        print(f"[FAIL] Expected chat-session role == 'PROPAGATION', got {chat_session_role!r}")
        sys.exit(1)
    print("  [OK] crm-service and chat-session correctly classified as PROPAGATION.")

    # 4.3. Check chat-com operator log role (NOISE)
    operator_hyp = None
    for h in hypotheses:
        if h.get("service") == "chat-com" or "Operator" in h.get("template_text", ""):
            operator_hyp = h
            break
    if not operator_hyp:
        print("[FAIL] chat-com operator hypothesis not found!")
        sys.exit(1)

    print(f"  -> chat-com operator hypothesis role: {operator_hyp.get('role')}")
    if operator_hyp.get("role") != "NOISE":
        print(f"[FAIL] Expected chat-com operator log role == 'NOISE', got {operator_hyp.get('role')!r}")
        sys.exit(1)
    print("  [OK] chat-com constant operator log correctly classified as NOISE.")

    # 4.4. Check clock_skew_findings
    chatcom_skew = None
    for cs in clock_skew_findings:
        if cs.get("service") == "chat-com":
            chatcom_skew = cs
            break
    if not chatcom_skew:
        print(f"[FAIL] chat-com not found in clock_skew_findings: {clock_skew_findings}")
        sys.exit(1)

    skew_ms = chatcom_skew.get("skew_ms", 0)
    print(f"  -> chat-com detected clock skew: {skew_ms} ms")
    if abs(skew_ms - 7200000) > 5000:
        print(f"[FAIL] Expected chat-com clock skew ~7200000 ms (+2h), got {skew_ms} ms")
        sys.exit(1)
    print("  [OK] chat-com clock skew accurately detected around 7,200,000 ms.")

    # 4.5. Check evidence_items
    if not evidence_items:
        print("[FAIL] evidence_items list in analysis is empty!")
        sys.exit(1)
    first_ev_id = evidence_items[0].get("id", "")
    print(f"  -> First evidence item ID: {first_ev_id}")
    if not first_ev_id.startswith("E-"):
        print(f"[FAIL] Expected evidence item ID to start with 'E-', got {first_ev_id!r}")
        sys.exit(1)
    print("  [OK] evidence_items populated with traceable IDs (E-01...).")

    # Step 5: Check Gemini Prompt Bundle for Redaction and Evidence IDs
    print("\n[*] Step 5: Validating Gemini report and PII redaction...")
    status, gemini_text = request_raw("GET", f"/api/v1/cases/{case_id}/reports/{run_id}/gemini")
    if status != 200 or not gemini_text:
        print(f"[FAIL] GET /api/v1/cases/{case_id}/reports/{run_id}/gemini returned {status}")
        sys.exit(1)

    # Check that secrets & PII are absent
    forbidden_tokens = [
        "test.user@example.com",
        "5.191.119.115",
        "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9",
    ]
    for tok in forbidden_tokens:
        if tok in gemini_text:
            print(f"[FAIL] PII leak in Gemini bundle: found forbidden string {tok!r}")
            sys.exit(1)
    print("  [OK] PII sanitized: email, public IP, and JWT token are completely absent from Gemini bundle.")

    # Check that evidence citation and root cause service are present
    if "[E-01]" not in gemini_text:
        print("[FAIL] Expected citation '[E-01]' in Gemini bundle, not found")
        sys.exit(1)
    if "billing-db-proxy" not in gemini_text:
        print("[FAIL] Expected service 'billing-db-proxy' in Gemini bundle, not found")
        sys.exit(1)
    print("  [OK] Gemini bundle contains '[E-01]' evidence citation and 'billing-db-proxy' root cause.")

    # Step 6: Test 1-Click Raw Purge
    print("\n[*] Step 6: Testing 1-click raw logs purge POST /api/v1/storage/purge-raw...")
    status, purge_resp = request_json("POST", "/api/v1/storage/purge-raw")
    if status != 200:
        print(f"[FAIL] POST /api/v1/storage/purge-raw returned status {status}: {purge_resp}")
        sys.exit(1)
    print(f"  [+] Purge completed: {purge_resp}")

    status, storage_status = request_json("GET", "/api/v1/storage/status")
    if status != 200 or not isinstance(storage_status, dict):
        print(f"[FAIL] GET /api/v1/storage/status returned {status}: {storage_status}")
        sys.exit(1)

    raw_bytes = storage_status.get("raw_bytes", -1)
    print(f"  -> Storage status after purge: raw_bytes = {raw_bytes}, total_bytes = {storage_status.get('total_bytes')}")
    if raw_bytes != 0:
        print(f"[FAIL] Expected raw_bytes == 0 after purge, got {raw_bytes}")
        sys.exit(1)
    print("  [OK] raw_bytes successfully dropped to 0.")

    # Verify that analysis and html report are still 200 OK after purge
    status, get_analysis = request_json("GET", f"/api/v1/cases/{case_id}/analysis")
    if status != 200 or not isinstance(get_analysis, dict):
        print(f"[FAIL] GET /api/v1/cases/{case_id}/analysis returned {status} after purge")
        sys.exit(1)
    print("  [OK] GET /api/v1/cases/{id}/analysis remains 200 OK after raw purge.")

    status, html_report = request_raw("GET", f"/api/v1/cases/{case_id}/reports/{run_id}/html")
    if status != 200 or not html_report:
        print(f"[FAIL] GET /api/v1/cases/{case_id}/reports/{run_id}/html returned {status} after purge")
        sys.exit(1)
    print("  [OK] GET /api/v1/cases/{id}/reports/{run_id}/html remains 200 OK after raw purge.")

    # Step 7: Done
    print("\n==========================================")
    print("ALL E2E CHECKS PASSED!")
    print("==========================================")


def main():
    try:
        run_e2e_flow()
    except Exception as e:
        print(f"\n[FAIL] E2E verification aborted with exception: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)


if __name__ == "__main__":
    main()
