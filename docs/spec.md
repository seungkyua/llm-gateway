# spec.md — llm-gateway

> 본 spec.md는 `brief.md`(=`prd.md`+`sdd.md` 병합본)에 이미 확정된 스택/아키텍처/
> API/데이터 모델/보안 설계를 그대로 옮긴 것입니다. Planner는 이를 재설계하지
> 않았습니다. 이 문서의 부가가치는 "마일스톤 분해"와 "실행 가능한 인수 기준"입니다.

## 목표(Goal)

vLLM 추론 서버 앞단에 배치되어 API Key 기반 인증, 요청/토큰 사용량 제어(Rate
Limit·Quota), 다중 vLLM 백엔드 로드밸런싱(+헬스체크), 성능 관측(TTFT/TPOT/ITL)을
담당하는 Go 기반 단일 프로세스 게이트웨이 `llm-gateway`를 만든다. 클라이언트는
기존 OpenAI SDK의 `base_url`만 바꿔 그대로 사용할 수 있어야 하며, 운영자는
관리자 REST API와 `config.yaml`로 Key/한도/백엔드/로드밸런싱 전략을 관리한다.
현재 범위는 단일 인스턴스·파일 기반 상태 저장을 전제로 하되, 향후 Redis/RDB로
스토리지만 교체할 수 있도록 `storage.Store` 인터페이스로 경계를 미리 그어둔다.

## 스택(Stack)

브리프(§4, §7.5)에 이미 확정된 대로 그대로 채택한다.

| 영역 | 선택 | 근거(브리프에서 그대로) |
|---|---|---|
| 언어/런타임 | Go (안정 최신 버전; 이 spec에서는 1.22+ 가정 — Assumptions 참고) | 고성능 프록시/동시성 처리에 적합 |
| 웹 프레임워크 | Gin (`github.com/gin-gonic/gin`, MIT) | 라우팅/미들웨어 구성이 간결 |
| 설정 파일 | YAML (`gopkg.in/yaml.v3`, MIT/Apache-2.0) — `config.yaml` | 사람이 직접 편집하는 설정에 적합 |
| 상태 파일 | JSON — `data/keys.json`, `data/usage.json` | 기계가 쓰고 읽기 쉽고 Redis/RDB 매핑이 쉬움 |
| 스토리지 추상화 | `internal/storage`의 `Store` 인터페이스 + `FileStore` 구현체 | 향후 구현체 교체만으로 Redis/RDB 전환 |
| 로깅 | 구조화 JSON(기본) / 텍스트(옵션), stdout(기본)/파일(옵션) | 관측성 도구 연동 + 로컬 가독성 |
| 빌드 | Makefile (`make build OS=... ARCH=...`), darwin/linux × amd64/arm64 | 단일 바이너리 배포 |
| 배포 | Dockerfile, multi-stage, buildx로 `linux/amd64`+`linux/arm64` | 컨테이너 배포 |
| 해시 | 표준 라이브러리 `crypto/sha256`, `crypto/subtle`(상수시간 비교) | API Key 해시 저장 + 타이밍 공격 방지 |

## 아키텍처(Architecture)

브리프 sdd.md §2, §5 그대로.

```
Clients                    llm-gateway (단일 Go 프로세스)                 vLLM 백엔드(N대)
─────────                  ──────────────────────────────                ─────────────
OpenAI SDK  ──/v1/*────▶  [Auth MW] → [RateLimiter] → [TokenLimiter]
                                                            │
                                                            ▼
                                                     [LoadBalancer] ◀── [HealthChecker](주기적 ping)
                                                            │
                                                            ▼
                                                        [Proxy] ───────────────▶ vLLM #1..#N
                                                            │
                                                            ▼
                                                       [Metrics/Logger] → stdout/파일 JSON 로그

운영자 ──/admin/*───▶  [Admin API] ──▶ storage.Store ──▶ FileStore (keys.json/usage.json)
                        (관리자 토큰)
```

- 단일 프로세스·단일 인스턴스 전제(파일 기반 상태). 다중 인스턴스 확장은 Out of scope.
- 백엔드가 1대뿐이면 LoadBalancer는 항상 그 1대만 반환한다(별도 분기 불필요).
- `internal/`은 계층이 아니라 도메인(auth, ratelimit, tokenlimit, loadbalancer,
  healthcheck, proxy, tokencount, metrics, storage, admin, apierror, config)
  단위로 분리한다.

### 폴더 구조 (브리프 sdd.md §5 그대로)

```
llm-gateway/
├── cmd/llm-gateway/main.go
├── internal/
│   ├── config/
│   ├── admin/
│   ├── auth/
│   ├── ratelimit/
│   ├── tokenlimit/
│   ├── loadbalancer/
│   ├── healthcheck/
│   ├── proxy/
│   ├── tokencount/
│   ├── metrics/
│   ├── storage/
│   └── apierror/
├── config.yaml.example
├── data/                 (.gitignore, 0600)
├── logs/                 (.gitignore)
├── Makefile
├── Dockerfile
├── go.mod / go.sum
└── README.md
```

