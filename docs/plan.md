# plan.md — llm-gateway TDD 체크리스트

## Phase 0: 스캐폴딩
- [x] Step 0: git init(.gitignore 커밋), go.mod 초기화, 폴더 구조 생성

## Phase 1: 설정 로더 (internal/config)
- [x] Step 1: 정상 YAML 로드 + 구조체 매핑
- [x] Step 2: `${VAR}` 환경변수 치환(존재하는 경우 치환됨)
- [x] Step 3: `admin.token`이 미해결(`${ADMIN_TOKEN}` 그대로) 상태면 에러 반환
- [x] Step 4: 잘못된 YAML이면 에러 반환
- [x] Step 5: `--config` 플래그 / `LLM_GATEWAY_CONFIG` 환경변수 / 기본값 우선순위 로직(LoadPath 함수)
- [x] Step 6: 존재하지 않는 config 경로 에러

## Phase 2: 스토리지 계층 (internal/storage)
- [x] Step 7: Store 인터페이스, KeyRecord/UsageRecord 타입 정의
- [x] Step 8: FileStore.CreateKey + GetKeyByHash(존재)
- [x] Step 9: FileStore.GetKeyByHash(미존재 → false)
- [x] Step 10: FileStore.RevokeKey (soft delete, revoked_at 설정)
- [x] Step 11: FileStore.ListKeys
- [x] Step 12: FileStore.GetUsage(없으면 0 값)
- [x] Step 13: FileStore.IncrementUsage(누적)
- [x] Step 14: FileStore.ResetUsage
- [x] Step 15: 파일 없으면 최초 기동 시 빈 구조 자동 생성 + 0600 권한 검증

## Phase 3: 에러 헬퍼 + 관리자 API
- [x] Step 16: apierror 헬퍼(OpenAI 호환 JSON 응답 생성 함수)
- [x] Step 17: admin 인증 미들웨어(상수시간 비교) - 토큰 없음/불일치 401
- [x] Step 18: POST /admin/keys 정상 발급(201, key_id/key/limits)
- [x] Step 19: POST /admin/keys name 누락 400
- [x] Step 20: GET /admin/keys 목록(key_hash/평문 미포함)
- [x] Step 21: DELETE /admin/keys/{key_id} 정상 폐기
- [x] Step 22: DELETE /admin/keys/{존재하지 않는 id} 404

## Phase 4: 클라이언트 인증 미들웨어 (internal/auth)
- [x] Step 23: 유효 key 통과 + KeyRecord 컨텍스트 전달
- [x] Step 24: Authorization 헤더 누락 401
- [x] Step 25: 알 수 없는 key 401
- [x] Step 26: 폐기된 key 401

## Phase 5: Rate Limiter (internal/ratelimit)
- [x] Step 27: 한도 이내 요청 통과
- [x] Step 28: RPM 초과 시 429 + Retry-After
- [x] Step 29: TPM 초과 시 429 + Retry-After
- [x] Step 30: 한도 0 = 즉시 차단
- [x] Step 31: 서로 다른 key는 독립적으로 카운트

## Phase 6: Token Limiter (internal/tokenlimit)
- [x] Step 32: max_tokens 상한 이내 통과
- [x] Step 33: max_tokens 상한 초과 400
- [x] Step 34: 쿼타 소진 429
- [x] Step 35: 쿼타 여유 있음(통과 후 IncrementUsage 호출 확인)
- [x] Step 36: daily/monthly period 키 계산 함수

## Phase 7: Load Balancer + Health Checker
- [x] Step 37: 백엔드 1개면 항상 그것만 선택
- [x] Step 38: 백엔드 N개 라운드로빈 순환
- [x] Step 39: Unhealthy 백엔드 제외
- [x] Step 40: 모든 백엔드 Unhealthy면 Pick() 에러 반환
- [x] Step 41: LeastRequests 전략 - 진행 중 요청 수 최소 백엔드 선택
- [x] Step 42: HealthChecker 성공→Healthy 전이
- [x] Step 43: HealthChecker 연속 실패(1회)→Unhealthy 전이

## Phase 8: Proxy (internal/proxy)
- [x] Step 44: 비스트리밍 정상 프록시(요청/응답 그대로 전달)
- [x] Step 45: 가용 백엔드 없음 503
- [x] Step 46: 백엔드 5xx 그대로 전달
- [x] Step 47: vLLM 비정상 응답(HTML/빈 body) 502
- [x] Step 48: 스트리밍 정상 프록시(SSE 청크 수 일치)
- [x] Step 49: 스트리밍 중 연결 끊김 시 부분 응답 + 에러 이벤트

