#!/usr/bin/env python3
"""
Автоматический верификатор задач (Automated Quality & TDD Gate) для Воркера и Ревьюера.
Запуск: py ./verify_task.py TASK-XX (TASK-01 .. TASK-23)
Проверяет:
1. Наличие всех файлов из раздела Scope задачи на диске и их непустоту.
2. Полное отсутствие заглушек (TODO, FIXME, panic("not implemented")) и os.ReadFile/io.ReadAll в потоковом чтении.
3. Запуск тестов с кэшем Docker-томов (rca_go_cache, rca_go_build, rca_npm_cache) и проверку покрытия (coverage >= 75-80%).
4. Сборку Docker-образа (docker compose build) для финальных задач микросервисов (TASK-09, TASK-15, TASK-17, TASK-19, TASK-20, TASK-22).
"""

import os
import re
import subprocess
import sys

TASK_SPECS = {
    "TASK-01": {
        "files": [
            "go.work",
            "pkg/contracts/go.mod",
            "services/ingest-service/go.mod",
            "services/rca-engine/go.mod",
            "services/evidence-service/go.mod",
            "services/gateway-api/go.mod",
            "services/tilt-collector/go.mod",
            "deploy/clickhouse/low_memory.xml",
            "deploy/clickhouse/low_memory_profile.xml",
            "deploy/clickhouse/init_schema.sql",
        ],
        "go_dir": None,
        "pkg_rel": None,
        "min_cov": 0.0,
        "docker_service": None,
    },
    "TASK-02": {
        "files": [
            "docker-compose.yml",
            "config/service_profiles.yaml",
        ],
        "go_dir": None,
        "pkg_rel": None,
        "min_cov": 0.0,
        "docker_service": None,
        "custom_cmd": ["docker", "compose", "config", "-q"],
    },
    "TASK-03": {
        "files": [
            "pkg/contracts/events.go",
            "pkg/contracts/events_test.go",
        ],
        "go_dir": "pkg/contracts",
        "pkg_rel": "./...",
        "min_cov": 80.0,
        "docker_service": None,
    },
    "TASK-04": {
        "files": [
            "pkg/contracts/rca.go",
            "pkg/contracts/api.go",
            "pkg/contracts/rca_test.go",
        ],
        "go_dir": "pkg/contracts",
        "pkg_rel": "./...",
        "min_cov": 80.0,
        "docker_service": None,
    },
    "TASK-05": {
        "files": [
            "services/ingest-service/internal/reader/stream_reader.go",
            "services/ingest-service/internal/reader/stream_reader_test.go",
        ],
        "go_dir": "services/ingest-service",
        "pkg_rel": "./internal/reader/...",
        "min_cov": 80.0,
        "docker_service": None,
        "forbid_readall": True,
    },
    "TASK-06": {
        "files": [
            "services/ingest-service/internal/normalizer/normalizer.go",
            "services/ingest-service/internal/normalizer/normalizer_test.go",
        ],
        "go_dir": "services/ingest-service",
        "pkg_rel": "./internal/normalizer/...",
        "min_cov": 80.0,
        "docker_service": None,
    },
    "TASK-07": {
        "files": [
            "services/ingest-service/internal/classifier/redactor.go",
            "services/ingest-service/internal/classifier/classifier.go",
            "services/ingest-service/internal/classifier/classifier_test.go",
        ],
        "go_dir": "services/ingest-service",
        "pkg_rel": "./internal/classifier/...",
        "min_cov": 80.0,
        "docker_service": None,
    },
    "TASK-08": {
        "files": [
            "services/ingest-service/internal/drain/drain.go",
            "services/ingest-service/internal/drain/drain_test.go",
        ],
        "go_dir": "services/ingest-service",
        "pkg_rel": "./internal/drain/...",
        "min_cov": 80.0,
        "docker_service": None,
    },
    "TASK-09": {
        "files": [
            "services/ingest-service/internal/chwriter/writer.go",
            "services/ingest-service/internal/server/http.go",
            "services/ingest-service/internal/server/http_test.go",
            "services/ingest-service/cmd/ingest/main.go",
            "services/ingest-service/Dockerfile",
        ],
        "go_dir": "services/ingest-service",
        "pkg_rel": "./internal/...",
        "min_cov": 75.0,
        "docker_service": "ingest-service",
    },
    "TASK-10": {
        "files": [
            "services/gateway-api/internal/metadb/store.go",
            "services/gateway-api/internal/storage/manager.go",
            "services/gateway-api/internal/storage/manager_test.go",
        ],
        "go_dir": "services/gateway-api",
        "pkg_rel": "./internal/...",
        "min_cov": 80.0,
        "docker_service": None,
    },
    "TASK-11": {
        "files": [
            "services/rca-engine/internal/idgraph/graph.go",
            "services/rca-engine/internal/idgraph/graph_test.go",
        ],
        "go_dir": "services/rca-engine",
        "pkg_rel": "./internal/idgraph/...",
        "min_cov": 80.0,
        "docker_service": None,
    },
    "TASK-12": {
        "files": [
            "services/rca-engine/internal/topology/topology.go",
            "services/rca-engine/internal/topology/topology_test.go",
        ],
        "go_dir": "services/rca-engine",
        "pkg_rel": "./internal/topology/...",
        "min_cov": 80.0,
        "docker_service": None,
    },
    "TASK-13": {
        "files": [
            "services/rca-engine/internal/timeseries/anomaly.go",
            "services/rca-engine/internal/timeseries/anomaly_test.go",
        ],
        "go_dir": "services/rca-engine",
        "pkg_rel": "./internal/timeseries/...",
        "min_cov": 80.0,
        "docker_service": None,
    },
    "TASK-14": {
        "files": [
            "services/rca-engine/internal/timeseries/leadlag.go",
            "services/rca-engine/internal/timeseries/leadlag_test.go",
        ],
        "go_dir": "services/rca-engine",
        "pkg_rel": "./internal/timeseries/...",
        "min_cov": 80.0,
        "docker_service": None,
    },
    "TASK-15": {
        "files": [
            "services/rca-engine/internal/ranker/ranker.go",
            "services/rca-engine/internal/ranker/ranker_test.go",
            "services/rca-engine/internal/server/http.go",
            "services/rca-engine/cmd/rca/main.go",
            "services/rca-engine/Dockerfile",
        ],
        "go_dir": "services/rca-engine",
        "pkg_rel": "./internal/...",
        "min_cov": 75.0,
        "docker_service": "rca-engine",
    },
    "TASK-16": {
        "files": [
            "services/evidence-service/internal/collector/collector.go",
            "services/evidence-service/internal/svgchart/charts.go",
            "services/evidence-service/internal/svgchart/charts_test.go",
        ],
        "go_dir": "services/evidence-service",
        "pkg_rel": "./internal/...",
        "min_cov": 75.0,
        "docker_service": None,
    },
    "TASK-17": {
        "files": [
            "services/evidence-service/internal/reporter/pii.go",
            "services/evidence-service/internal/reporter/builder.go",
            "services/evidence-service/internal/reporter/builder_test.go",
            "services/evidence-service/internal/server/http.go",
            "services/evidence-service/cmd/evidence/main.go",
            "services/evidence-service/Dockerfile",
        ],
        "go_dir": "services/evidence-service",
        "pkg_rel": "./internal/...",
        "min_cov": 75.0,
        "docker_service": "evidence-service",
    },
    "TASK-18": {
        "files": [
            "services/tilt-collector/internal/discovery/discovery.go",
            "services/tilt-collector/internal/discovery/discovery_test.go",
        ],
        "go_dir": "services/tilt-collector",
        "pkg_rel": "./internal/discovery/...",
        "min_cov": 80.0,
        "docker_service": None,
    },
    "TASK-19": {
        "files": [
            "services/tilt-collector/internal/collector/manager.go",
            "services/tilt-collector/internal/server/http.go",
            "services/tilt-collector/internal/server/http_test.go",
            "services/tilt-collector/cmd/tiltcollector/main.go",
            "services/tilt-collector/Dockerfile",
        ],
        "go_dir": "services/tilt-collector",
        "pkg_rel": "./internal/...",
        "min_cov": 75.0,
        "docker_service": "tilt-collector",
    },
    "TASK-20": {
        "files": [
            "services/gateway-api/internal/explorer/explorer.go",
            "services/gateway-api/internal/server/http.go",
            "services/gateway-api/internal/server/http_test.go",
            "services/gateway-api/cmd/gateway/main.go",
            "services/gateway-api/Dockerfile",
        ],
        "go_dir": "services/gateway-api",
        "pkg_rel": "./internal/...",
        "min_cov": 75.0,
        "docker_service": "gateway-api",
    },
    "TASK-21": {
        "files": [
            "services/frontend-ui/package.json",
            "services/frontend-ui/tsconfig.json",
            "services/frontend-ui/vite.config.ts",
            "services/frontend-ui/index.html",
            "services/frontend-ui/src/api/client.ts",
            "services/frontend-ui/src/components/StorageHeader.tsx",
            "services/frontend-ui/src/components/CaseSidebar.tsx",
            "services/frontend-ui/src/styles.css",
        ],
        "go_dir": None,
        "pkg_rel": None,
        "min_cov": 0.0,
        "docker_service": None,
        "frontend_tsc": True,
    },
    "TASK-22": {
        "files": [
            "services/frontend-ui/src/components/RcaDashboard.tsx",
            "services/frontend-ui/src/components/EvidenceAndGeminiPanel.tsx",
            "services/frontend-ui/src/App.tsx",
            "services/frontend-ui/src/main.tsx",
            "services/frontend-ui/nginx.conf",
            "services/frontend-ui/Dockerfile",
        ],
        "go_dir": None,
        "pkg_rel": None,
        "min_cov": 0.0,
        "docker_service": "frontend-ui",
        "frontend_build": True,
    },
    "TASK-23": {
        "files": [
            "tools/synthgen/generate_incident.py",
            "tools/synthgen/e2e_verify.py",
        ],
        "go_dir": None,
        "pkg_rel": None,
        "min_cov": 0.0,
        "docker_service": None,
    },
    "TASK-24": {
        "files": [
            "services/frontend-ui/src/types/navigation.ts",
            "services/frontend-ui/src/components/TopNavHeader.tsx",
            "services/frontend-ui/src/components/IngestAndTiltPage.tsx",
            "services/frontend-ui/src/styles.css",
        ],
        "go_dir": None,
        "pkg_rel": None,
        "min_cov": 0.0,
        "docker_service": None,
        "frontend_tsc": True,
    },
    "TASK-25": {
        "files": [
            "services/frontend-ui/src/components/HypothesesPage.tsx",
            "services/frontend-ui/src/components/TopologyAndTimelinePage.tsx",
            "services/frontend-ui/src/styles.css",
        ],
        "go_dir": None,
        "pkg_rel": None,
        "min_cov": 0.0,
        "docker_service": None,
        "frontend_tsc": True,
    },
    "TASK-26": {
        "files": [
            "services/frontend-ui/src/components/ChainAndContextPage.tsx",
            "services/frontend-ui/src/components/EvidenceAndGeminiPanel.tsx",
            "services/frontend-ui/src/App.tsx",
            "services/frontend-ui/src/styles.css",
        ],
        "go_dir": None,
        "pkg_rel": None,
        "min_cov": 0.0,
        "docker_service": "frontend-ui",
        "frontend_build": True,
    },
    "TASK-27": {
        "files": [
            "services/frontend-ui/src/utils/humanRcaTranslator.ts",
            "services/frontend-ui/src/components/SimpleRcaOverviewPage.tsx",
            "services/frontend-ui/src/styles.css",
        ],
        "go_dir": None,
        "pkg_rel": None,
        "min_cov": 0.0,
        "docker_service": None,
        "frontend_tsc": True,
    },
    "TASK-28": {
        "files": [
            "services/frontend-ui/src/components/ProblemLogsAndCorrelationPanel.tsx",
            "services/frontend-ui/src/components/SimpleRcaOverviewPage.tsx",
            "services/frontend-ui/src/components/TopNavHeader.tsx",
            "services/frontend-ui/src/types/navigation.ts",
            "services/frontend-ui/src/App.tsx",
            "services/frontend-ui/src/styles.css",
        ],
        "go_dir": None,
        "pkg_rel": None,
        "min_cov": 0.0,
        "docker_service": "frontend-ui",
        "frontend_build": True,
    },
}

