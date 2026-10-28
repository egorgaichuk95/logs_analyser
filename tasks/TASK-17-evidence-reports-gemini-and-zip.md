# TASK-17: Маскировщик PII, отчёты `report.md/html`, пакет `gemini_bundle.md`, `evidence.zip` и HTTP API `:8083` (`./services/evidence-service`)

- **Статус:** `TODO`
- **Зависимости:** `TASK-16`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`, `./services/evidence-service/internal/`
  - Создавать/редактировать:
    - `./services/evidence-service/internal/reporter/pii.go`
    - `./services/evidence-service/internal/reporter/builder.go`
    - `./services/evidence-service/internal/reporter/builder_test.go`
    - `./services/evidence-service/internal/server/http.go`
    - `./services/evidence-service/cmd/evidence/main.go`
    - `./services/evidence-service/Dockerfile`
  - **НЕ трогать:** другие микросервисы

---

## 1. Цель задачи

Завершить микросервис `evidence-service` (`:8083`):
1. Реализовать строгий маскировщик PII (`RedactPII`), который заменяет email-адреса, имена пользователей (`display_name`), публичные IP и остаточные токены на детерминированные метки `<EMAIL_xxxx>`, `<USER_xxxx>`, `<CLIENT_IP_xxxx>`.
2. Сгенерировать в `/data/reports/<case_id>/<run_id>/`:
   - `report.md` и `report.html` (со встроенными `timeline.svg` и `topology.svg`);
   - `gemini_bundle.md` (готовый системный промпт + граф + таблица гипотез + доказательства `E-01..E-NN`, полностью очищенные через `RedactPII`, размером $\le 320\text{ КБ}$);
   - `evidence.jsonl` и архив `evidence_<case_id>.zip` (содержащий все файлы отчёта + `manifest.json` с контрольными суммами `SHA-256`).
3. Поднять HTTP API `:8083` (`POST /internal/evidence/build`).

---

## 2. Точные сигнатуры и шаблоны (Blueprint)

### 2.1. `./services/evidence-service/internal/reporter/pii.go`
```go
package reporter

import (
	"fmt"
	"hash/fnv"
	"net"
	"regexp"
)

var (
	emailRe       = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	displayNameRe = regexp.MustCompile(`(?i)(display_name)(%22%3A%22|":\s*"|=)([^"&%\s]+)`)
	ipv4Re        = regexp.MustCompile(`\b(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})\b`)
	jwtFallbackRe = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
)

// RedactPII маскирует email, display_name, внешние IP (сохраняя приватные 10.x, 172.16-31.x, 192.168.x, 127.x) и JWT
func RedactPII(text string) string
```

### 2.2. `./services/evidence-service/internal/reporter/builder.go`
```go
package reporter

import (
	"github.com/local/logs-analyzer/pkg/contracts"
)

// BuildAllArtifacts создаёт в reportsDir/<case_id>/<run_id>/ файлы:
// timeline.svg, topology.svg, evidence.jsonl, report.md, report.html, gemini_bundle.md, evidence_<case_id>.zip
func BuildAllArtifacts(reportsDir string, req contracts.BuildEvidenceRequest, items []contracts.EvidenceItem) (contracts.BuildEvidenceResponse, error)
```

**Обязательный системный промпт в начале `gemini_bundle.md`:**
```markdown
# SYSTEM PROMPT FOR GOOGLE GEMINI (RCA VERIFICATION)

Ты — Principal SRE и эксперт по распределённым микросервисным системам.
Ниже представлен автоматический отчёт поиска первопричины инцидента (RCA) и пакет проверяемых артефактов (`E-01` .. `E-NN`), собранный по логам нескольких сервисов. Все секреты и PII замаскированы детерминированными токенами.