### 상태 머신 (브리프 sdd.md §6 그대로)

**요청 처리 파이프라인**: Authenticating → (Unauthorized | RateChecking) →
(RateLimited | TokenChecking) → (TokenLimited | Routing) → (NoBackend | Proxying)
→ (Streaming|Completed) → (PartialError|Completed) → Logging → 종료.

**백엔드 헬스**: Unknown → (Healthy | Unhealthy), Healthy → Unhealthy(연속 실패
N회, 기본 1회), Unhealthy → Healthy(성공 1회). `Unknown`은 라우팅 후보에서
제외(콜드스타트 보호).

### 인메모리 vs 파일 상태 (브리프 sdd.md §6.3 그대로)

| 상태 | 저장 위치 |
|---|---|
| RPS/RPM/TPM 카운터 | 인메모리(mutex/atomic) |
| 백엔드 헬스 상태 | 인메모리 |
| Key 메타데이터/해시 | 파일(keys.json) + 인메모리 캐시 |
| 기간별 누적 토큰 쿼타 | 인메모리 캐시 + 파일(usage.json) write-behind(기본 5초 주기 flush + graceful shutdown 시 flush) |

## 공개 인터페이스(Public interface)

브리프 sdd.md §4 그대로. 모든 응답 `Content-Type: application/json`(스트리밍
제외). 모든 에러는 OpenAI 호환 포맷: `{"error": {"message": "...", "type": "...", "code": "..."}}`.

### 관리자 API (`Authorization: Bearer <admin_token>` 필수)

- `POST /admin/keys` — Key 발급.
  - Request: `{"name": string(필수,1-100자), "rpm"?: int, "tpm"?: int, "max_tokens_per_request"?: int, "quota_period"?: "daily"|"monthly", "quota_tokens"?: int64}`
  - Response 201: `{"key_id","key","name","created_at","limits":{...}}` — 평문 `key`는 **이 응답에서만** 노출.
  - 에러: 401 `invalid_admin_token`, 400 `invalid_request_error`(name 누락 등).
- `GET /admin/keys` — 발급된 Key 메타데이터 목록(key_hash/평문 제외).
- `DELETE /admin/keys/{key_id}` — soft delete(`revoked_at` 설정, 레코드 보존).
  - Response 200: `{"key_id","revoked_at"}`. 에러: 404 `key_not_found`.

### 프록시 API (`Authorization: Bearer <api_key>` 필수)

- `POST /v1/chat/completions`, `POST /v1/completions` — vLLM OpenAI 호환
  스키마를 그대로 프록시(필드 가공 없음). `stream=true`면
  `Content-Type: text/event-stream`으로 SSE 청크를 그대로 중계.
  에러: 401/429/400/503(아래 에러 표 참고).

### Ops 엔드포인트 (인증 불필요)

- `GET /healthz` — 프로세스 liveness. Response 200 `{"status":"ok"}`.
- `GET /readyz` — HEALTHY 백엔드 ≥ 1개 여부. Response 200
  `{"status":"ready","healthy_backends":N,"total_backends":M}`, 0개면 503.

### 에러 응답 매트릭스 (브리프 sdd.md §8.1 그대로)

| 상황 | HTTP | type | code |
|---|---|---|---|
| API Key 누락/무효/폐기됨 | 401 | authentication_error | invalid_api_key |
| RPS/RPM/TPM 초과 | 429 | rate_limit_error | rate_limit_exceeded (+`Retry-After` 헤더) |
| 요청당 `max_tokens` 상한 초과 | 400 | invalid_request_error | max_tokens_exceeded |
| 기간별 누적 토큰 쿼타 소진 | 429 | rate_limit_error | quota_exceeded (+`Retry-After` 헤더) |
| 가용 백엔드 없음 | 503 | api_error | no_available_backend |
| 관리자 토큰 누락/불일치 | 401 | authentication_error | invalid_admin_token |
| 존재하지 않는 key_id 폐기 시도 | 404 | invalid_request_error | key_not_found |
| vLLM 비정상 응답(HTML/빈 응답 등) | 502 | api_error | bad_gateway |
| Admin Key 발급 요청 body 오류(name 누락 등) | 400 | invalid_request_error | invalid_request_error |

### CLI / 설정 진입점

- `--config <path>` 플래그(기본값 `./config.yaml`).
- `LLM_GATEWAY_CONFIG` 환경변수.
- 우선순위: CLI 플래그 > 환경변수 > 기본값.
- `config.yaml` 내 `${VAR}` 미해결 시, 그 값이 필수 필드(`admin.token`)면 기동 거부 + stderr 원인 출력 + non-zero exit.

## 데이터 모델(Data model)

브리프 sdd.md §3 그대로.

### `internal/storage.Store` 인터페이스