FORBIDDEN_PATTERNS = [
    (re.compile(r"//\s*TODO\b", re.IGNORECASE), "Found forbidden '// TODO' placeholder"),
    (re.compile(r"//\s*FIXME\b", re.IGNORECASE), "Found forbidden '// FIXME' placeholder"),
    (re.compile(r"//\s*implement\s+later", re.IGNORECASE), "Found forbidden '// implement later' placeholder"),
    (re.compile(r'panic\s*\(\s*"not implemented"', re.IGNORECASE), "Found forbidden panic(\"not implemented\")"),
]


def fail(msg: str) -> None:
    print(f"\n[VERIFY FAILED] {msg}")
    sys.exit(1)


def main() -> None:
    if len(sys.argv) < 2 or sys.argv[1].upper() not in TASK_SPECS:
        print("Usage: py ./verify_task.py TASK-XX (e.g. TASK-01 .. TASK-23)")
        sys.exit(2)

    task_id = sys.argv[1].upper()
    spec = TASK_SPECS[task_id]
    cwd = os.getcwd()
    print(f"=== Running Automated Verification Gate for {task_id} ===")

    for rel_path in spec["files"]:
        full_path = os.path.join(cwd, rel_path)
        if not os.path.isfile(full_path):
            fail(f"Missing required file in Scope: {rel_path}")
        size = os.path.getsize(full_path)
        if size == 0:
            fail(f"Required file is empty (0 bytes): {rel_path}")
        print(f"  [OK] File exists: {rel_path} ({size} bytes)")

        if rel_path.endswith((".go", ".ts", ".tsx", ".py")):
            with open(full_path, "r", encoding="utf-8") as f:
                content = f.read()
            for pat, reason in FORBIDDEN_PATTERNS:
                if pat.search(content):
                    fail(f"{reason} in {rel_path}")
            if spec.get("forbid_readall") and not rel_path.endswith("_test.go"):
                if "os.ReadFile(" in content or "io.ReadAll(" in content:
                    fail(f"Forbidden os.ReadFile / io.ReadAll in streaming reader {rel_path}")

    if "custom_cmd" in spec:
        cmd = spec["custom_cmd"]
        print(f"  [RUN] {' '.join(cmd)}")
        res = subprocess.run(cmd, capture_output=True, text=True)
        if res.returncode != 0:
            fail(f"Command failed: {' '.join(cmd)}\n{res.stderr}")
        print("  [OK] Custom verification command passed.")

    if spec["go_dir"]:
        go_cmd = [
            "docker", "run", "--rm",
            "-v", f"{cwd}:/workspace",
            "-v", "rca_go_cache:/go/pkg/mod",
            "-v", "rca_go_build:/root/.cache/go-build",
            "-w", f"/workspace/{spec['go_dir']}",
            "golang:1.23-alpine",
            "go", "test", "-v", "-cover", spec["pkg_rel"],
        ]
        print(f"  [RUN] {' '.join(go_cmd)}")
        res = subprocess.run(go_cmd, capture_output=True, text=True)
        print(res.stdout)
        if res.returncode != 0:
            print(res.stderr)
            fail(f"Go tests failed for {task_id}")

        cov_matches = re.findall(r"coverage:\s+([\d.]+)%\s+of\s+statements", res.stdout)
        if not cov_matches:
            fail(f"No coverage percentage reported by 'go test -cover' for {task_id}")
        max_cov = max(float(c) for c in cov_matches)
        print(f"  [COVERAGE] Detected max package coverage: {max_cov:.1f}% (min required: {spec['min_cov']:.1f}%)")
        if max_cov < spec["min_cov"]:
            fail(f"Test coverage {max_cov:.1f}% is below minimum threshold {spec['min_cov']:.1f}% for {task_id}")

    if spec.get("frontend_tsc") or spec.get("frontend_build"):
        fe_dir = os.path.join(cwd, "services", "frontend-ui")
        npm_script = "npm install && npx tsc --noEmit" if spec.get("frontend_tsc") else "npm install && npm run build"
        fe_cmd = [
            "docker", "run", "--rm",
            "-v", f"{fe_dir}:/app",
            "-v", "rca_npm_cache:/root/.npm",
            "-w", "/app",
            "node:22-alpine",
            "sh", "-c", npm_script,
        ]
        print(f"  [RUN] {' '.join(fe_cmd)}")
        res = subprocess.run(fe_cmd, capture_output=True, text=True)
        print(res.stdout)
        if res.returncode != 0:
            print(res.stderr)
            fail(f"Frontend verification failed for {task_id}")

    if spec["docker_service"]:
        build_cmd = ["docker", "compose", "build", spec["docker_service"]]
        print(f"  [SMOKE BUILD] {' '.join(build_cmd)}")
        res = subprocess.run(build_cmd, capture_output=True, text=True)
        if res.returncode != 0:
            print(res.stderr)
            fail(f"Docker image build failed for {spec['docker_service']}")
        print(f"  [OK] Docker image built successfully for {spec['docker_service']}")

    print(f"\n[VERIFY PASSED] {task_id} passed all automated checks!")


if __name__ == "__main__":
    main()
