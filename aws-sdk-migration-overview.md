# AWS SDK v2 migration — overview

A short read on **why** this migration needs care and **how** we plan to ship it.

---

## What we are doing

`exodus-rsync` talks to **exodus-gw** through a small S3-compatible upload API: check whether a blob exists (`HeadObject`), then upload if needed (`PutObject` / multipart upload). Today that path uses **AWS SDK for Go v1** in `internal/gw/client.go`. We will move it to **aws-sdk-go-v2** and remove v1 from the module graph.

Everything else in the tool—publish APIs, tasks, walking the tree—uses ordinary HTTP and does **not** change.

Only **exodus-rsync** in the Exodus family uses the Go AWS SDK. **exodus-gw** owns the upload contract (documented in that repo’s upload API).

---

## Why this is not a simple dependency bump

We are not calling real AWS S3. **exodus-gw** is a partial S3 implementation: mTLS for auth, **no SigV4 signing**, mandatory `Content-MD5` and `Content-Length` on uploads, and error bodies that do not always match what the SDK expects.

The SDK v2 stack (Smithy) classifies failures differently than v1. A missing object on HEAD might surface as a typed “not found” error, a bare HTTP 404, or a parse failure on an empty body with misleading `Content-Length`. If we get that wrong, the tool treats every HEAD as a hard error and **re-uploads everything**, or mis-classifies real failures as “missing.”

Tests today **patch inside the v1 SDK** (handler hooks). That mechanism does not exist in v2—we need an HTTP-level fake server and new error-injection patterns.

---

## Main challenges


| Challenge                      | What goes wrong if we miss it                                                                                  |
| ------------------------------ | -------------------------------------------------------------------------------------------------------------- |
| **404 / “not found” handling** | Duplicate uploads or spurious upload failures on `HeadObject`                                                  |
| **Unsigned requests**          | v2 might add `Authorization` or `X-Amz-`* headers; gateway expects mTLS only                                   |
| **Wire fidelity**              | Wrong URLs, missing `Content-MD5`, or different multipart behavior vs gateway                                  |
| **Test gap**                   | CI passes but production TLS or gateway-specific HEAD shapes fail                                              |
| **Observability**              | SDK retry/debug logs disappear unless we bridge v2 logging to our logger                                       |
| **Dual HTTP clients**          | S3 must use SDK retries only; JSON publish API must keep its separate `rehttp` wrapper—we must not merge those |


---

## How we will accomplish it

**One Jira story**, **three pull requests**, each keeping CI green.

### PR 1 — Evidence and test foundation

Before touching production upload logic:

- Capture real HTTP traffic to exodus-gw (HEAD hit/miss, PUT, large file / multipart) into `test/fixtures/wire-captures/`
- Add v2 modules to `go.mod` while **keeping v1** until PR 2
- Prove outgoing requests stay **unsigned** (automated parity test)
- Build an HTTP fake S3 server, mTLS test, and `isNotFound` with broad tests—including a real v2 client against a server that mimics our gateway’s HEAD-404 shape (empty body, XML content-type, non-zero content-length)

No change to `client.go` yet; old test fakes can remain until PR 3.

### PR 2 — Production migration

- Rewrite `client.go` and remove the v1 session layer
- Use anonymous credentials, path-style endpoint, upload manager, and `isNotFound` from PR 1
- Add a Smithy log adapter; drop the v1 logger hook
- Remove v1 from `go.mod`
- Only minimal test updates required for green CI

### PR 3 — Tests, verification, release

- Remove v1 handler-based test fakes; port tests to the HTTP fake
- Smoke on a real exodus-gw environment; compare traffic to wire captures
- CHANGELOG, `govulncheck`, binary/module baseline notes

**Release:** ship PR 2 and PR 3 together when possible so a single revert restores a known-good v1 build.

---

## Decisions already directionally set

These are planning defaults; PR 1 evidence can revise them:

- **Credentials:** `aws.AnonymousCredentials{}` (must match wire proof of no SigV4 headers)
- **Uploads:** v2 `feature/s3/manager` with default part size (multipart for large files)
- **Retries:** `RetryMaxAttempts` from existing config only
- **Logging:** route v2 SDK logs through our structured logger when verbose/trace
- **Gateway environments:** QA capture is representative; full per-env matrix only if smoke fails

---

## What success looks like

- v1 SDK fully removed; only v2 in `go.mod`
- Same external behavior: skip upload when blob exists, upload when not, dry-run still HEADs without PUT
- Wire comparison shows acceptable request shape (paths, MD5, no signing headers)
- `go test -race ./internal/gw/...` clean on the new fakes
- Documented rollback: revert PR 2 (+ PR 3 if needed) to restore v1 client

---

## If something breaks in production

Watch for failed publishes or a sharp drop in “blob already present” skips—that often means `**isNotFound` is wrong**. Roll back to the last v1 binary; git-revert PR 2 (and PR 3 if test code depends on v2). PR 1-only changes are safe to leave merged.

A config flag to run v1 and v2 side by side is possible but discouraged (two SDKs in one binary); only consider it if PR 1 + PR 3 smoke still leave major uncertainty.