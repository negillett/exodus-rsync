# AWS SDK v2 migration — engineering decisions

Evidence and defaults for the migration. **Overview:** `aws-sdk-migration-overview.md`. **PR plan:** `aws-sdk-migration-issues.md`. **Technical guide:** `aws-sdk-migration.md`.

---

## Evidence status

| Artifact | Path | Present |
| --- | --- | --- |
| Wire captures | `test/fixtures/wire-captures/` | **No** — deliver in **PR 1** |
| Credential parity test | `internal/gw/aws_v2_credential_parity_test.go` | **No** |
| v2 pins in `go.mod` | `go.mod` | **No** (v1 only today) |

Update when **PR 1** merges. **PR 2** needs confirmed credentials; **PR 3** needs wire baselines for gateway diff.

---

## Planning defaults

Confirm or revise when **PR 1** evidence lands.

1. **Environment parity** — S3 upload semantics assumed equivalent across exodus-gw environments. Capture on QA `pre` (or team QA host). **PR 3** smoke on shipping pipeline.

2. **Upload library** — `github.com/aws/aws-sdk-go-v2/feature/s3/manager` in **PR 2**. Follow-up: `feature/s3/transfermanager` after stable.

3. **Uploader tuning** — Manager defaults; no custom tuning unless **PR 3** proves need.

4. **Retries** — `RetryMaxAttempts` from `GwMaxAttempts()` only in **PR 2**.

5. **SDK observability** — Option A (`aws-sdk-migration.md` §Observability Continuity): smithy adapter in **PR 2**.

6. **Test infrastructure** — HTTP fake + mTLS in **PR 1**; full handler-fake removal in **PR 3**.

7. **`isNotFound`** — Full matrix in `aws-sdk-migration.md`; HTTP 404 authoritative for empty-body HEAD miss (confirm on wire in **PR 1**).

**Credentials (confirm in PR 1):** `aws.AnonymousCredentials{}`.

---

## Pinned AWS SDK v2 modules

Fill when **PR 1** runs `go get`. Prior targets (re-verify):

| Module | Version |
| --- | --- |
| `github.com/aws/aws-sdk-go-v2` | _TBD_ (prior: v1.41.7) |
| `github.com/aws/aws-sdk-go-v2/service/s3` | _TBD_ (prior: v1.101.0) |
| `github.com/aws/aws-sdk-go-v2/credentials` | _TBD_ (prior: v1.19.16) |
| `github.com/aws/smithy-go` | _TBD_ (prior: v1.25.1) |

Add `feature/s3/manager` pin with **PR 2** if not pulled in PR 1.

---

## v1 pre-migration baselines (PR 3)

Record **before PR 2** merges, `linux/amd64`, `CGO_ENABLED=0`:

```bash
go build -o exodus-rsync-linux-amd64 ./cmd/exodus-rsync
wc -c < exodus-rsync-linux-amd64
go list -m all | wc -l
```

| Metric | v1 baseline | v2 post-migration |
| --- | --- | --- |
| Release binary size (bytes) | _TBD_ | _TBD_ |
| `go list -m all` count | _TBD_ | _TBD_ |

Document drift in **PR 3** CHANGELOG.