```go
type Store interface {
    CreateKey(ctx context.Context, rec KeyRecord) error
    GetKeyByHash(ctx context.Context, keyHash string) (KeyRecord, bool, error)
    RevokeKey(ctx context.Context, keyID string) error
    ListKeys(ctx context.Context) ([]KeyRecord, error)

    GetUsage(ctx context.Context, keyID string, period string) (UsageRecord, error)
    IncrementUsage(ctx context.Context, keyID string, period string, tokens int64) error
    ResetUsage(ctx context.Context, keyID string, period string) error
}
```

`FileStore`가 최초 구현체. `internal/admin`, `internal/auth`, `internal/tokenlimit`은
이 인터페이스에만 의존하고 `FileStore` 구체 타입을 직접 참조하지 않는다.

### `KeyRecord` (`data/keys.json`, 배열, 파일 권한 0600)

| 필드 | 타입 | 비고 |
|---|---|---|
| key_id | string | PK, `key_xxxxxxxx` 형태 |
| key_hash | string | SHA-256 hex, 평문 미저장 |
| name | string | 필수 |
| created_at | RFC3339 string | |
| revoked_at | RFC3339 string \| null | null=활성 |
| limits.rpm / limits.tpm | int | ≥0 |
| limits.max_tokens_per_request | int | ≥0 |
| limits.quota_period | "daily"\|"monthly" | |
| limits.quota_tokens | int64 | ≥0 |

### `UsageRecord` (`data/usage.json`, `key_id → period → UsageRecord`, 파일 권한 0600)

| 필드 | 타입 |
|---|---|
| key_id | string |
| period | string (`2026-09` 월간 / `2026-09-08` 일간) |
| tokens_used | int64 |
| updated_at | RFC3339 string |

- 파일이 없으면 최초 기동 시 빈 구조(`[]`/`{}`)로 자동 생성한다. **부모
  디렉터리(`data/`)가 존재하지 않는 경우도 반드시 함께 생성해야 한다**
  (예: `os.MkdirAll(filepath.Dir(path), 0o700)`를 파일 생성 전에 호출).
  `.gitignore`가 `data/`를 제외하므로 신규 clone 직후에는 `data/` 디렉터리
  자체가 존재하지 않는 것이 정상 상태이며, 이 상태에서 README의 "빌드/실행"
  절차만 그대로 따라 최초 기동했을 때 아무 에러 없이 성공해야 한다(사용자가
  수동으로 `mkdir data`를 먼저 실행하도록 요구해서는 안 됨).
- 같은 이유로 `logging.output: file`로 로그 파일 경로를 지정한 경우에도,
  로그 파일의 부모 디렉터리(`logs/`)가 없으면 로거 초기화 시 자동 생성한다.
- RPS/RPM/TPM 카운터는 이 파일에 넣지 않는다(인메모리 전용).

## 마일스톤(Milestones)

각 마일스톤은 한 번의 Generator 호출(TDD Red-Green-Refactor)로 구현 가능한
크기로 쪼갰다. 괄호 안은 테스트 단위로 더 쪼갤 수 있는 케이스 힌트.

1. **프로젝트 스캐폴딩 + 설정 로더(`internal/config`)**
   - `go.mod` 초기화, 위 폴더 구조 생성.
   - `config.yaml` 파싱(YAML), `${VAR}` 환경변수 치환, 스키마 검증.
   - `--config` 플래그 / `LLM_GATEWAY_CONFIG` 우선순위 처리.
   - 케이스: 정상 로드 / 필수 필드(`admin.token`) 미해결 시 기동 거부 /
     잘못된 YAML 파싱 오류 시 기동 거부(둘 다 stderr 메시지 + non-zero exit) /
     플래그가 환경변수보다 우선함.

2. **스토리지 계층(`internal/storage`)**
   - `Store` 인터페이스, `KeyRecord`/`UsageRecord` 타입 정의.
   - `FileStore`: `keys.json`/`usage.json` 읽기/쓰기, 파일 없으면 빈 구조 자동 생성, 0600 권한.
     **파일의 부모 디렉터리가 존재하지 않아도 `NewFileStore`가 이를
     자동 생성(`MkdirAll`)한 뒤 파일을 만들어야 한다** — 반드시 부모
     디렉터리가 아직 없는 경로(예: `t.TempDir()` 하위의 존재하지 않는
     서브디렉터리, `filepath.Join(t.TempDir(), "data", "keys.json")`처럼
     한 단계 더 들어간 미생성 경로)를 넘겨 `NewFileStore`가 성공하는지
     검증하는 테스트를 최소 1개 포함한다(테스트가 항상 이미 존재하는
     디렉터리만 부모로 사용하면 이 버그를 잡지 못하므로 주의).
   - 케이스: `CreateKey`+`GetKeyByHash`(존재/미존재) / `RevokeKey`(soft
     delete, `revoked_at` 설정) / `ListKeys` / `GetUsage`(없으면 0) /
     `IncrementUsage`(누적) / `ResetUsage` / **부모 디렉터리 미존재 상태에서
     `NewFileStore` 호출 시 디렉터리+파일이 함께 생성되고 에러 없이 성공**.

