# Iteration 01 report

## Summary
llm-gateway is a well-structured Go implementation that passes `go build`, `go
vet`, and `go test ./...` (all 13 packages, exit 0) and, in live smoke testing
against a mock vLLM backend, correctly implements admin key issuance/listing/
revocation, client auth, RPM rate limiting, max_tokens/quota enforcement,
round-robin load balancing with health-check-driven exclusion, non-streaming
and streaming (SSE) proxying, structured JSON/text request logging without
leaking secrets, ops endpoints, panic recovery wiring, cross-compiled
binaries, and license documentation. TDD discipline (20 commits alternating
`[behavioral]`/`[structural]`) is evident in git history.

However, one CRITICAL bug was found by actually following the project's own
README "빌드/실행" instructions on a clean checkout: `FileStore` never creates
the parent directory (`./data/`) for `keys.json`/`usage.json`, and no other
code path (main.go, Makefile, Dockerfile) creates it either. Because
`data/` is git-ignored (as spec requires) and never created automatically,
a first-time user who does exactly what the README says (`cp
config.yaml.example config.yaml`, `export ADMIN_TOKEN=...`, run the binary)
gets an immediate startup failure (`open ./data/keys.json: no such file or
directory`, exit 1) — the process never becomes reachable. This directly
contradicts spec.md's explicit requirement ("파일이 없으면 최초 기동 시 빈
구조로 자동 생성") and is not caught by any test because all storage tests
use `t.TempDir()` (an already-existing directory) as the parent, masking the
missing-`MkdirAll` bug. This is a spec violation that breaks the primary
documented startup flow, so verdict is FAIL despite all 32 numbered
acceptance criteria being individually satisfiable once the directory is
pre-created by hand.

## Commands run
- `go version` → `go1.25.0 darwin/arm64`
- `go build ./...` → exit 0, no output.
- `go vet ./...` → exit 0, no output.
- `go test ./... -count=1` → exit 0, all 13 packages `ok`
  (cmd/llm-gateway, internal/{admin,apierror,auth,config,healthcheck,
  loadbalancer,metrics,proxy,ratelimit,storage,tokencount,tokenlimit}).
- `find <pkg> -maxdepth 1 -name '*_test.go'` per required package → each has
  ≥1 `_test.go` (auth and metrics have 2).
- `make build OS=linux ARCH=amd64` then `make build OS=darwin ARCH=arm64`
  (after `rm -rf bin`) → both exit 0, both binaries created in `./bin/`.
- Started a Python `http.server`-based mock vLLM backend
  (`/health` → 200, `POST /v1/chat/completions` → JSON with `usage`, and an
  SSE stream mode) on 127.0.0.1:9001 (and :9002 for LB test).
- Ran `./bin/llm-gateway-darwin-arm64 --config ./config.yaml` in a scratch
  dir **without** pre-creating `./data/` (mirroring README steps exactly) →
  **exit 1**, stderr: `llm-gateway: wiring: failed to open storage: storage:
  failed to initialize "./data/keys.json": open ./data/keys.json: no such
  file or directory` — CRITICAL finding, see below.
- Re-ran after `mkdir -p data logs` → started fine, `/healthz`/`/readyz`
  200 with `healthy_backends:1,total_backends:1`.
- `curl -X POST /admin/keys` (valid token, `{"name":"team-a"}`) → 201 with
  `key_id`, `key` (`sk-...`), `limits`. Matches AC6.
- `curl -X POST /admin/keys` (no token) → 401
  `{"error":{"type":"authentication_error","code":"invalid_admin_token"}}`.
  Matches AC8.
- `curl -X DELETE /admin/keys/key_doesnotexist` (valid token) → 404
  `code":"key_not_found"`. Matches AC9.
- `curl GET /admin/keys` → array without `key`/`key_hash` fields. Matches AC7.
- `stat -f "%OLp" data/keys.json data/usage.json` → `600 600`. Matches AC27.
- `curl -X POST /v1/chat/completions` with issued key → 200, vLLM JSON
  relayed unchanged (`usage.prompt_tokens=5` etc). Matches AC10.
- `curl -X POST /v1/chat/completions` with no `Authorization` header → 401
  `code":"invalid_api_key"`. Matches AC11.
- Issued a key with `rpm:1`; 1st request 200, 2nd request → 429 with
  `Retry-After: 60` header. Matches AC12.
- Issued a key with `max_tokens_per_request:10`; request with
  `max_tokens:100` → 400 `code":"max_tokens_exceeded"`. Matches AC13.
