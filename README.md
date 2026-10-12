# llm-gateway

vLLM 추론 서버 앞단에 배치되는 Go 기반 단일 프로세스 게이트웨이입니다.
API Key 기반 인증, 요청/토큰 사용량 제어(Rate Limit·Quota), 다중 vLLM
백엔드 로드밸런싱(+헬스체크), 성능 관측(TTFT/TPOT/ITL)을 담당합니다.
클라이언트는 기존 OpenAI SDK의 `base_url`만 이 게이트웨이 주소로 바꾸면
그대로 사용할 수 있습니다.

## 빌드

Go 1.25+ 가 설치되어 있어야 합니다.

```bash
# 호스트 플랫폼용 빌드 (./bin/llm-gateway-<os>-<arch> 생성)
make build

# 크로스 컴파일 (darwin/linux × amd64/arm64)
make build OS=linux ARCH=amd64
make build OS=linux ARCH=arm64
make build OS=darwin ARCH=amd64
make build OS=darwin ARCH=arm64
```

## 테스트

```bash
make test   # go test ./... -count=1
make vet    # go vet ./...
```

패키지별로 최소 1개 이상의 `_test.go`가 있으며, TDD(Red-Green-Refactor)로
작성되었습니다.

## 실행

1. `config.yaml.example`을 복사해 `config.yaml`을 만듭니다 (`config.yaml`은
   `.gitignore`에 포함되어 커밋되지 않습니다).

   ```bash
   cp config.yaml.example config.yaml
   ```

2. 관리자 토큰을 환경변수로 주입합니다 (`config.yaml`에는
   `${ADMIN_TOKEN}`처럼 참조만 남기고 평문을 직접 쓰지 않습니다).

   ```bash
   export ADMIN_TOKEN="a-strong-random-secret"
   ```

3. 실행합니다.

   ```bash
   ./bin/llm-gateway-darwin-arm64 --config ./config.yaml
   # 또는
   LLM_GATEWAY_CONFIG=./config.yaml ./bin/llm-gateway-darwin-arm64

   # 혹은
   export ADMIN_TOKEN="a-strong-random-secret"
   go run ./cmd/llm-gateway --config ./config.yaml
   ```

   설정 파일 경로 우선순위: `--config` 플래그 > `LLM_GATEWAY_CONFIG`
   환경변수 > 기본값(`./config.yaml`).

   `admin.token`이 해석되지 않거나(예: `ADMIN_TOKEN` 미설정), `--config`
   경로가 존재하지 않으면 프로세스는 기동하지 않고 non-zero exit code와
   함께 stderr에 원인을 출력합니다.

4. `SIGTERM`/`SIGINT`를 받으면 진행 중인 요청을 정리하고(graceful
   shutdown), 누적 토큰 사용량(`data/usage.json`)을 flush한 뒤 종료합니다.

## 설정 (`config.yaml`)

`config.yaml.example` 참고. 주요 항목:

| 키 | 설명 |
|---|---|
| `server.listen_addr` | 게이트웨이가 바인딩할 주소 (예: `:8080`) |
| `admin.token` | 관리자 API 인증 토큰. `${ADMIN_TOKEN}` 형태로 환경변수 참조 |
| `backends[].url` | vLLM 백엔드 URL 목록 (1개 이상) |
| `load_balancer.strategy` | `round_robin_healthcheck` \| `least_requests` |
| `load_balancer.health_check_interval` | 헬스체크 주기 (예: `5s`) |
| `rate_limit.default.rpm` / `tpm` | Key 발급 시 값을 지정하지 않으면 쓰이는 기본 RPM/TPM |
| `token_limit.default.*` | 요청당 max_tokens 상한 / 쿼타 주기·상한의 기본값 |
| `token_counting.method` | `usage_field`(vLLM 응답의 usage 필드 신뢰, 기본값) \| `tokenizer`(게이트웨이 자체 근사 계산) |
| `storage.keys_file` / `usage_file` | `data/keys.json`, `data/usage.json` 경로 (0600 권한으로 자동 생성) |
| `logging.format` | `json`(기본) \| `text` |
| `logging.output` | `stdout`(기본) \| `file` (`logging.file_path`) |