3. **에러 헬퍼 + 관리자 API(`internal/apierror`, `internal/admin`)**
   - OpenAI 호환 에러 JSON 생성 헬퍼(`apierror`).
   - 관리자 토큰 인증 미들웨어(상수시간 비교, `crypto/subtle`).
   - `POST /admin/keys`(발급, SHA-256 해시 후 저장, 평문 1회 노출),
     `GET /admin/keys`(해시/평문 제외 목록), `DELETE /admin/keys/{key_id}`(soft delete).
   - 케이스: 정상 발급 / 관리자 토큰 누락·불일치(401) / name 누락(400) /
     존재하지 않는 key_id 폐기(404) / 목록 응답에 key_hash·평문 미포함 확인.

4. **클라이언트 인증 미들웨어(`internal/auth`)**
   - `Authorization: Bearer <api_key>` 파싱 → SHA-256 해시 → `Store.GetKeyByHash` 조회.
   - 케이스: 유효 key 통과(다음 핸들러로 KeyRecord 전달) / 헤더 누락(401) /
     알 수 없는 key(401) / 폐기된 key(`revoked_at`≠null, 401), 모두
     `invalid_api_key`.

5. **Rate Limiter(`internal/ratelimit`)**
   - Key별 RPS/RPM/TPM 인메모리 제한(슬라이딩 윈도우 또는 토큰버킷 — Assumptions 참고).
   - 케이스: 한도 이내 통과 / RPM 초과 시 429+`Retry-After` / TPM 초과 시
     429+`Retry-After` / 한도 0 = 즉시 차단 / 서로 다른 key는 독립적으로 카운트.

6. **Token Limiter(`internal/tokenlimit`)**
   - 요청당 `max_tokens` 상한 검사(요청 body의 `max_tokens` > limits.max_tokens_per_request면 400).
   - 기간별 누적 쿼타 검사(`Store.GetUsage`로 현재 사용량 확인, 초과 시 429).
   - 응답 후 `Store.IncrementUsage` 호출(write-behind: 인메모리 캐시 갱신 즉시 + 파일 flush는 주기적/graceful shutdown).
   - 케이스: 상한 이내 통과 / max_tokens 상한 초과(400) / 쿼타 소진(429) /
     쿼타 여유 있음(통과 후 사용량 증가 확인) / daily vs monthly period 키 계산.

7. **Load Balancer + Health Checker(`internal/loadbalancer`, `internal/healthcheck`)**
   - `Strategy` 인터페이스, `RoundRobinHealthcheck`, `LeastRequests` 구현.
   - Health Checker: 주기적(기본 5초) 능동 헬스체크, Unknown/Healthy/Unhealthy 상태 전이.
   - 케이스: 백엔드 1개면 항상 그것만 선택 / 백엔드 N개 라운드로빈 순환 /
     Unhealthy 백엔드 제외 / 모든 백엔드 Unhealthy면 `Pick()`이 "가용 백엔드 없음" 반환 /
     `LeastRequests`가 진행 중 요청 수 최소인 백엔드 선택 / 헬스체크 성공→Healthy,
     연속 실패(기본 1회)→Unhealthy 전이.

8. **Proxy(`internal/proxy`)**
   - `/v1/chat/completions`, `/v1/completions` 비스트리밍 프록시(요청/응답 그대로 전달).
   - `stream=true` SSE 프록시(청크 그대로 중계).
   - 가용 백엔드 없음 → 503 `no_available_backend`. vLLM 비정상 응답(HTML/빈 body) → 502 `bad_gateway`.
   - 스트리밍 도중 백엔드 연결 끊김 → 이미 보낸 청크 유지 + 에러 이벤트로 스트림 종료.
     **"연결 끊김"은 TCP RST뿐 아니라, 아직 완결되지 않은 청크(예: `[DONE]`나
     닫는 `data:` 라인 없이 스트림이 끝난 경우)를 읽는 도중 backend 쪽 연결이
     닫힌 경우(graceful FIN 포함)도 포함한다.** 즉 SSE 파서는 "지금까지
     버퍼에 완결되지 않은 이벤트가 남아있는 상태에서 스트림이 끝났는가"를
     기준으로 판단하고, 그런 경우에는 정상 종료가 아니라 `event: error`를
     보내야 한다. 반대로 백엔드가 정상적으로 `[DONE]`(또는 완결된 마지막
     이벤트)까지 다 보내고 연결을 닫은 경우는 정상 종료로 취급한다.
   - 케이스: 비스트리밍 정상 프록시 / 스트리밍 정상 프록시(청크 수 일치) /
     가용 백엔드 없음(503) / 백엔드 5xx는 그대로 전달 / 스트리밍 중 TCP RST로
     연결 끊김 시 부분 응답+에러 이벤트 / 스트리밍 중 미완결 청크 상태에서
     backend가 정상 close(FIN, RST 아님)한 경우에도 동일하게 부분 응답+에러
     이벤트 / 백엔드가 `[DONE]`까지 다 보내고 정상 close한 경우는 에러 이벤트
     없이 정상 종료.

