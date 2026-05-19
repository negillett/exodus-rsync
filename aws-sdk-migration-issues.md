# AWS SDK v1 → v2 — Jira issue and PR plan

One Jira story, **three PRs**. Quick read: `aws-sdk-migration-overview.md`; technical detail: `aws-sdk-migration.md`; evidence and defaults: `aws-sdk-migration-decisions.md`.

| Doc | Path |
| --- | --- |
| Overview | `aws-sdk-migration-overview.md` |
| Technical guide | `aws-sdk-migration.md` |
| Decisions + baselines | `aws-sdk-migration-decisions.md` |
| This file | `aws-sdk-migration-issues.md` |

---

## Jira: Migrate exodus-rsync to AWS SDK v2

**Suggested title:** Migrate exodus-rsync from AWS SDK v1 to v2  

**Outcome:** Production S3 path uses `aws-sdk-go-v2` only; v1 removed; CI green; gateway behavior verified against wire baselines; rollback path documented.

### Acceptance criteria (story)

- [ ] Wire captures in `test/fixtures/wire-captures/`; v2 modules pinned in `go.mod`; `internal/gw/aws_v2_credential_parity_test.go` passes
- [ ] HTTP S3 fake + mTLS test; `isNotFound` with full test matrix (+ v2 client integration test against QA-shaped 404 HEAD)
- [ ] `internal/gw/client.go` migrated; v1 `aws-sdk-go` removed; dual HTTP client split preserved (SDK retries vs `rehttp` on JSON API)
- [ ] Smithy logging adapter wired; v1 `Logger.Log` bridge removed
- [ ] Handler-based test fakes removed; gw tests on HTTP fake; `go test -race ./internal/gw/...` clean
- [ ] Smoke on target exodus-gw; wire diff vs captures (URLs, no SigV4, Content-MD5/Length); CHANGELOG; v1/v2 baselines in `aws-sdk-migration-decisions.md`; `govulncheck` clean

---

## PR plan (three PRs)

Each PR must leave **CI green** and the **release binary functional** (v1 client until PR 2 merges).

### PR 1 — Test and investigation setup

**Goal:** Wire evidence, v2 pins (v1 retained), test doubles, and `isNotFound`—no production migration yet. Handler-based fakes may coexist until PR 3.

**Does not include:** changes to `client.go` production paths; deleting `s3_helpers_test.go`.

#### Deliverables

| Area | Deliverables |
| --- | --- |
| Wire / evidence | `test/fixtures/wire-captures/` + README; update `aws-sdk-migration-decisions.md` evidence table |
| Pins | `aws-sdk-go-v2` (+ `service/s3`, `credentials`, `smithy-go`; `feature/s3/manager` optional until PR 2) in `go.mod` — **v1 stays** |
| Credentials | `internal/gw/aws_v2_credential_parity_test.go` (no SigV4 headers); confirm `aws.AnonymousCredentials{}` in decisions |
| HTTP fake | `fake_s3_server_test.go` — HEAD/PUT, error sequences, QA-shaped 404 HEAD mode (empty body, `Content-Type: application/xml`, `Content-Length: 226`) |
| mTLS | `mtls_test.go` — client cert presented (v1 client OK) |
| `isNotFound` | `s3_errors.go` + table tests + **integration test:** real v2 client → httptest with captured HEAD-miss shape |

#### PR 1 checklist

- [ ] Wire captures archived (QA `pre` HEAD, PUT, **and MPU** where feasible)
- [ ] Credential behavior verified; choice recorded in `aws-sdk-migration-decisions.md`
- [ ] Multipart support confirmed or denied (wire capture or exodus-gw confirmation)
- [ ] 404 / HEAD-miss format documented (raw response; HEAD body empty or not)
- [ ] v2 module versions pinned in `go.mod` (v1 **still present** until PR 2)
- [ ] HTTP fake, mTLS test, `isNotFound` + tests in same PR

#### Step 1 — Pin AWS SDK v2 (keep v1 until PR 2)

```bash
go get github.com/aws/aws-sdk-go-v2@latest
go get github.com/aws/aws-sdk-go-v2/service/s3@latest
go get github.com/aws/aws-sdk-go-v2/feature/s3/manager@latest
go get github.com/aws/aws-sdk-go-v2/credentials@latest
go get github.com/aws/smithy-go@latest
```

Record exact versions in `aws-sdk-migration-decisions.md` §Pinned AWS SDK v2 modules.

**Prior targets (re-verify):**

| Module | Prior target |
| --- | --- |
| `github.com/aws/aws-sdk-go-v2` | v1.41.7 |
| `github.com/aws/aws-sdk-go-v2/service/s3` | v1.101.0 |
| `github.com/aws/aws-sdk-go-v2/credentials` | v1.19.16 |
| `github.com/aws/smithy-go` | v1.25.1 |

#### Step 2 — Wire capture against real exodus-gw