## 관리자 API 사용 예시

모든 `/admin/*` 요청에는 `Authorization: Bearer <ADMIN_TOKEN>`이 필요합니다.

```bash
# Key 발급 (평문 key는 이 응답에서만 노출됩니다)
curl -X POST http://localhost:8080/admin/keys \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"team-a","rpm":60,"tpm":100000}'

# 발급된 Key 목록 (key_hash/평문 미포함)
curl http://localhost:8080/admin/keys \
  -H "Authorization: Bearer $ADMIN_TOKEN"

# Key 폐기 (soft delete)
curl -X DELETE http://localhost:8080/admin/keys/key_9f1c2a \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

### 어떤 제한에도 걸리지 않는(사실상 무제한) Key 발급

llm-gateway에는 "무제한"을 뜻하는 별도 값(예: `0`, `-1`)이 없습니다. 오히려
`rpm: 0`처럼 0을 주면 **즉시 모든 요청이 차단**됩니다(`internal/ratelimit`이
`현재 카운트 >= limits.RPM`으로 비교하기 때문에 한도가 0이면 항상 초과 상태).
따라서 "무제한"은 **실질적으로 절대 도달하지 않을 만큼 큰 값**을 지정해서
구현합니다:

```bash
curl -X POST http://localhost:8080/admin/keys \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "unlimited-key",
    "rpm": 1000000000,
    "tpm": 1000000000,
    "max_tokens_per_request": 1000000,
    "quota_period": "monthly",
    "quota_tokens": 1000000000000
  }'
```

- `rpm`/`tpm`: 분당 10억 — 어떤 실사용 트래픽도 이 값을 넘지 않습니다.
- `max_tokens_per_request`: 100만 — 어떤 모델의 `max_tokens`보다도 큽니다.
- `quota_tokens`: 1조(월간) — 사실상 소진 불가능한 양입니다.
- `name`만 필수이고 나머지 필드를 생략하면 `config.yaml`의
  `rate_limit.default`/`token_limit.default` 값이 대신 적용되므로, 반드시
  위처럼 모든 한도 필드를 명시적으로 큰 값으로 지정해야 합니다.
- 요청/응답 형식은 위 "Key 발급"과 동일하며, 평문 `key`는 이 응답에서만
  노출됩니다.

## 클라이언트 사용 예시 (OpenAI SDK 호환)

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-..." \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen2.5-7b","messages":[{"role":"user","content":"hi"}]}'
```

Python OpenAI SDK를 사용한다면 `base_url`만 게이트웨이 주소로 바꾸면
됩니다:

```python
from openai import OpenAI
client = OpenAI(base_url="http://localhost:8080/v1", api_key="sk-...")
```

## Ops 엔드포인트

- `GET /healthz` — 프로세스 liveness (인증 불필요). `{"status":"ok"}`
- `GET /readyz` — 가용(HEALTHY) 백엔드가 1개 이상이면 200, 0개면 503.
  `{"status":"ready","healthy_backends":N,"total_backends":M}`

## Docker

```bash
# 로컬 단일 플랫폼 빌드
docker build -t llm-gateway .

# buildx 멀티 플랫폼 빌드
docker buildx build --platform linux/amd64,linux/arm64 -t llm-gateway .

docker run --rm -p 8080:8080 \
  -e ADMIN_TOKEN=a-strong-random-secret \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  -v $(pwd)/data:/app/data \
  llm-gateway
```

## 아키텍처

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

- 단일 프로세스·단일 인스턴스 전제(파일 기반 상태 저장). 다중 인스턴스로
  확장하려면 `internal/storage.Store` 인터페이스를 만족하는 Redis/RDB
  구현체를 추가하면 됩니다(현재 범위 밖, 인터페이스만 준비).
- `internal/`은 도메인 단위로 분리됩니다: `config`, `storage`, `admin`,
  `auth`, `ratelimit`, `tokenlimit`, `loadbalancer`, `healthcheck`,
  `proxy`, `tokencount`, `metrics`, `apierror`.

## 설계상 결정 사항 (Assumptions)