## Phase 9: Token Counting + Metrics/Logging
- [x] Step 50: usage_field 모드 토큰 카운트
- [x] Step 51: tokenizer 모드 토큰 카운트
- [x] Step 52: TTFT/TPOT/ITL 계산 로직
- [x] Step 53: 비스트리밍 요청 로그 1줄(JSON) 생성 + 필드 검증
- [x] Step 54: 스트리밍 요청 로그에 itl_ms 배열 포함
- [x] Step 55: text 포맷 로그 생성
- [x] Step 56: 로그에 민감정보(Authorization, 프롬프트 본문) 미포함 검증

## Phase 10: Ops 엔드포인트 + main.go 조립 + E2E
- [x] Step 57: GET /healthz 200
- [x] Step 58: GET /readyz 가용 백엔드 있음(200)/없음(503)
- [x] Step 59: Gin Recover 미들웨어 - 패닉 시 500 (핸들러 유닛 테스트)
- [x] Step 60: main.go 조립 + 전체 파이프라인 통합 테스트(비스트리밍)
- [x] Step 61: 전체 파이프라인 통합 테스트(스트리밍)
- [x] Step 62: 관리자 API로 발급한 key로 실제 /v1/chat/completions 호출 성공 E2E

## Phase 11: 빌드/배포
- [x] Step 63: Makefile(build/test/docker 타겟) + 크로스 컴파일 검증
- [x] Step 64: Dockerfile(multi-stage, buildx) — 로컬에 docker 미설치로 buildx 실행은 검증 못함(Dockerfile 정적 검토만)
- [x] Step 65: config.yaml.example, .gitignore, README.md 작성
- [x] Step 66: 의존성 라이선스 표를 README에 기재

## Phase 12: CRITICAL 수정 — data/logs 부모 디렉터리 자동 생성 (Iteration 02, 인수 기준 27a)
- [x] Step 67: `internal/storage.NewFileStore`가 부모 디렉터리(`data/`)가
  없는 경로에서도 `MkdirAll` 후 성공하는지 검증하는 회귀 테스트 추가 +
  `ensureJSONFile`에 `os.MkdirAll(filepath.Dir(path), 0o700)` 적용
- [x] Step 68: `cmd/llm-gateway`의 `openLogWriter`가 `logging.output: file`일
  때 로그 파일의 부모 디렉터리(`logs/`)가 없어도 자동 생성하는지 검증하는
  `buildEngine` 통합 테스트 추가 + `os.MkdirAll` 적용
- [x] Step 69: 실제 클린 체크아웃(git archive)에서 README "빌드/실행" 절차만
  그대로 따라(`mkdir data`/`mkdir logs` 없이) 기동해 `/healthz` 200 및
  `data/keys.json`,`data/usage.json`,`logs/*.log` 0600 자동 생성을 라이브로
  재현·검증 (인수 기준 27a)

## Phase 13: MINOR 정밀화 반영 (Iteration 03, 인수 기준 19/21/30 문구 정밀화)
- [x] Step 70: `internal/tokencount`에 `ExtractRequestText`/`ExtractResponseText`
  추가(messages[].content 또는 prompt / choices[].message.content 또는
  choices[].text만 추출, 파싱 실패·필드 없음 시 raw body로 fallback) +
  JSON 오버헤드가 텍스트 토큰 수에 영향을 주지 않음을 검증하는 단위 테스트
- [x] Step 71: `cmd/llm-gateway`의 `countTokens`(tokenizer 모드)가 raw
  request/response body 대신 위 추출 함수를 사용하도록 수정 + 순수 텍스트
  요청과 JSON 오버헤드가 많은 요청의 로그 `prompt_tokens`가 동일함을
  검증하는 `buildEngine` 통합 테스트 (인수 기준 21)
- [x] Step 72: `internal/proxy.splitSSEEvents`가 atEOF 시 미완결(구분자
  없는) 버퍼 데이터를 정상 최종 이벤트로 취급하지 않고 에러로 표면화하도록
  수정 + (a) graceful FIN으로 미완결 이벤트 상태에서 연결이 닫히면 에러
  이벤트가 나가는 테스트, (b) `[DONE]`까지 완결된 이벤트를 다 보낸 뒤 정상
  close하면 에러 이벤트 없이 정상 종료되는 테스트 추가 (인수 기준 19)
- [x] Step 73: 인수 기준 30(Docker) 재검토 — 로컬에 docker 미설치 확인,
  `Dockerfile` 정적 검토로 (a)multi-stage (b)`ARG TARGETOS/TARGETARCH`→
  `GOOS/GOARCH` (c)non-root `USER` (d)`EXPOSE`/`ENTRYPOINT` 4항목 충족 재확인
  (코드 변경 없음, 기존 Dockerfile이 이미 충족)