Use `mitmproxy`, tcpdump, or `GODEBUG=httptrace=1` to capture HTTP exchanges.

| Scenario | Capture |
| --- | --- |
| HeadObject — key exists | Full request + response |
| HeadObject — key does not exist | Status, headers, body (even if empty on wire) |
| PutObject — small file (&lt;5 MiB) | Request headers (**Content-MD5**, **Content-Length**) |
| PutObject — file &gt;5 MiB | Single PUT vs multipart |
| Signing | No `Authorization`, `X-Amz-Date`, `X-Amz-Security-Token` |

Archive under `test/fixtures/wire-captures/`.

**curl:** use **`curl -sSI`** for HEAD; plain **`curl -X HEAD -o /dev/null`** may **hang** when `Content-Length` is non-zero on 404.

**Host (prior run):** `https://exodus-gw.corp.qa.redhat.com`, environment **`pre`**.

#### Step 3 — Credential / signing parity

Implement `internal/gw/aws_v2_credential_parity_test.go`:

1. `credentials.NewStaticCredentialsProvider("", "", "")` → `HeadObject` to `httptest` → capture headers.
2. `aws.AnonymousCredentials{}` → repeat.
3. Assert **no** `Authorization`, `X-Amz-Date`, `X-Amz-Security-Token`.

**Planned default:** `aws.AnonymousCredentials{}` — record in `aws-sdk-migration-decisions.md`.

#### Step 4 — Multipart support

Upload **&gt;5 MiB** with v1 `s3manager` (default settings) or confirm with exodus-gw owners. Record in `aws-sdk-migration-decisions.md`. If MPU is unsupported, note uploader constraints for PR 2.

**Prior run (unverified):** MPU OK on QA `pre`; test key `d804240d2e0fb82c0a8ca90159cd72eed387d256334fd3af8c654f2fb7fbd040` (6 MiB zeros + `mpu-wire-qa`).

#### Step 5 — 404 / HEAD-miss shape

Document from wire capture. Feed shape into `isNotFound` tests and HTTP fake QA mode.

**Prior observation (re-verify):**

| Scenario | Previously observed |
| --- | --- |
| HEAD — key missing | **404**, `Content-Type: application/xml`, `Content-Length: 226`; **HEAD body length 0** |
| PUT — small | **200**; `Content-MD5` + `Content-Length` on request |
| MPU | Full flow **200**; parts require `Content-MD5` |

**GET** on `/upload/{env}/{key}` returns **405** (not used for existence checks).

---

### PR 2 — Migrate production client to v2

**Goal:** Tool uses v2 for HeadObject and upload; v1 removed from `go.mod`.

**Includes:**

- `internal/gw/client.go` — `aws.Config`, `s3.NewFromConfig`, `manager.Upload`, `isNotFound`, `HeadObject(ctx, …)`, log URLs from config
- `internal/gw/gw.go` — remove session provider / `ext.awsSessionProvider`
- `internal/log/sdk_adapter.go` + wire in `NewClient`; remove v1 `(*Logger).Log` bridge
- `go mod tidy` — drop `github.com/aws/aws-sdk-go` v1
- **Minimal** gw test updates for green CI (handler fakes still OK until PR 3)

**Does not include:** deleting `s3_helpers_test.go`; full test port; gateway smoke (PR 3).

**Rollback note:** Reverting this PR restores v1; PR 1 remains safe to keep merged.

---

### PR 3 — Test cleanup, verification, release

**Goal:** Tests fully on HTTP fake; release evidence complete.

**Includes:**

- Delete `s3_helpers_test.go`; migrate `client_*_test.go` to HTTP fake / smithy mocks
- Extend fake for **MPU** if tests need files **>5 MiB**
- `go test -race -count=5 ./internal/gw/...`; coverage not regressed
- Real-gateway smoke + wire comparison vs `test/fixtures/wire-captures/`
- v1 binary/module baselines recorded (`aws-sdk-migration-decisions.md`); CHANGELOG; grep for v1 module path residue

**Ship with PR 2** in one release when possible (single rollback unit for client + tests).

---

## Dependency graph

```
PR 1 (wire + pins + fake + isNotFound + parity test)
    │
    ▼
PR 2 (v2 client + observability + remove v1)
    │
    ▼
PR 3 (test rewrite + smoke + CHANGELOG)
```

**Critical path:** PR 1 → PR 2 → PR 3.

---

## Legacy numbering

Older docs used **EXRSYNC-1 … EXRSYNC-6** (one ticket per PR). Mapping:

| Old | New |
| --- | --- |
| Spike + EXRSYNC-1 + EXRSYNC-2 | **PR 1** |
| EXRSYNC-3 + EXRSYNC-5 | **PR 2** |
| EXRSYNC-4 + EXRSYNC-6 | **PR 3** |

Implementation pseudocode and risk tables in `aws-sdk-migration.md` still apply; read **PR 1/2/3** where those docs mention individual EXRSYNC tickets.