9. **Token Counting + Metrics/Logging(`internal/tokencount`, `internal/metrics`)**
   - `usage_field`(vLLM 응답 `usage.prompt_tokens`/`completion_tokens` 신뢰) /
     `tokenizer`(Gateway 자체 계산) 두 전략, 설정으로 선택(기본 `usage_field`).
     **`tokenizer` 전략은 요청 JSON 전체가 아니라 `messages[].content`
     (completions의 경우 `prompt`)와 응답의 `choices[].message.content`
     (또는 `choices[].text`)만 추출해 그 텍스트에 대해서만 카운트한다** —
     JSON 필드명/구두점까지 토큰으로 잡혀 쿼타·TPM이 부풀려지는 것을 방지하기
     위함(Iteration 02 평가에서 지적된 MINOR 사항 반영). 텍스트 추출에
     실패하는(스키마가 예상과 다른) 경우에만 fallback으로 raw body를 사용해도 된다.
   - TTFT(첫 청크까지), TPOT(청크당 평균), ITL(청크간 간격 배열) 계산.
   - 구조화 JSON 로그(기본, 1요청 1줄) + 텍스트 로그, stdout(기본)/파일 출력 설정.
   - 로그에 `request_id`, `key_id`(해시 아님), `model`, `backend`, `stream`,
     `ttft_ms`, `tpot_ms`, `itl_ms`, `prompt_tokens`, `completion_tokens`, `status` 포함,
     API Key 평문/Authorization 헤더/프롬프트-응답 본문은 미포함.
   - 케이스: `usage_field` 모드 토큰 카운트 / `tokenizer` 모드 토큰 카운트 /
     비스트리밍 요청 로그 1줄 생성(TTFT는 응답 전체 소요시간과 동일 취급 가능) /
     스트리밍 요청 로그에 ITL 배열 포함 / JSON vs 텍스트 포맷 선택 / 로그에
     민감정보(Authorization 값, 프롬프트 본문) 미포함 검증.

10. **Ops 엔드포인트 + `main.go` 조립(wiring) + 엔드투엔드 파이프라인**
    - `GET /healthz`, `GET /readyz`(가용 백엔드 수 반영).
    - `main.go`: 설정 로드 → `FileStore` → 각 컴포넌트(auth/ratelimit/tokenlimit/
      loadbalancer/healthcheck/proxy/metrics) 조립 → Gin 라우터 기동.
    - Gin `Recover` 미들웨어로 패닉 → 500(클라이언트에 스택 미노출, 로그에만 기록).
    - 케이스: `/healthz` 200 / `/readyz` 가용 백엔드 있으면 200, 없으면 503 /
      전체 파이프라인 통합 테스트(모의 vLLM 백엔드에 대해 인증→rate
      limit→token limit→로드밸런싱→프록시→로그까지 한 번에 검증, 최소 1개
      비스트리밍 + 1개 스트리밍 시나리오) / 관리자 API로 발급한 key로 실제
      `/v1/chat/completions` 호출 성공.

11. **빌드/배포(Makefile, Dockerfile, README, config.yaml.example)**
    - `Makefile`: `make build OS=darwin ARCH=arm64` 등으로 크로스 컴파일
      타겟(최소 darwin/linux × amd64/arm64 4종), `make test`, `make docker` 타겟.
    - `Dockerfile`: multi-stage, `linux/amd64`+`linux/arm64` buildx 빌드 가능.
    - `config.yaml.example`(브리프 §7 예시와 동일 구조), `.gitignore`(data/, logs/, 실 config.yaml).
    - `README.md`: 빌드/실행/설정/관리자 API 사용 예시.
    - 케이스: `make build OS=linux ARCH=amd64`로 바이너리 생성 확인 /
      `make test`가 `go test ./...`를 실행하고 exit 0 / `docker build`(buildx
      플랫폼 지정)가 실패 없이 완료(로컬 CI 환경에서 검증 가능한 범위로 제한).

## 인수 기준(Acceptance criteria)

1. `go build ./...`가 exit 0로 성공한다.
2. `go vet ./...`가 exit 0로 성공한다.
3. `go test ./... -count=1`이 exit 0로 성공하고, `internal/config`,
   `internal/storage`, `internal/admin`, `internal/auth`, `internal/ratelimit`,
   `internal/tokenlimit`, `internal/loadbalancer`, `internal/healthcheck`,
   `internal/proxy`, `internal/tokencount`, `internal/metrics` 각 패키지에
   `_test.go` 파일이 최소 1개 이상 존재한다.
4. `admin.token`이 해석되지 않은 상태(`ADMIN_TOKEN` 미설정)로 실행하면
   프로세스가 기동하지 않고 non-zero exit code와 stderr에 원인 메시지를 남긴다.
