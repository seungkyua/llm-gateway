# Iteration 03 report

## Summary
Iteration 03 targeted three MINOR items flagged in the iter_02 (PASS) report:
(1) SSE graceful-close-with-incomplete-buffer not producing an error event,
(2) `tokenizer` token counting inflating on raw JSON instead of only content
text, and (3) Docker-unavailable fallback wording for AC30. All three are
now fixed/clarified and verified: `splitSSEEvents` correctly distinguishes
"stream ended with an unfinished event" (RST or graceful FIN) from "stream
ended cleanly after a complete final event," backed by three genuine
raw-TCP unit tests; the tokenizer strategy now extracts only
`messages[].content`/`prompt`/`choices[].message.content`/`choices[].text`
before counting, verified live end-to-end with a JSON-overhead-heavy
request producing an identical token count to a minimal request with the
same content text; and the Dockerfile was statically re-verified against
the 4-point checklist (docker is not installed in this sandbox, consistent
with the spec's documented fallback).

Beyond the plan's listed changes, I independently re-ran the full
acceptance-criteria matrix end-to-end against this build: `go build`/`go
vet`/`go test ./... -count=1` all pass; a real `git archive` clean checkout
was built and booted with zero manual directory setup, confirming the
Iteration 01/02 CRITICAL (missing parent-directory auto-creation) stays
fixed for both `data/` and `logs/`; a Python mock vLLM backend was used to
drive admin key issuance/listing/revocation, client auth, RPM rate
limiting, max_tokens/quota enforcement, round-robin load balancing across
2 backends, health-check-driven backend exclusion, streaming and
non-streaming proxying, 500/HTML backend response handling, and both JSON
and text log formats — all matched spec.md's numbered acceptance criteria
and error-matrix codes exactly. No hardcoded secrets, no CLAUDE.md
violations, no CRITICAL or MAJOR findings. Three MINOR notes are recorded
below (all either pre-existing spec ambiguity or disclosed, low-impact
design trade-offs), none of which block PASS.

## Commands run
- `go version` → go1.25.0 darwin/arm64
- `go build ./...` (in project/) → exit 0. AC1 MET.
- `go vet ./...` (in project/) → exit 0. AC2 MET.
- `go test ./... -count=1` (in project/) → exit 0, all 13 packages `ok`. AC3 (build) MET.
- Verified `_test.go` present in every package named by AC3 (config, storage, admin, auth, ratelimit, tokenlimit, loadbalancer, healthcheck, proxy, tokencount, metrics) — all ≥1 (auth and metrics have 2). AC3 MET.
- `git archive HEAD | tar -x` into a scratch dir to reproduce a truly clean checkout (no `data/`, no `logs/`), then followed README steps verbatim (`go build`, `cp config.yaml.example config.yaml`, `export ADMIN_TOKEN=...`, run binary) with `logging.output: stdout` → `data/keys.json`/`data/usage.json` auto-created at `0600`, `/healthz` returned 200. Repeated with `logging.output: file` → `logs/llm-gateway.log` auto-created at `0600`. AC27, AC27a MET (live reproduction, not just unit tests).
- Ran a Python `http.server`-based mock vLLM backend (chat/completions non-stream, SSE stream, `/health`, and trigger models for 500/HTML responses) and exercised the gateway end-to-end via curl:
  - `POST /admin/keys` (valid token) → 201 with `key_id`/`key` (`sk-...`)/`limits`. AC6 MET.
  - `GET /admin/keys` → list has no `key`/`key_hash` fields. AC7 MET.
  - `POST /admin/keys` without admin token → 401 `invalid_admin_token`. AC8 MET.
  - `DELETE /admin/keys/key_doesnotexist` → 404 `key_not_found`. AC9 MET.
  - Issued key → `POST /v1/chat/completions` → 200, mock response relayed unchanged. AC10 MET.
  - No `Authorization` header → 401 `invalid_api_key`. AC11 MET.
  - Key with `rpm:1`, 2nd request within window → 429 + `Retry-After: 56`. AC12 MET.
  - Key with `max_tokens_per_request:50`, request `max_tokens:500` → 400 `max_tokens_exceeded`. AC13 MET.
  - Key with `quota_tokens:5`, second call after usage=19 tokens recorded → 429 `quota_exceeded`. AC14 MET.
  - Killed mock backend, health check (1s interval) marked it Unhealthy → `/v1/chat/completions` → 503 `no_available_backend`, `/readyz` → 503. AC15, AC17, AC25 MET.
  - `stream:true` → `Content-Type: text/event-stream`, 3 sent SSE chunks + `[DONE]` all relayed. AC18 MET (chunk-count match observed).
  - `model:"trigger-500"` → backend 500 body relayed unchanged (500). AC (matrix row "vLLM 5xx passthrough", supports milestone 8 case) confirmed.
  - `model:"trigger-html"` → 502 `bad_gateway`. Matrix row confirmed.
  - Two mock backends + `round_robin_healthcheck`, 4 sequential requests → 2/2 split confirmed via log `backend` field grep. AC16 MET.
  - `token_counting.method: tokenizer`: sent one request with plain-text `messages[].content` and one with heavy JSON padding/extra fields but identical content text → both logged `prompt_tokens:4` (word count of the shared content), proving JSON overhead does not inflate the tokenizer count. AC21 MET (live, not just unit test).
  - `logging.format: json` request log line inspected: contains `request_id`,`key_id`,`model`,`backend`,`stream`,`ttft_ms`,`tpot_ms`,`itl_ms`,`prompt_tokens`,`completion_tokens`,`status`; `grep -i "authorization\|sk-\|mock response text\|\"hi\""` over the full server log returned no matches (exit 1). AC22 MET.
  - `logging.format: text` → single human-readable `key=value` line produced. AC23 MET.
  - `GET /healthz` → 200 `{"status":"ok"}` (observed repeatedly across runs). AC24 MET.
- Ran gateway with `ADMIN_TOKEN` unset → non-zero exit (1) + stderr `admin.token is required but unresolved`. AC4 MET.
- Ran gateway with `--config ./does-not-exist.yaml` → non-zero exit (1) + stderr file-not-found message. AC5 MET.
- `grep -rn 'admin.*token\s*=\s*"' --include='*.go' .` in project/ → no matches (exit 1). AC28 MET.
- `make build OS=linux ARCH=amd64` and `make build OS=darwin ARCH=arm64` (BUILD_DIR overridden to scratch dir) → both exit 0, binaries produced (`llm-gateway-linux-amd64` 31MB, `llm-gateway-darwin-arm64` 30MB). AC29 MET.
- `which docker` → not found in this environment. Per spec's explicit fallback for AC30, performed a static review of `Dockerfile`: (a) multi-stage `golang:1.25-alpine AS build` → `alpine:3.20` runtime — present; (b) `ARG TARGETOS`/`ARG TARGETARCH` piped into `GOOS=${TARGETOS} GOARCH=${TARGETARCH}` — present; (c) `USER gateway` (non-root, created via `addgroup`/`adduser`) — present; (d) `EXPOSE 8080` and `ENTRYPOINT ["/usr/local/bin/llm-gateway"]` — present. AC30 MET (static review, docker unavailable in this sandbox).
- Confirmed `config.yaml.example`, `README.md`, `.gitignore` (containing `/data/`, `/logs/`, `config.yaml`) exist at project root. AC31 MET.
- Read README's dependency license table: all entries MIT/Apache-2.0/BSD-3-Clause. AC32 MET.
- Source review of `internal/proxy/proxy.go` (`splitSSEEvents`/`errIncompleteSSEEvent`) confirms EOF-with-unterminated-event is surfaced as an error and triggers an `event: error` SSE frame while preserving already-sent chunks; a stream ending cleanly after `[DONE]\n\n` does not trigger this path. Cross-checked against `internal/proxy/proxy_test.go`, which has three dedicated tests: TCP-RST mid-stream, graceful-close-with-incomplete-buffer, and clean-close-after-complete-final-event. AC19 MET (source + test review; did not additionally reproduce this scenario live with a raw TCP script, see Minor notes).
- Source review of `internal/admin/keygen.go` (crypto/rand, sk-/key_ prefixes), `internal/auth/hash.go` (crypto/sha256), `internal/admin/middleware.go` (crypto/subtle.ConstantTimeCompare) — matches spec's security requirements (AC28-adjacent, no CLAUDE.md violations found).
- Source review of `internal/tokenlimit/tokenlimit.go` `PeriodKey` — daily `"2006-01-02"`, monthly `"2006-01"`, matches spec's period key examples.
- Cleaned up all background processes (mock servers, gateway instances) at end of session.

## Acceptance criteria

1. `go build ./...` exit 0 — **MET**
2. `go vet ./...` exit 0 — **MET**
3. `go test ./... -count=1` exit 0 + each listed package has ≥1 `_test.go` — **MET**
4. Missing `admin.token` resolution → non-zero exit + stderr message — **MET** (live)
5. Missing `--config` path → non-zero exit + stderr message — **MET** (live)
6. `POST /admin/keys` valid → 201 with `key_id`/`key` (`sk-`)/`limits` — **MET** (live)
7. `GET /admin/keys` excludes `key`/`key_hash` — **MET** (live)
8. `POST /admin/keys` without admin token → 401 `invalid_admin_token` — **MET** (live)
9. `DELETE /admin/keys/{nonexistent}` → 404 `key_not_found` — **MET** (live)
10. Valid key → `POST /v1/chat/completions` → 200, mock response relayed unchanged — **MET** (live)
11. Missing `Authorization` → 401 `invalid_api_key` — **MET** (live)
12. `rpm:1`, 2nd request → 429 + `Retry-After` — **MET** (live)
13. `max_tokens` over `max_tokens_per_request` → 400 `max_tokens_exceeded` — **MET** (live)
14. Quota exhausted → 429 `quota_exceeded` — **MET** (live)
15. Zero available backends → 503 `no_available_backend` — **MET** (live)
16. 2+ backends, round_robin_healthcheck → cyclical distribution — **MET** (live: 2/2 split over 4 requests)
17. Failed healthcheck excludes backend from routing — **MET** (live: killed mock backend, subsequent request got 503/no_available_backend)
18. `stream=true` → `Content-Type: text/event-stream`, chunk counts match — **MET** (live)
19. Mid-stream disconnect (RST or graceful close with incomplete buffered event) → partial response + error event, clean termination; complete-then-close → no error event — **MET** (source review of `splitSSEEvents`/`errIncompleteSSEEvent` + 3 dedicated unit tests covering both disconnect modes and the clean-close case; not additionally reproduced with a raw TCP-level script by the evaluator)
20. `usage_field` method → logged `prompt_tokens`/`completion_tokens` match mock `usage` — **MET** (live: logged values 12/7 matched mock's `usage.prompt_tokens=12`/`completion_tokens=7`)
21. `tokenizer` method counts only message/prompt/response content text, not JSON overhead — **MET** (live: identical `prompt_tokens` for low- and high-JSON-overhead requests with same content text)
22. JSON log has required fields, no sensitive data — **MET** (live, grep confirmed no leaks)
23. `text` format produces readable single-line log — **MET** (live)
24. `GET /healthz` → 200 `{"status":"ok"}` — **MET** (live, multiple runs)
25. `GET /readyz` → 200 with counts when healthy backend exists, 503 when none — **MET** (live)
26. Panic recovery → 500, no crash — **MET** (source: `gin.Recovery()` wired in `wiring.go`; dedicated test `TestPanicRecoveryShouldReturn500InsteadOfCrashing`)
27. `data/keys.json`/`data/usage.json` created at `0600` — **MET** (live `stat` confirmed `600`)
27a. Clean-checkout first boot (no manual `mkdir data`/`logs`) succeeds per README steps — **MET** (live reproduction via `git archive` into a fresh scratch dir, both `data/` and `logs/` auto-created with correct permissions)
28. No hardcoded admin token in source — **MET** (grep found nothing)
29. `make build OS=linux ARCH=amd64` / `OS=darwin ARCH=arm64` produce binaries — **MET** (live)
30. Dockerfile buildable / static review fallback (docker unavailable here) — **MET** (static review: multi-stage, TARGETOS/TARGETARCH→GOOS/GOARCH, non-root USER, EXPOSE/ENTRYPOINT all present)
31. `config.yaml.example`, `README.md`, `.gitignore` (data/, logs/, config.yaml) at root — **MET**
32. All dependencies MIT/Apache-2.0/BSD in README table — **MET**

## Findings
### CRITICAL
None found.

### MAJOR
None found.

### MINOR
- `internal/storage/filestore.go` `IncrementUsage`/`ResetUsage` write to disk synchronously on every call rather than the "in-memory cache + 5s periodic flush (write-behind)" scheme spec.md's data-model section (§ "인메모리 vs 파일 상태") explicitly asks for. No numbered acceptance criterion tests the flush cadence directly, and the README's "설계상 결정 사항" section discloses this trade-off honestly (stronger durability, no perf optimization). Functionally harmless for this project's scale, but it is a documented deviation from an explicit (non-AC) spec requirement, so a future evaluator/spec author should decide whether to accept the trade-off permanently or ask for the write-behind buffer.
- spec.md's architecture/milestone prose mentions a per-key "RPS" limit (`Key별 RPS/RPM/TPM`) but the `KeyRecord` data-model table only defines `limits.rpm`/`limits.tpm` (no rps field), and no acceptance criterion tests RPS. The Generator correctly implemented only RPM/TPM and documented the omission in README ("RPS는 spec의 데이터 모델에 별도 필드가 없어 RPM/TPM만 구현"). This is really a pre-existing internal inconsistency in spec.md itself (prose vs. data model table), not a Generator defect — flagging only so the next spec revision reconciles the wording.
- AC19's mid-stream-disconnect scenario was verified via source review + 3 targeted `net.Listen`-based unit tests (real TCP RST/FIN, not mocked) rather than an additional evaluator-authored live curl reproduction; confidence is high given the tests use genuine raw sockets and assert on both the preserved partial body and the `event: error` marker, but this is disclosed for transparency.

## Next iteration should
- Nothing blocking. Optional polish for a future pass: (a) consider actually
  implementing the spec's 5s write-behind buffering for `usage.json` (or
  update spec.md to drop that requirement since synchronous writes already
  satisfy every numbered AC), and (b) reconcile spec.md's mention of
  per-key RPS limits in prose vs. the `KeyRecord` data model, which only
  defines rpm/tpm.

verdict: PASS