- Issued a key with `quota_tokens:1` (daily); 1st request 200 (consumes
  8 tokens per mock `usage`), 2nd request → 429 `code":"quota_exceeded"`.
  Matches AC14.
- Killed the backend, waited > health_check_interval → `/readyz` 503
  (`healthy_backends:0`), then proxy call → 503 `code":"no_available_backend"`.
  Matches AC15, AC17, AC25 (0-backend branch).
- Started 2 backends (9001/9002), 4 sequential proxy calls → log
  `backend` field alternates 9001,9002,9001,9002 (verified round-robin).
  Matches AC16.
- `curl -N -i` with `"stream":true` → `Content-Type: text/event-stream`,
  3 SSE chunks + `[DONE]` relayed unchanged; log line shows
  `stream:true`, `itl_ms:[...]` (3 entries), `tpot_ms` computed. Matches
  AC18 and (partially, via unit test below) AC19.
- `go test ./internal/proxy/... -v` → includes
  `TestHandlerShouldSendErrorEventAfterPartialChunksWhenConnectionDropsMidStream`
  PASS — covers AC19 (mid-stream disconnect) since live simulation via the
  Python mock was impractical.
- Set `token_counting.method: tokenizer`, issued a key, made a request →
  log shows `completion_tokens:21` (vs `usage_field` mode's `3`), confirming
  the tokenizer code path is actually invoked and diverges from usage_field.
  Matches AC21 (spec only requires the logic to run, not accuracy).
- Set `logging.format: text` → single human-readable `ts=... key=value`
  line per request (not JSON). Matches AC23.
- `grep -i "bearer\|hello\|hi there"` and `grep -F "<plaintext key>"` against
  the JSON/text log files → both no matches (exit 1), confirming no
  Authorization header, prompt/response body, or plaintext key leaks into
  logs. Matches AC22 (sensitive-field omission part).
- `grep -rn 'admin.*token\s*=\s*"'` over `*.go` files → no matches (only a
  test/comment reference to `gin.Recovery()`), confirming no hardcoded admin
  token literal. Matches AC28.
- `grep -rn "gin.Recovery\|Recover("` → found in `cmd/llm-gateway/wiring.go`
  and covered by `cmd/llm-gateway/wiring_test.go`. Matches AC26.
- Ran binary with a nonexistent `--config` path → exit 1, stderr
  `config: failed to read ...: no such file or directory`. Matches AC5.
- Ran binary with `ADMIN_TOKEN` unset (`${ADMIN_TOKEN}` unresolved) → exit 1,
  stderr `config: admin.token is required but unresolved ...`. Matches AC4.
- `docker version` → `docker: command not found` in this environment; AC30
  (buildx build) could not be executed. Dockerfile reviewed statically only
  (multi-stage, non-root user, `TARGETOS`/`TARGETARCH` build args — looks
  correct, but not build-verified here; matches the Generator's own
  plan.md note that this was likewise never actually built).
- `grep -rn "Mkdir" project/**/*.go` → **zero results** anywhere in the
  codebase, confirming the missing-directory-creation bug is systemic, not a
  one-off oversight in a single function.
- `git log --oneline` → 20 commits alternating `[behavioral]`/`[structural]`,
  consistent with TDD Red-Green-Refactor discipline (informational only,
  not judged as pass/fail evidence).

## Acceptance criteria

1. `go build ./...` exit 0 — **MET**.
2. `go vet ./...` exit 0 — **MET**.
3. `go test ./... -count=1` exit 0 with `_test.go` in every required
   package — **MET**.
4. Missing `ADMIN_TOKEN` → non-zero exit + stderr message — **MET**
   (verified live).
5. Nonexistent `--config` path → non-zero exit + stderr message — **MET**
   (verified live).
6. `POST /admin/keys` valid → 201 with `key_id`/`key` (`sk-` prefix)/
   `limits` — **MET** (verified live).
7. `GET /admin/keys` excludes `key`/`key_hash` — **MET** (verified live).
8. `POST /admin/keys` without admin token → 401
   `invalid_admin_token` — **MET** (verified live).
9. `DELETE /admin/keys/{nonexistent}` → 404 `key_not_found` — **MET**
   (verified live).
10. Valid API key → `POST /v1/chat/completions` 200, vLLM response relayed
    unchanged — **MET** (verified live against mock backend).
11. Missing `Authorization` → 401 `invalid_api_key` — **MET** (verified
    live).
12. `rpm:1`, 2nd request → 429 + `Retry-After` — **MET** (verified live).
13. `max_tokens` over `limits.max_tokens_per_request` → 400
    `max_tokens_exceeded` — **MET** (verified live).
14. Quota exhausted → 429 `quota_exceeded` — **MET** (verified live).
15. Zero healthy backends → 503 `no_available_backend` — **MET** (verified
    live by killing the backend and waiting past the health-check
    interval).
16. 2+ backends, `round_robin_healthcheck` cycles requests — **MET**
    (verified live: 4 requests alternated 9001/9002/9001/9002).
17. Backend forced-unhealthy is excluded from routing — **MET** (verified
    live via the same kill-backend test as #15; also unit-tested in
    `internal/loadbalancer`).
18. `stream=true` → `text/event-stream`, chunk count matches — **MET**
    (verified live: 3 mock SSE chunks relayed, `Content-Type` correct).
19. Mid-stream disconnect → partial chunks + error event, no
    panic/hang — **MET** (unit test
    `TestHandlerShouldSendErrorEventAfterPartialChunksWhenConnectionDropsMidStream`
    passes; not independently reproduced live due to tooling limits of the
    Python mock, but the test scenario matches the AC wording precisely).
20. `usage_field` mode: logged `prompt_tokens`/`completion_tokens` match
    vLLM's `usage` — **MET** (verified live, `5`/`3` matched exactly).
21. `tokenizer` mode: gateway's own count logged (need not match
    `usage_field`) — **MET** (verified live: `21` vs `usage_field`'s `3`,
    confirming the alternate code path executes). Implementation quality
    caveat noted as MINOR below (counts raw JSON bytes of request/response
    bodies, not message text).