5. 존재하지 않는 `--config` 경로로 실행하면 non-zero exit + stderr 메시지.
6. `POST /admin/keys`(정상 관리자 토큰 + `{"name":"team-a"}`)가 201과
   `key_id`, `key`(`sk-`로 시작하는 평문), `limits` 필드를 포함한 JSON을 반환한다.
7. 같은 key_id로 `GET /admin/keys` 응답에는 `key`/`key_hash` 필드가 없다.
8. 관리자 토큰 없이 `POST /admin/keys` 호출 시 401과
   `{"error":{"type":"authentication_error","code":"invalid_admin_token"}}` 형태 응답.
9. `DELETE /admin/keys/{존재하지 않는 id}` 호출 시 404와 `code":"key_not_found"`.
10. 6번에서 발급받은 평문 key로 `POST /v1/chat/completions` 호출(모의 vLLM
    백엔드 대상) 시 200과 vLLM 응답이 그대로 전달된다.
11. `Authorization` 헤더 없이 `/v1/chat/completions` 호출 시 401,
    `code":"invalid_api_key"`.
12. 발급된 key의 `rpm`을 1로 설정한 뒤 연속 2회 요청 시 두 번째 요청이 429와
    `Retry-After` 헤더를 반환한다.
13. 요청 body의 `max_tokens`가 key의 `limits.max_tokens_per_request`를
    초과하면 400과 `code":"max_tokens_exceeded"`.
14. 쿼타(`quota_tokens`)를 이미 소진한 key로 요청 시 429와
    `code":"quota_exceeded"`.
15. 백엔드가 0개(전부 Unhealthy)인 상태로 `/v1/chat/completions` 호출 시
    503과 `code":"no_available_backend"`.
16. 백엔드 2개 이상을 설정하고 `round_robin_healthcheck` 전략일 때, 연속
    N회 요청이 백엔드 사이에서 순환 분배됨을 테스트로 확인한다(모의 서버
    호출 횟수 비교).
17. 헬스체크 대상 백엔드를 강제로 실패하게 만들면(healthcheck 실패
    시뮬레이션), 이후 요청이 그 백엔드로 라우팅되지 않는다.
18. `stream=true` 요청에 대해 응답 `Content-Type`이
    `text/event-stream`이며, 모의 vLLM이 보낸 SSE 청크 수와 클라이언트가
    수신한 청크 수가 일치한다.
19. 스트리밍 도중 백엔드와의 연결이 끊기면(TCP RST **또는** 아직 완결되지
    않은 청크를 기다리던 중 backend가 연결을 닫는 graceful close 모두 포함),
    클라이언트는 이미 전송된 청크 이후 에러 이벤트를 수신하고 연결이 정상
    종료된다(패닉/행 없음). 단, 백엔드가 `[DONE]`(또는 완결된 마지막
    이벤트)까지 모두 보낸 뒤 연결을 닫은 경우는 정상 종료로 취급하며 에러
    이벤트를 보내지 않는다. 두 케이스(RST / graceful close 중 미완결 상태)를
    모두 테스트로 검증한다.
20. `token_counting.method: usage_field`(기본값)일 때 로그의
    `prompt_tokens`/`completion_tokens`가 모의 vLLM 응답의 `usage` 필드 값과
    일치한다.
21. `token_counting.method: tokenizer`로 설정 시 Gateway가 자체 계산한 토큰
    수가 로그에 기록된다(usage 필드와 다른 값이어도 됨 — 계산 로직이
    호출됐는지만 검증). 이 값은 요청/응답 JSON 전체가 아니라
    `messages[].content`/`prompt`/`choices[].message.content`(or `.text`)
    등 실제 텍스트 콘텐츠만 대상으로 계산됐음을, `"model":`·`"role":"user"`
    같은 JSON 필드명/구두점을 제거한 순수 텍스트만 넣은 요청과 넣은 요청+
    JSON 오버헤드가 많은 요청을 비교해 토큰 수가 텍스트 길이에 비례하고
    JSON 오버헤드 크기에 비례하지 않음을 테스트로 확인한다.
22. `logging.format: json`(기본)일 때 stdout(또는 지정 파일)에 요청당 1줄의
    유효한 JSON이 기록되고, 그 JSON에 `ttft_ms`, `tpot_ms`, `itl_ms`,
    `prompt_tokens`, `completion_tokens`, `status`, `request_id`, `key_id`
    필드가 존재한다. `key_id`는 있되 `key_hash`/평문 key/`Authorization` 값/
    프롬프트-응답 본문은 로그 어디에도 없다.
23. `logging.format: text`로 설정 시 사람이 읽을 수 있는 단일 라인 텍스트
    로그가 생성된다(JSON 파싱 대상이 아니어도 됨).
24. `GET /healthz`가 인증 없이 200과 `{"status":"ok"}`를 반환한다.
25. `GET /readyz`가 가용 백엔드가 1개 이상이면 200과
    `healthy_backends`/`total_backends` 필드를 포함해 반환하고, 가용 백엔드가
    0개이면 503을 반환한다.