Твоя задача:
1. Проверить ранжированные гипотезы (`ROOT_CAUSE`, `PROPAGATION`, `SYMPTOM`, `NOISE`) и оценить, верно ли отделена первопричина от каскадных следствий (обрати особое внимание на направление ошибок: `CANCELED` (`context canceled`) означает отмену сверху вызывающим сервисом, а `OWN_DEFECT` / `RESOURCE` / `DEADLINE` указывают на корень проблемы).
2. Указать точное место поломки: **Сервис → Pod → Файл и строка кода (`source_place`) → HTTP-ручка**.
3. В каждом своём выводе обязательно ссылаться на конкретные идентификаторы доказательств в квадратных скобках, например `[E-01]`, `[E-02]`.
```

### 2.3. `./services/evidence-service/internal/reporter/builder_test.go`
Проверить:
1. `RedactPII` скрывает `ak.odilov@talkwus.com` (`<EMAIL_...>`), `%22display_name%22%3A%22Rahil+Nabiyev%22` (`<USER_...>`) и внешний IP `5.191.119.115` (`<CLIENT_IP_...>`), но **оставляет без изменений** внутренний IP узла k8s `10.128.26.1` и DNS `chat-com.chat.svc.cluster.local`.
2. `BuildAllArtifacts` создаёт на диске все 7 файлов (`timeline.svg`, `topology.svg`, `evidence.jsonl`, `report.md`, `report.html`, `gemini_bundle.md`, `evidence_<case_id>.zip`), и внутри `.zip` присутствует валидный `manifest.json`.

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/evidence-service golang:1.23-alpine go test -v -cover ./...
docker compose build evidence-service
py ./verify_task.py TASK-17
```

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-28
- **Созданные/изменённые файлы:**
  - `services/evidence-service/internal/reporter/pii.go` — детерминированный маскировщик PII (`RedactPII`) для email, display_name, публичных IP, JWT токенов с сохранением приватных подсетей (10.x, 172.16-31.x, 192.168.x, 127.x) и DNS k8s;
  - `services/evidence-service/internal/reporter/builder.go` — генератор 7 артефактов (`timeline.svg`, `topology.svg`, `evidence.jsonl`, `report.md`, `report.html`, `gemini_bundle.md`, `evidence_<case_id>.zip` с `manifest.json`);
  - `services/evidence-service/internal/reporter/builder_test.go` — исчерпывающие тесты TDD: маскирование PII, генерация 7 файлов, валидация manifest.json и sha256 внутри zip, системный промпт Gemini и жесткое ограничение размера bundle <= 320 КБ;
  - `services/evidence-service/internal/server/http.go` — HTTP сервер (`GET /health`, `POST /internal/evidence/build`) с интеграцией ClickHouse fallback через `collector.CollectEvidence`;
  - `services/evidence-service/internal/server/http_test.go` — тесты HTTP API: healthcheck, сборка доказательств, обработка некорректных запросов и fallback;
  - `services/evidence-service/cmd/evidence/main.go` — точка входа микросервиса с graceful shutdown;
  - `services/evidence-service/Dockerfile` — многоэтапная сборка Docker-образа `evidence-service`.
- **Результаты проверок и тестов:**
  - `go test -v -cover ./internal/...`:
    * `github.com/local/logs-analyzer/services/evidence-service/internal/collector`: `coverage: 87.3% of statements` (PASS)
    * `github.com/local/logs-analyzer/services/evidence-service/internal/reporter`: `coverage: 89.5% of statements` (PASS)
    * `github.com/local/logs-analyzer/services/evidence-service/internal/server`: `coverage: 93.1% of statements` (PASS)
    * `github.com/local/logs-analyzer/services/evidence-service/internal/svgchart`: `coverage: 92.7% of statements` (PASS)
  - `docker compose build evidence-service`: успешно собран образ `rca-project-evidence-service:latest`.
  - `py ./verify_task.py TASK-17`: `[VERIFY PASSED] TASK-17 passed all automated checks!`.
- **Заметки по реализации:**
  - Системный промпт для Google Gemini встроен посимвольно в соответствии со спецификацией Blueprint.
  - Лимит размера `gemini_bundle.md` (<= 320 КБ) соблюдается благодаря динамическому контролю объема при включении доказательств.
  - Контрольные суммы SHA-256 вычисляются для всех 6 отчётных файлов и упаковываются в `manifest.json` внутри архива `evidence_<case_id>.zip`.
  - Заглушки, `TODO`, `FIXME` и нереализованный код отсутствуют.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - Все контракты (`BuildEvidenceRequest`, `BuildEvidenceResponse`, `EvidenceItem`, HTTP API `POST /internal/evidence/build`, `GET /health`) посимвольно проверены на соответствие `./pkg/contracts/` и Blueprint задачи.
  - Посимвольно сверен системный промпт Google Gemini в `builder.go` и `gemini_bundle.md`.
  - Проверено детерминированное маскирование PII (`RedactPII`) в `pii.go`: email (`<EMAIL_...>`), display_name (`<USER_...>`), публичные IP (`<CLIENT_IP_...>`), JWT (`<TOKEN_...>`) с сохранением подсетей k8s `10.x`, `172.16-31.x`, `192.168.x`, `127.x` и DNS `*.svc.cluster.local`.
  - Проверена генерация всех 7 артефактов (`timeline.svg`, `topology.svg`, `evidence.jsonl`, `report.md`, `report.html`, `gemini_bundle.md`, `evidence_<case_id>.zip`) и `manifest.json` с SHA-256 контрольными суммами внутри zip-архива.
  - Проверено ограничение размера `gemini_bundle.md` $\le 320\text{ КБ}$.
  - Лично выполнен автоматический верификатор `py ./verify_task.py TASK-17` — все проверки успешно пройдены (`[VERIFY PASSED]`), Docker-образ `evidence-service` успешно собран.
  - Покрытие тестами:
    * `services/evidence-service/internal/server`: `93.1% of statements` (порог: 75.0%)
    * `services/evidence-service/internal/svgchart`: `92.7% of statements` (порог: 75.0%)
    * `services/evidence-service/internal/reporter`: `89.5% of statements` (порог: 75.0%)
    * `services/evidence-service/internal/collector`: `87.3% of statements` (порог: 75.0%)
- **Найденные замечания (если есть):** Замечаний нет. Реализация выполнена в строгом соответствии с контрактами, TDD-методологией, требованиями безопасности и ресурсными ограничениями.