22. JSON log line per request with required fields; no key/hash/
    Authorization/prompt-response body leakage — **MET** (verified live;
    grep for secrets/prompt content returned no matches).
23. `logging.format: text` produces a readable single-line log — **MET**
    (verified live).
24. `GET /healthz` → 200 `{"status":"ok"}`, no auth — **MET** (verified
    live).
25. `GET /readyz` → 200 with counts when ≥1 healthy backend, 503 when
    0 — **MET** (verified live both branches).
26. Panic doesn't crash the process, returns 500 (Gin `Recovery()`) —
    **MET** (`gin.Recovery()` wired in `wiring.go`, unit-tested in
    `wiring_test.go`).
27. `data/keys.json`/`data/usage.json` created with `0600` — **MET**
    (verified live via `stat`), **but see CRITICAL finding**: this is only
    reachable if the `data/` directory already exists — the app itself
    never creates it, contradicting spec's "파일이 없으면 최초 기동 시
    빈 구조로 자동 생성" and breaking the README's own first-run
    instructions.
28. No hardcoded admin token in source — **MET** (`grep` found no literal
    token assignment).
29. `make build OS=linux ARCH=amd64` / `OS=darwin ARCH=arm64` produce
    binaries — **MET** (verified live, both files present after `rm -rf
    bin` + rebuild).
30. `Dockerfile` exists, `docker buildx build --platform
    linux/amd64,linux/arm64 .` (or single-platform `docker build .`)
    completes — **NOT VERIFIED** (docker not installed in this evaluation
    environment; Dockerfile reviewed statically and looks structurally
    correct — multi-stage, non-root user, TARGETOS/TARGETARCH args — but
    was never actually built, by the Generator or by this evaluation).
31. `config.yaml.example`, `README.md`, `.gitignore` (with `data/`,
    `logs/`, `config.yaml`) exist at repo root — **MET** (all present and
    correctly scoped).
32. External dependencies documented as MIT/Apache-2.0/BSD — **MET**
    (README has a full license table; `strings.Fields`-based tokenizer
    avoids adding a new dependency, consistent with the table).

## Findings

### CRITICAL