26. 패닉을 유발하는 내부 오류가 발생해도 프로세스가 죽지 않고 500을
    반환한다(Gin Recover 미들웨어 동작 확인, 최소 핸들러 유닛 테스트로 검증).
27. `data/keys.json`, `data/usage.json` 생성 시 파일 권한이 `0600`이다
    (`os.Stat` 결과의 `Mode().Perm()`으로 검증).
27a. **클린 체크아웃 최초 기동 시나리오**: `data/`, `logs/` 디렉터리가
    아예 존재하지 않는 새 작업 디렉터리에서(직접 `mkdir data`/`mkdir logs`를
    실행하지 않고) README의 "빌드/실행" 절차만 그대로 따라(`cp
    config.yaml.example config.yaml`, `ADMIN_TOKEN` export, 바이너리 실행)
    프로세스를 기동하면, non-zero exit 없이 정상 기동하고 `data/keys.json`,
    `data/usage.json`이 `0600` 권한으로 자동 생성된다(`GET /healthz` 200
    확인). 이 시나리오는 `internal/storage`의 유닛 테스트(마일스톤 2의
    "부모 디렉터리 미존재" 케이스)와 별개로, Evaluator가 실제 클린
    디렉터리에서 재현 가능해야 한다.
28. 관리자 토큰이 소스코드에 하드코딩되어 있지 않다 — 저장소 전체
    `grep -rn "admin.*token\s*=\s*\"" --include=*.go`(또는 이에 준하는 검사)
    결과가 리터럴 비밀값을 포함하지 않는다.
29. `make build OS=linux ARCH=amd64`와 `make build OS=darwin ARCH=arm64`
    실행 시 각각 대응하는 바이너리 파일이 생성된다(파일 존재 확인).
30. `Dockerfile`이 존재하고 `docker buildx build --platform
    linux/amd64,linux/arm64 .`(또는 로컬 CI 제약상 단일 플랫폼 `docker build .`)이
    에러 없이 완료된다. **평가 환경에 `docker`가 설치되어 있지 않은 경우**에는
    실제 빌드 대신 `Dockerfile` 정적 검토로 대체할 수 있다 — 이때는 (a)
    multi-stage 빌드(builder + 최종 런타임 스테이지)인지, (b) `ARG
    TARGETOS`/`ARG TARGETARCH`를 받아 `GOOS`/`GOARCH`로 넘기는지, (c)
    non-root `USER`로 실행하는지, (d) `EXPOSE`/`ENTRYPOINT`가 존재하는지
    4가지를 확인하면 이 기준을 충족한 것으로 간주한다.
31. `config.yaml.example`, `README.md`, `.gitignore`(`data/`, `logs/`,
    `config.yaml` 포함)가 저장소 루트에 존재한다.
32. `go.mod`에 명시된 외부 의존성이 모두 MIT/Apache-2.0/BSD 계열 라이선스임을
    README 또는 spec 문서 내 표로 확인할 수 있다(브리프 §7.5 표를 유지/갱신).

## 범위 제외(Out of scope)

브리프 §9(prd.md) 그대로:

- Redis/RDB 스토리지 구현체 (인터페이스만 준비, 구현은 후속)
- 다중 Gateway 인스턴스 간 분산 상태 동기화
- 조직/테넌트 단위 쿼타, 멀티테넌시
- 관리자 웹 UI (관리자 REST API만 제공)
- Key 자기발급(셀프서비스) API
- Gateway 자체 TLS 종료
- vLLM 외 다른 추론 서버(TGI 등) 지원
- vLLM 응답에 대한 자동 재시도(브리프 sdd.md §8.4 근거: 스트리밍 중복 응답
  위험 + 비멱등성)

## Assumptions

- **Go 버전**: 브리프가 "최신 안정 버전"이라고만 명시했으므로 Go 1.22+ 로
  가정한다. Generator는 실행 환경에 설치된 최신 안정 버전을 `go.mod`의
  `go` 지시자에 명시하면 된다.
- **Rate limit 알고리즘 구체화**: 브리프 sdd.md는 "슬라이딩 윈도우/토큰버킷"
  둘 다 언급만 하고 확정하지 않았다. 구현 단순성을 위해 **고정 윈도우(초/분
  단위 카운터 리셋) 방식**으로 구현하는 것으로 가정한다. RPS는 1초 윈도우,
  RPM/TPM은 60초 윈도우로 계산한다. `Retry-After`는 현재 윈도우 종료까지
  남은 초를 올림 처리해 반환한다.
- **Tokenizer 전략 구현**: `token_counting.method: tokenizer`용 자체
  토크나이저는 특정 모델 vocab과 100% 일치할 필요는 없다고 가정한다(브리프가
  구체적 알고리즘을 지정하지 않음). MIT/Apache-2.0 라이선스의 오픈소스
  BPE 토크나이저 라이브러리(예: tiktoken 호환 Go 구현) 사용 또는 공백/서브워드
  근사 카운팅 중 Generator가 선택하되, 반드시 §7.5 라이선스 표에 추가·기재한다.