- **Rate limit 알고리즘**: 고정 윈도우(초/분 단위 카운터 리셋) 방식.
  RPM/TPM은 60초 윈도우. `Retry-After`는 현재 윈도우 종료까지 남은 시간을
  올림 처리해 반환합니다. (RPS는 spec의 데이터 모델에 별도 필드가 없어
  RPM/TPM만 구현했습니다.)
- **tokenizer 카운팅 방식**: 외부 BPE 토크나이저 라이브러리 대신,
  공백 기준 근사 카운팅(`strings.Fields`)을 자체 구현했습니다. 특정 모델
  vocab과 100% 일치하지 않지만 계산 로직이 실제로 호출되어 별도 값을
  로그에 남기는 것을 목표로 합니다. 외부 의존성이 없어 별도 라이선스
  검토가 필요 없습니다.
- **헬스체크**: `GET <backend_url>/health`, 기본 5초 주기, 연속 실패
  1회로 Unhealthy 전이, 성공 1회로 Healthy 전이. 신규 백엔드는 첫 헬스체크
  전까지 `Unknown` 상태로 라우팅 후보에서 제외됩니다(cold-start 보호).
- **usage.json 저장 방식**: `FileStore`는 매 `IncrementUsage`/`ResetUsage`
  호출마다 즉시 파일에 flush합니다(spec이 언급한 5초 주기 write-behind
  버퍼링 대신 항상-최신 동기 쓰기를 선택). 정합성은 더 강하지만 I/O
  최적화는 하지 않은 트레이드오프입니다. RPS/RPM/TPM 카운터는 spec대로
  인메모리에만 존재하며 파일에 쓰지 않습니다.
- **패닉 복구 테스트**: Gin `Recovery()` 미들웨어 동작은 프로덕션 코드에
  의도적 패닉을 넣지 않고, 테스트 전용 핸들러(`cmd/llm-gateway` 테스트)로
  검증합니다.

## 의존성 라이선스

| 패키지 | 용도 | 라이선스 |
|---|---|---|
| github.com/gin-gonic/gin | HTTP 라우팅/미들웨어 | MIT |
| gopkg.in/yaml.v3 | config.yaml 파싱 | MIT / Apache-2.0 (dual) |
| github.com/bytedance/sonic, sonic/loader | gin 내부 JSON 인코딩 | Apache-2.0 |
| github.com/bytedance/gopkg | gin 내부 유틸리티 | Apache-2.0 |
| github.com/cloudwego/base64x | gin 내부 인코딩 | Apache-2.0 |
| github.com/gabriel-vasile/mimetype | gin 내부 MIME 감지 | MIT |
| github.com/gin-contrib/sse | gin SSE 지원 | MIT |
| github.com/go-playground/locales, universal-translator, validator/v10 | gin 요청 바인딩 검증 | MIT |
| github.com/goccy/go-json, go-yaml | JSON/YAML 인코딩(전이 의존성) | MIT |
| github.com/json-iterator/go | JSON 인코딩(전이 의존성) | MIT |
| github.com/klauspost/cpuid/v2 | CPU 기능 감지(전이 의존성) | MIT |
| github.com/leodido/go-urn | validator 전이 의존성 | MIT |
| github.com/mattn/go-isatty | 터미널 감지(전이 의존성) | MIT |
| github.com/modern-go/concurrent, reflect2 | json-iterator 전이 의존성 | Apache-2.0 |
| github.com/pelletier/go-toml/v2 | gin 전이 의존성 | MIT |
| github.com/quic-go/qpack, quic-go | gin 전이 의존성(HTTP/3 지원) | MIT |
| github.com/twitchyliquid64/golang-asm | sonic 전이 의존성 | BSD-3-Clause |
| github.com/ugorji/go/codec | gin 전이 의존성 | MIT |
| go.mongodb.org/mongo-driver/v2 | quic-go 전이 의존성 | Apache-2.0 |
| golang.org/x/arch, crypto, net, sys, text | Go 표준 확장 라이브러리 | BSD-3-Clause |
| google.golang.org/protobuf | quic-go 전이 의존성 | BSD-3-Clause |

모두 MIT / Apache-2.0 / BSD-3-Clause 계열이며, 사용이 제한된 라이선스는
포함되어 있지 않습니다.