1. **`FileStore` never creates the parent directory for `data/keys.json` /
   `data/usage.json`, breaking the documented first-run flow and violating
   spec.md's explicit auto-creation requirement.**
   `project/internal/storage/filestore.go`, function `ensureJSONFile`
   (called from `NewFileStore`), does:
   ```go
   func ensureJSONFile(path string, emptyContent []byte) error {
       if _, err := os.Stat(path); err == nil {
           return os.Chmod(path, filePermission)
       } else if !os.IsNotExist(err) {
           return err
       }
       return os.WriteFile(path, emptyContent, filePermission)
   }
   ```
   `os.WriteFile` fails with `ENOENT` if the parent directory (`./data/`)
   does not exist; there is no `os.MkdirAll(filepath.Dir(path), ...)`
   anywhere in the call chain (`grep -rn "Mkdir"` across the entire
   repository returns zero matches — this is not an isolated slip, no code
   path creates the directory). Reproduced live by following
   `project/README.md` "실행" section verbatim on a clean scratch directory
   (`cp config.yaml.example config.yaml`, `export ADMIN_TOKEN=...`, run the
   binary) — process exits immediately with:
   `llm-gateway: wiring: failed to open storage: storage: failed to
   initialize "./data/keys.json": open ./data/keys.json: no such file or
   directory`. Since `.gitignore` (correctly, per spec) excludes `/data/`,
   every fresh clone hits this on first run; the Docker image (`WORKDIR
   /app`, no `data/` created in the Dockerfile either) has the same problem.
   This directly contradicts spec.md's data-model note: "파일이 없으면
   최초 기동 시 빈 구조(`[]`/`{}`)로 자동 생성" — the spec's intent is
   clearly that a fresh install "just works"; instead it never starts.
   No test in `internal/storage/filestore_test.go` catches this because
   every test passes `t.TempDir()` (a directory that already exists) as
   the parent of `keysPath`/`usagePath`, so the missing `MkdirAll` is never
   exercised.
   **Fix**: in `ensureJSONFile` (or in `NewFileStore` before calling it),
   add `os.MkdirAll(filepath.Dir(path), 0o700)` before `os.WriteFile`, and
   add a regression test that constructs `NewFileStore` with a path whose
   parent directory does not yet exist.

### MAJOR
(none beyond the CRITICAL item above; all numbered acceptance criteria that
could be independently verified passed)

### MINOR

1. **Tokenizer-mode "approximate" token counting counts raw JSON request/
   response bytes, not actual message text**, producing numbers detached
   from any real token estimate (e.g., counting `{"model":"qwen","messages":
   [{"role":"user","content":"hi there how are you"}]}` — including JSON
   punctuation/field names as "words" — rather than just the prompt text).
   `project/cmd/llm-gateway/wiring.go:189-190`:
   ```go
   if method == tokencount.MethodTokenizer {
       return tokencount.CountApprox(string(res.RequestBody)), tokencount.CountApprox(string(res.ResponseBody))
   }
   ```
   Spec's Assumptions explicitly allow inaccuracy ("100% 일치할 필요는
   없다... 계산 로직이 실제로 호출됐는지만 검증"), so this does not fail
   acceptance criterion 21, but it's a low-effort implementation that will
   surprise anyone reading the logs expecting even approximate token
   counts — worth tightening to at least extract `messages[].content` /
   `choices[].message.content` text before counting.

2. **`plan.md` marks Docker buildx (Step 64) and multi-platform Docker
   build (AC30) as never actually executed** ("로컬에 docker 미설치로
   buildx 실행은 검증 못함(Dockerfile 정적 검토만)"), and this evaluation
   likewise had no `docker` binary available to verify it. The Dockerfile
   reads correctly on static review, but AC30 remains formally unverified
   by either the Generator or this Evaluator.

3. A stray gateway process and a stray mock-server process from what
   appears to be the Generator's own prior manual testing were found still
   running (`/tmp/gw-smoke/config.yaml`, `/tmp/mock_vllm.py`) when this
   evaluation started interacting with the system's process table — not a
   spec violation, but worth the Generator remembering to clean up
   long-running background processes it starts for manual verification.

## Next iteration should

1. Fix the CRITICAL bug: add `os.MkdirAll(filepath.Dir(path), 0o700)` (or
   equivalent) before creating `keys.json`/`usage.json` in
   `internal/storage/filestore.go`'s `ensureJSONFile` (or in `NewFileStore`
   before calling it). Add a regression test in
   `internal/storage/filestore_test.go` that passes a path whose parent
   directory does not exist yet, asserting `NewFileStore` still succeeds
   and the directory + files are created with the correct permissions.
2. Re-verify the full README "빌드/실행" flow on a genuinely clean
   checkout (no pre-existing `data/`/`logs/` directories) end-to-end,
   exactly as a first-time user would, to confirm the fix closes the gap.
3. Optionally tighten the `tokenizer` counting mode to operate on extracted
   message text rather than raw JSON bytes (MINOR, not blocking).
4. If a `docker` daemon becomes available in a later iteration, actually
   run `docker build .` or `docker buildx build --platform ... .` to
   verify AC30 rather than relying on static review only.

verdict: FAIL