- **헬스체크 실패 판정 기준**: 브리프가 "연속 실패 N회(기본 1회)"라고
  명시했으므로 기본값 1회(즉시 Unhealthy 전이)로 구현한다. 헬스체크 요청은
  vLLM이 제공하는 `/health` 또는 이에 준하는 경로에 GET하는 것으로 가정한다
  (vLLM 표준 헬스 엔드포인트가 별도로 명시되지 않았으므로 config에서 경로를
  하드코딩하지 않고 `http://backend-base-url/health` 규칙을 기본값으로 가정).
- **usage.json flush 주기**: 브리프 sdd.md §6.3의 "기본 5초"를 그대로
  채택한다. 정상 종료(SIGTERM) 시 flush 후 종료하는 graceful shutdown을
  구현하는 것으로 가정한다(브리프가 "정상 종료 시 flush"를 명시했으므로).
  `kill -9` 등 비정상 종료 시 유실은 브리프가 이미 감수한 트레이드오프다.
- **모의(mock) vLLM 백엔드**: 인수 기준의 프록시/로드밸런싱/헬스체크/
  스트리밍 관련 테스트는 실제 vLLM 대신 Go `net/http/httptest` 기반 모의
  서버로 검증하는 것으로 가정한다(외부 vLLM 인스턴스 의존 없이 CI에서
  재현 가능해야 하므로).
- **패닉 유발 테스트(인수 기준 26)**: 실제 프로덕션 코드에 의도적 패닉을
  넣지 않고, 테스트 전용 핸들러 또는 Gin 미들웨어 유닛 테스트로 Recover
  동작을 검증하는 것으로 가정한다.
- **부모 디렉터리 자동 생성 (Iteration 01 평가에서 CRITICAL로 지적)**:
  Iteration 01의 `FileStore` 구현이 `data/` 부모 디렉터리를 생성하지 않아
  `.gitignore`로 `data/`가 제외된 클린 체크아웃에서 README의 "빌드/실행"
  절차를 그대로 따르면 최초 기동이 즉시 실패하는 것이 확인됐다(모든
  스토리지 테스트가 `t.TempDir()`처럼 이미 존재하는 디렉터리만 부모로
  사용해 이 버그를 가려왔음). 이번 수정은 데이터 모델 절과 마일스톤 2,
  인수 기준 27/27a에 "부모 디렉터리 미존재 시에도 자동 생성"을 명시적으로
  요구하고, 이를 놓치지 않도록 하는 회귀 테스트 케이스와 클린 체크아웃
  재현 시나리오를 추가하는 것으로 범위를 최소화해 반영했다. 로그 파일
  출력 경로(`logs/`)도 동일한 클래스의 버그이므로 함께 명시했다(구현
  비용이 사실상 0에 가까운 대칭적 보강).
- **Iteration 02 평가(PASS, MINOR 3건) 반영**: verdict는 PASS였고 CRITICAL/
  MAJOR는 없었으므로 스택/아키텍처/마일스톤 구조는 그대로 두고, 지적된
  MINOR 3건만 문구를 좁혀 명시했다. (1) 스트리밍 중 연결 끊김이 TCP RST일
  때만 에러 이벤트가 나가고 graceful FIN(미완결 청크 상태에서 정상 close)일
  때는 그냥 스트림이 끝나버리는 문제 — 마일스톤 8과 인수 기준 19에 "미완결
  상태에서의 graceful close도 RST와 동일하게 에러 이벤트를 내야 한다"는
  요구와 그 반대 케이스(완결 후 정상 close는 에러 없음)를 명시해 다음
  Generator 호출이 `splitSSEEvents`의 EOF 분기를 고치도록 유도했다. (2)
  `tokenizer` 모드가 raw JSON 전체를 카운트해 쿼타/TPM이 JSON 오버헤드로
  부풀려지는 문제 — 마일스톤 9와 인수 기준 21에 "메시지/응답 콘텐츠
  텍스트만 카운트"를 명시하고, 이를 검증할 수 있는 비교 테스트(순수 텍스트
  vs JSON 오버헤드 많은 요청)를 요구했다. (3) Docker 미설치 평가 환경에서
  기준 30을 실행으로 검증할 수 없었던 문제 — 이는 Generator 결함이 아니라
  환경 제약이므로 기능을 바꾸지 않고, 인수 기준 30에 "docker 미설치 시
  Dockerfile 정적 검토로 대체 가능"이라는 명시적 대체 경로만 추가해 앞으로도
  이 기준이 불필요하게 미확정 상태로 남지 않도록 했다. 세 항목 모두 기존
  스코프를 넘어서는 새 기능이 아니라 이미 있는 요구사항의 문구를 정밀화한
  것이다.
