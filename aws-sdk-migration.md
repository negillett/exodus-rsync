# AWS SDK v1 → v2 Migration

Comprehensive guide for migrating `exodus-rsync` from `github.com/aws/aws-sdk-go` (v1.55.8) to `github.com/aws/aws-sdk-go-v2`. Covers every affected file, every API surface change, surrounding software impacts, and the testing strategy.

**Status of code snippets:** All examples in this document are **illustrative pseudocode** unless explicitly marked otherwise. Field names, type signatures, and API shapes must be verified against the **pinned** `aws-sdk-go-v2` version at implementation time. The v2 SDK's retry, credential, and options APIs have changed across releases.

### Documentation index

| File | Role |
| --- | --- |
| `aws-sdk-migration-overview.md` | Short human read: challenges and approach |
| `aws-sdk-migration.md` (this file) | Technical guide: API mapping, risks, test strategy |
| `aws-sdk-migration-decisions.md` | Planning defaults, evidence status, module pins, v1 baselines |
| `aws-sdk-migration-issues.md` | Single Jira story + **three-PR** plan |

### Cross-repo scope

Only **exodus-rsync** uses the Go AWS SDK among Exodus projects. **exodus-gw** implements the S3-compatible `/upload` API (Python/boto3 upstream); treat [exodus-gw upload API docs](https://github.com/release-engineering/exodus-gw) and `exodus_gw/routers/upload.py` as the contract reference. No coordinated SDK migration is required in other `exodus-*` repositories.

### PR 1 investigation (wire + test setup)

Wire captures, v2 pins (v1 retained in `go.mod`), parity test, HTTP fake, and `isNotFound` are **PR 1** — see **`aws-sdk-migration-issues.md`** (steps 1–5 and checklist). Prior captures were **lost**; re-gather in PR 1.

Defaults: **`aws-sdk-migration-decisions.md`** §Planning defaults. **Follow-up:** `feature/s3/transfermanager` after v2 is stable.

---

## Current State

### Scope of v1 usage

All AWS SDK usage is confined to a single package: `internal/gw/`. Production code touches the SDK in one file (`client.go`); tests touch it in three files.

| File | SDK usage |
| --- | --- |
| `internal/gw/client.go` | Session, S3 client, HeadObject, s3manager Upload, credentials, logging bridge |
| `internal/gw/gw.go` | `session.Options` type in `ext.awsSessionProvider` |
| `internal/gw/s3_helpers_test.go` | `awserr`, `request.Request`, `s3.HeadObjectInput`, `s3.PutObjectInput` handler manipulation |
| `internal/gw/client_create_test.go` | `session.Options` / `session.Session` for provider override |
| `internal/gw/client_upload_errors_test.go` | `awserr.New` for simulated errors |

### SDK operations used (production only)

| Operation | SDK v1 call | Input fields | Response fields used |
| --- | --- | --- | --- |
| Check blob exists | `s3.S3.HeadObject` | `Bucket`, `Key` | None (only error) |
| Upload blob | `s3manager.Uploader.UploadWithContext` | `Bucket`, `Key`, `Body` | `Location` (debug log) |

### Configuration passed to SDK

| Setting | v1 value | Purpose |
| --- | --- | --- |
| `Endpoint` | `cfg.GwURL() + "/upload"` | exodus-gw's S3-compatible upload API |
| `S3ForcePathStyle` | `true` | Path-style URLs: `{endpoint}/{bucket}/{key}` |
| `Region` | `"us-east-1"` | Placeholder (gateway doesn't use SigV4 region) |
| `Credentials` | `credentials.AnonymousCredentials` | No signing; auth is mTLS |
| `HTTPClient` | Custom `*http.Client` with mTLS transport (no rehttp) | Client cert auth to gateway |
| `Logger` | `log.FromContext(ctx)` (apex/log logger bridge) | SDK debug output |
| `LogLevel` | `aws.LogOff` or `aws.LogDebug` | Controlled by verbosity |
| `MaxRetries` | `cfg.GwMaxAttempts()` | SDK-level retry ceiling |
| `SharedConfigState` | `session.SharedConfigDisable` | No `~/.aws` interference |

---

## Target State (v2)

### Module structure

v2 is modular. Only import what you use:

```
github.com/aws/aws-sdk-go-v2                    (core: aws.Config, types)
github.com/aws/aws-sdk-go-v2/service/s3          (S3 client, HeadObject, PutObject)
github.com/aws/aws-sdk-go-v2/feature/s3/manager  (upload manager, optional)
github.com/aws/aws-sdk-go-v2/credentials         (anonymous credentials)
github.com/aws/smithy-go                         (error types, logging interface)
```

**Not needed** (because we don't use standard config resolution):
- `github.com/aws/aws-sdk-go-v2/config` (only needed for `LoadDefaultConfig`)

### Equivalent construction (pseudocode — verify against pinned version)

```go
import (
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/credentials"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "github.com/aws/aws-sdk-go-v2/feature/s3/manager"
)

func newS3Client(cfg conf.Config, httpClient *http.Client) *s3.Client {
    return s3.NewFromConfig(aws.Config{
        Region:           "us-east-1",
        Credentials:      /* see §Credential Parity below */,
        HTTPClient:       httpClient,
        RetryMaxAttempts: cfg.GwMaxAttempts(),
    }, func(o *s3.Options) {
        o.BaseEndpoint = aws.String(cfg.GwURL() + "/upload")
        o.UsePathStyle = true
    })
}
```

---

## Critical: Credential and Signing Parity

### The problem

v1 `credentials.AnonymousCredentials` is a well-defined concept: it instructs the SDK to **skip SigV4 signing entirely** — no `Authorization` header is emitted. The request goes out "bare" and the only auth is mTLS at the transport layer.

v2 does **not** have a direct `AnonymousCredentials` constant in the same sense. The closest equivalents are:

1. `credentials.NewStaticCredentialsProvider("", "", "")` — provides empty credentials. Whether this results in an empty `Authorization` header, a malformed one, or no header depends on the **signing middleware behavior** for the pinned version.
2. `aws.AnonymousCredentials{}` — a v2 sentinel type (introduced in later releases). Check if available in your pinned version.

### Required verification (Phase 1 — **PR 1**; re-run on pin bumps)

**Do not assume equivalence** across SDK versions. **PR 1** must deliver:

1. **Pinned** v2 modules in `go.mod` (recorded in **`aws-sdk-migration-decisions.md`**).
2. **`internal/gw/aws_v2_credential_parity_test.go`** — `HeadObject` to `httptest` server; asserts **no** `Authorization`, **no** `X-Amz-Date`, **no** `X-Amz-Security-Token`.
3. **QA manual traffic** (wire README + `qa-*.headers`) — confirm PUT / MPU requests show **no** SigV4 auth headers.

**Planned default (PR 2, if PR 1 confirms):** **`aws.AnonymousCredentials{}`**. Re-run parity tests whenever `aws-sdk-go-v2` pins change.

If any signing artifacts appear after an upgrade, the credential approach is wrong for exodus-gw (mTLS-only).

---

## Critical: 404 / Error Typing for S3-Compatible Gateways

### The problem

This is the **highest-risk area** of the migration. The current v1 code checks:
```go
awsErr, isAwsErr := err.(awserr.Error)
if isAwsErr && awsErr.Code() == "NotFound" { ... }
```

In v2, the error unwrapping path is different and **more fragile against non-AWS S3 implementations**. exodus-gw is not AWS S3 — it returns S3-shaped responses but may diverge in error body format, XML structure, or HTTP headers.

**Expected shape (confirm on re-capture):** for a missing object, **`HeadObject`** may return **HTTP 404** with **`Content-Type: application/xml`** and a non-zero **`Content-Length`** while the **HEAD response body is empty** on the wire. `isNotFound` must therefore **not** depend on parsing an XML error document from the HEAD response alone; treat **HTTP 404** (via `smithyhttp.ResponseError` and related wrappers) as authoritative once confirmed. Details: **`aws-sdk-migration-issues.md`** PR 1 step 5 and **`test/fixtures/wire-captures/README.md`** after capture.

| Gateway behavior | v2 SDK reaction | Consequence |
| --- | --- | --- |
| Standard S3 XML error body with `<Code>NoSuchKey</Code>` | Parses into `*s3types.NoSuchKey` | Works if we check this type |
| Bare 404 with empty body | May produce `*smithy.OperationError` wrapping `*http.ResponseError` | Neither `s3types.NotFound` nor `GenericAPIError` match; **falls through to hard error** |
| 404 with non-XML body (JSON, plain text) | XML parse fails; error is `*smithy.DeserializationError` or `*http.ResponseError` | Same: falls through |
| 404 with `<Code>NotFound</Code>` (not `NoSuchKey`) | May not match `*s3types.NoSuchKey`; might match `GenericAPIError` | Depends on deserialization |
| 403 masquerading as 404 (gateway ACL) | Completely different error type | Incorrect "not found" treatment if checking loosely |

### Required implementation

The error check must be **exhaustive**, not a two-branch if-else:

```go
// Pseudocode — exact types depend on pinned version
func isNotFound(err error) bool {
    // 1. Typed S3 not-found (standard AWS)
    var noSuchKey *s3types.NoSuchKey
    if errors.As(err, &noSuchKey) {
        return true
    }

    // 2. Generic API error with known "not found" codes
    var apiErr *smithy.GenericAPIError
    if errors.As(err, &apiErr) {
        switch apiErr.Code {
        case "NotFound", "NoSuchKey", "404":
            return true
        }
    }

    // 3. HTTP-level response error (empty body or unparseable)
    // Note: the exact type lives in smithy-go/transport/http or similar;
    // confirm package path at compile time against the pinned version.
    var respErr *http.ResponseError  // e.g. smithytransport.ResponseError
    if errors.As(err, &respErr) && respErr.HTTPStatusCode() == 404 {
        return true
    }

    // 4. Deserialization error wrapping a 404 response.
    // Only treat as not-found when the underlying HTTP status is 404;
    // a deserialization error on a 200 body is a real error, not a miss.
    var deserErr *smithy.DeserializationError
    if errors.As(err, &deserErr) {
        var innerResp *http.ResponseError
        if errors.As(deserErr.Err, &innerResp) && innerResp.HTTPStatusCode() == 404 {
            return true
        }
    }

    // 5. Operation error wrapping any of the above
    var opErr *smithy.OperationError
    if errors.As(err, &opErr) {
        return isNotFound(opErr.Err)
    }

    return false
}
```

### Required verification (Phase 1 — **PR 1**)

1. Issue `HEAD /{gwenv}/{nonexistent-key}` against a **real** exodus-gw (e.g. **QA `pre`**) and archive the raw response in `test/fixtures/wire-captures/` (see **`aws-sdk-migration-issues.md`** PR 1 step 2).
2. Capture raw HTTP response: status code, headers, body (even if empty) — **reference fixtures** in `test/fixtures/wire-captures/`.
3. Feed that **shape** into **`isNotFound`** tests (**EXRSYNC-2**); add branches if another environment differs.
4. **EXRSYNC-6:** repeat with the **v2 SDK** client (curl behavior may differ from SDK error wrapping).

### Mandatory test cases for `isNotFound`

**Unit matrix** (constructed smithy types) — table in `aws-sdk-migration-issues.md` (EXRSYNC-2).

**Integration test (required in EXRSYNC-2):** build a **real** v2 `s3.Client` (pinned modules) pointed at an `httptest` server that returns the **QA `pre` HEAD-miss** shape: HTTP **404**, `Content-Type: application/xml`, `Content-Length: 226`, **no response body**. Assert `isNotFound(err) == true`. This catches SDK-version drift better than hand-built `ResponseError` values alone.

- Standard XML body: `<Error><Code>NoSuchKey</Code>...</Error>` → true
- Standard XML body: `<Error><Code>NotFound</Code>...</Error>` → true
- Bare 404, empty body → true
- 404 with JSON body `{"code": "NotFound"}` → true (if gateway does this)
- 404 with plain text body → true
- 403 Forbidden → false (not a missing key)
- 500 Internal Server Error → false
- Network timeout (no HTTP response) → false
- Deserialization error wrapping a 404 response → true (**only** when the underlying HTTP status is 404; a deserialization error wrapping a 200 with a malformed body must **not** be classified as not-found)

---

## Migration Map: Line-by-Line Changes

### `internal/gw/client.go`

#### Imports

| Remove | Add |
| --- | --- |
| `github.com/aws/aws-sdk-go/aws` | `github.com/aws/aws-sdk-go-v2/aws` |
| `github.com/aws/aws-sdk-go/aws/awserr` | `github.com/aws/smithy-go` (+ HTTP error types) |
| `github.com/aws/aws-sdk-go/aws/credentials` | `github.com/aws/aws-sdk-go-v2/credentials` |
| `github.com/aws/aws-sdk-go/aws/session` | (removed; v2 has no session concept) |
| `github.com/aws/aws-sdk-go/service/s3` | `github.com/aws/aws-sdk-go-v2/service/s3` |
| `github.com/aws/aws-sdk-go/service/s3/s3manager` | `github.com/aws/aws-sdk-go-v2/feature/s3/manager` |

#### Struct changes (pseudocode)

```go
// Before
type client struct {
    cfg        conf.Config
    httpClient *http.Client
    s3         *s3.S3
    uploader   *s3manager.Uploader
    dryRun     bool
}

// After
type client struct {
    cfg        conf.Config
    httpClient *http.Client
    s3         *s3.Client
    uploader   *manager.Uploader
    dryRun     bool
}
```

**Import alias:** Use `manager` consistently (not `s3manager`) to avoid confusion with the v1 name. All examples in this doc use `manager`.

#### `NewClient` (lines 418–467) — pseudocode

**Session elimination:** v2 has no `session.Session`. Configuration is passed directly to the service client.

```go
func (impl) NewClient(ctx context.Context, cfg conf.Config) (Client, error) {
    cert, err := tls.LoadX509KeyPair(cfg.GwCert(), cfg.GwKey())
    if err != nil {
        return nil, fmt.Errorf("can't load cert/key: %w", err)
    }

    out := &client{cfg: cfg}

    transport := &http.Transport{
        TLSClientConfig: &tls.Config{
            Certificates: []tls.Certificate{cert},
        },
    }

    s3HttpClient := &http.Client{Transport: transport}
    out.httpClient = &http.Client{Transport: retryTransport(ctx, cfg, transport)}

    // Credential approach — must be verified per §Credential Parity above
    awsCfg := aws.Config{
        Region:           "us-east-1",
        Credentials:      /* verified anonymous provider */,
        HTTPClient:       s3HttpClient,
        RetryMaxAttempts: cfg.GwMaxAttempts(),
    }

    // Logging: see §Observability Continuity below
    if cfg.Verbosity() > 2 || cfg.LogLevel() == "trace" {
        awsCfg.ClientLogMode = aws.LogRequest | aws.LogResponse
        awsCfg.Logger = /* smithy logging adapter */
    }

    out.s3 = s3.NewFromConfig(awsCfg, func(o *s3.Options) {
        o.BaseEndpoint = aws.String(cfg.GwURL() + "/upload")
        o.UsePathStyle = true
    })

    out.uploader = manager.NewUploader(out.s3)

    return out, nil
}
```

#### `haveBlob` (lines 109–138) — pseudocode

Uses the `isNotFound` helper from the error handling section above:

```go
func (c *client) haveBlob(ctx context.Context, item walk.SyncItem) (bool, error) {
    logger := log.FromContext(ctx)

    fullURL := c.cfg.GwURL() + "/upload/" + c.cfg.GwEnv() + "/" + item.Key
    logConnectionOpen(ctx, fullURL)
    defer logConnectionClose(ctx, fullURL)

    _, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
        Bucket: aws.String(c.cfg.GwEnv()),
        Key:    aws.String(item.Key),
    })

    if err == nil {
        logger.F("key", item.Key).Info("Skipping upload, blob is present")
        return true, nil
    }

    if isNotFound(err) {
        logger.F("key", item.Key).Debug("blob is not present")
        return false, nil
    }

    logger.F("error", err, "key", item.Key).Warn("S3 HEAD unexpected error")
    return false, err
}
```

#### `uploadBlob` (lines 140–174) — pseudocode

```go
func (c *client) uploadBlob(ctx context.Context, item walk.SyncItem) error {
    logger := log.FromContext(ctx)
    var err error
    defer logger.F("src", item.SrcPath, "key", item.Key).Trace("Uploading").Stop(&err)

    if c.dryRun {
        return nil
    }

    file, err := os.Open(item.SrcPath)
    if err != nil {
        return err
    }
    defer file.Close()

    fullURL := c.cfg.GwURL() + "/upload/" + c.cfg.GwEnv() + "/" + item.Key
    logConnectionOpen(ctx, fullURL)
    defer logConnectionClose(ctx, fullURL)

    res, err := c.uploader.Upload(ctx, &s3.PutObjectInput{
        Bucket: aws.String(c.cfg.GwEnv()),
        Key:    aws.String(item.Key),
        Body:   file,
    })
    if err != nil {
        return fmt.Errorf("upload %s: %w", item.SrcPath, err)
    }

    logger.F("location", res.Location).Debug("uploaded blob")
    return nil
}
```

**Key differences:**
- v2 `manager.Upload` takes `*s3.PutObjectInput` directly (not a separate `UploadInput` type).
- Context is the first argument (not `UploadWithContext`).
- `Key` is `*string` via `aws.String()`.

### Observability Continuity

Replacing v1's `Logger` + `LogLevel` with v2's `ClientLogMode` is **not a clean swap**. The risks:

1. v1 routed SDK debug logs through the structured application logger (apex/log) with correlation context.
2. v2's `ClientLogMode` writes to a `logging.Logger` interface that is **not** the application logger by default.
3. Dropping the bridge means SDK retry/error logs **stop appearing** in structured application output. This is an **observability regression** in production.

**Planning default (see `aws-sdk-migration-decisions.md`):** **Option A** — wire a smithy `logging.Logger` adapter through the application logger. Options B and C are **out of scope** unless product requests a change.

| Option | Tradeoff |
| --- | --- |
| A: Wire a smithy `logging.Logger` adapter that routes through the application logger | Parity with v1; slight implementation effort |
| B: Accept that SDK-internal logs go to stderr/nowhere when debug is off | Simpler; lose SDK retry visibility unless `-vvv` |
| C: Always enable `LogRequest`/`LogResponse` and filter in the application logger | Noisy; may log sensitive headers |

**Recommendation:** Option A. Implement a small adapter (**EXRSYNC-5** implements; **EXRSYNC-3** sets `ClientLogMode` / logger on `aws.Config` when verbose or trace — see **`aws-sdk-migration-decisions.md` §Planning defaults**, item 5).

```go
// Pseudocode — verify logging.Logger interface for pinned version
type sdkLogAdapter struct {
    logger /* application logger type */
}

func (a *sdkLogAdapter) Logf(classification logging.Classification, format string, v ...interface{}) {
    // Route through application logger at debug level
    a.logger.F("sdk", "aws", "classification", classification).Debugf(format, v...)
}
```

### `internal/gw/gw.go`

#### Remove session provider

```go
// Before
var ext = struct {
    awsSessionProvider func(session.Options) (*session.Session, error)
}{
    session.NewSessionWithOptions,
}

// After: ext struct no longer needs awsSessionProvider.
// If test hook is still needed, expose the S3 client constructor instead:
var ext = struct {
    newS3Client func(cfg aws.Config, optFns ...func(*s3.Options)) *s3.Client
}{
    s3.NewFromConfig,
}
```

### `internal/gw/dryrun.go`

No SDK imports. No changes needed.

### Test Rewrite Strategy

The v1 test fake (`s3_helpers_test.go`) manipulates SDK internals (`client.s3.Client.Handlers`) which **do not exist in v2**. The v2 SDK uses middleware, not handler lists. **Complete rewrite required.**

#### Option A: HTTP test server (primary)

```go
// Pseudocode
func newFakeS3Server(t *testing.T, blobs blobMap) *httptest.Server {
    return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
        if len(parts) < 2 {
            w.WriteHeader(http.StatusBadRequest)
            return
        }
        key := parts[1]

        switch r.Method {
        case http.MethodHead:
            if _, ok := blobs[key]; !ok {
                w.WriteHeader(http.StatusNotFound)
                return
            }
            w.WriteHeader(http.StatusOK)
        case http.MethodPut:
            blobs[key] = nil
            w.WriteHeader(http.StatusOK)
        default:
            w.WriteHeader(http.StatusMethodNotAllowed)
        }
    }))
}
```

**TLS realism (addressing critique §7):**

`httptest.NewTLSServer` uses a self-signed cert with no client cert requirement. This is **simpler than production** where mTLS is enforced. The test assurance boundary:

- **Covered by HTTP fake:** URL composition, path-style routing, request/response serialization, error code handling, SDK retry behavior, upload body delivery.
- **NOT covered by HTTP fake:** mTLS handshake, CA trust chain, SNI, certificate expiry, client cert selection.

To cover the TLS layer, add **one dedicated mTLS integration test** that:
1. Creates a test CA, server cert, and client cert.
2. Configures `httptest.NewUnstartedServer` with `TLS.ClientAuth = tls.RequireAndVerifyClientCert`.
3. Verifies the S3 client presents the expected client certificate.

This separates concerns: most tests exercise HTTP semantics quickly; one test verifies TLS plumbing thoroughly.

#### Option B: Interface-based mock (for fine-grained error injection)

```go
type s3HeadAPI interface {
    HeadObject(ctx context.Context, input *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

type s3UploadAPI interface {
    Upload(ctx context.Context, input *s3.PutObjectInput, optFns ...func(*manager.Uploader)) (*manager.UploadOutput, error)
}
```

Use for unit tests that need to inject specific smithy error types without an HTTP server.

#### Recommendation

- **HTTP fake server** (Option A) for all integration-like tests (upload flows, dedup, concurrency).
- **Interface mock** (Option B) for targeted error-path unit tests (specific smithy error types).
- **One mTLS test** for TLS assurance.

### `internal/gw/client_create_test.go`

**Behavioral difference:** v2's `s3.NewFromConfig` does not establish a connection or validate credentials at construction time. It only fails when an operation is called.

If the current test verifies that `NewClient` returns an error when the session can't be created:
- TLS cert loading failure still surfaces at `NewClient` time (before SDK construction) — this case is unchanged.
- SDK-level failures (invalid endpoint, auth) only surface at first API call — test strategy must shift to verifying the first operation fails, not construction.

### `internal/gw/client_upload_errors_test.go`

Replace `awserr.New(...)` with HTTP-level responses from the fake server or smithy error types from interface mocks. Use the test server to return specific HTTP status codes and bodies that exercise all branches of `isNotFound`.

---

## URL Composition Verification

### The problem

`BaseEndpoint = aws.String(cfg.GwURL() + "/upload")` with `UsePathStyle = true` should produce request URLs of the form:

```
{gwurl}/upload/{bucket}/{key}
```

But v2 may normalize, deduplicate slashes, or append path segments differently than v1. The gateway may be sensitive to:
- Trailing slashes on the endpoint
- Double slashes from concatenation
- URL encoding of key characters
- Query parameters added by the SDK (e.g., `x-id=HeadObject`)

### Required verification (Phase 3, blocking)

Compare the **exact** `Request-URI` (from wire capture) for both v1 and v2:

| Operation | Expected URL | Check |
| --- | --- | --- |
| HeadObject | `HEAD /upload/{gwenv}/{key} HTTP/1.1` | No extra path segments, no query params that gateway rejects |
| PutObject (single) | `PUT /upload/{gwenv}/{key} HTTP/1.1` | Same |
| CreateMultipartUpload | `POST /upload/{gwenv}/{key}?uploads HTTP/1.1` | Only if multipart is used |

v2 may append SDK-internal query parameters (e.g., `x-id=HeadObject` for operation dispatch). Most S3-compatible gateways ignore unknown query params rather than rejecting them, so this is unlikely to break — but Phase 3 wire comparison catches it if it does.

**Method:** Use `GODEBUG=httptrace=1` or an HTTP proxy (`mitmproxy`, `nghttp2`) to capture exact request lines. Compare v1 and v2 output character-by-character.

If URLs differ, either adjust `BaseEndpoint` construction or use SDK middleware to rewrite the request path.

---

## Dual HTTP clients (do not merge)

`NewClient` constructs two clients on the same mTLS `http.Transport`:

| Client | Used for | Retries |
| --- | --- | --- |
| `s3HttpClient` | AWS SDK S3 path (`HeadObject`, upload manager) | SDK `RetryMaxAttempts` only |
| `out.httpClient` | JSON publish API (`doJSONRequest`) | `rehttp` wrapper (`retryTransport`) |

EXRSYNC-3 must preserve this split. Do not wrap the S3 client's transport with `rehttp`, and do not rely on `rehttp` for S3 retries.

---

## Retry Behavior

### Differences

| Aspect | v1 | v2 |
| --- | --- | --- |
| Default retry count | 3 (overridden by `MaxRetries`) | 3 (overridden by `RetryMaxAttempts`) |
| Retry strategy | Exponential backoff with jitter | Exponential backoff with jitter (standard mode) |
| Retryable errors | SDK-defined list (throttle, timeout, connection reset) | Smithy-defined retryable trait |
| Timeout handling | `http.Client.Timeout` or context | Context-first; no separate timeout field |
| Rate limiting | Not applied | Optional adaptive mode |

### Custom retryer (pseudocode — verify types against pinned version)

```go
// This is PSEUDOCODE. The retry package API has changed across v2 releases.
// Verify exact types and field names before committing.

import "github.com/aws/aws-sdk-go-v2/aws/retry"

awsCfg.Retryer = func() aws.Retryer {
    return retry.NewStandard(func(o *retry.StandardOptions) {
        o.MaxAttempts = cfg.GwMaxAttempts()
        // Add retryable status codes that exodus-gw may return
        o.Retryables = append(o.Retryables, /* HTTP status code retryer for 500/502/503/504 */)
    })
}
```

**Action:** After pinning the v2 version, write a compiling test that verifies 500/502/503/504 responses trigger retries with the configured max attempts.

---

## Gateway mandatory headers (Content-MD5)

exodus-gw requires **`Content-MD5`** and **`Content-Length`** on uploads (single `PUT` and MPU parts); chunked encoding is not supported. See `exodus_gw/routers/upload.py` and QA captures in `test/fixtures/wire-captures/`.

The v1 and v2 upload managers normally compute and send these headers. **EXRSYNC-6 wire comparison must include them** on PUT and MPU part requests (diff against `qa-put-*.headers` / `qa-mpu-part*.headers`). Missing or wrong MD5 is a **blocking regression** even when URLs and signing look correct.

---

## Request Signing

### The assertion

Both v1 (`AnonymousCredentials`) and v2 (with the correct anonymous approach) should produce **completely unsigned requests** — no `Authorization` header, no `X-Amz-Date`, no `X-Amz-Content-Sha256` beyond what the SDK adds unconditionally.

### Why this matters

exodus-gw authenticates via **mTLS only**. If the SDK adds a malformed `Authorization` header (which it might for empty static credentials vs truly anonymous credentials), the gateway could:
- Reject the request (401/403)
- Attempt to validate a bogus signature
- Behave differently than v1 in subtle, environment-dependent ways

### Verification (Phase 1 and Phase 3, blocking)

1. **Phase 1:** Capture all request headers from v1 against real gateway. Record which `X-Amz-*` or `Authorization` headers appear (expect: none or `UNSIGNED-PAYLOAD` for content hash).
2. **Phase 3:** Capture same from v2. Diff. Any new signing-related headers are a **blocking regression**.

---

## Multipart Upload Behavior

| Aspect | v1 s3manager | v2 manager |
| --- | --- | --- |
| Default part size | 5 MiB | 5 MiB |
| Default concurrency | 5 | 5 |
| Multipart threshold | Same as part size (5 MiB) | Same as part size (5 MiB) |
| Context support | `UploadWithContext` | `Upload(ctx, ...)` (context is first-class) |
| Abort on failure | `LeavePartsOnError: false` default | Same (aborts incomplete multipart) |

**Caveat:** Confirm MPU and large single **`PUT`** on the wire in **PR 1** step 4. **Planning default:** use **`feature/s3/manager`** per **`aws-sdk-migration-decisions.md`**. **PR 3** smoke on the shipping pipeline validates the **v2 SDK** (reopen wire work if smoke fails).

If a **future** deployment **does not** support MPU:

- Adjust uploader configuration or use **`PutObject`**-only paths for that deployment.
- Record the exception in **`aws-sdk-migration-decisions.md`**.

**Library note:** `feature/s3/manager` is **deprecated** upstream in favor of **`feature/s3/transfermanager`** — a **post-migration** move is planned per **`aws-sdk-migration-decisions.md`**.

## Surrounding Software Impacts

### Definite impacts (will break without changes)

| Component | Impact | Action required |
| --- | --- | --- |
| `internal/gw/s3_helpers_test.go` | Handler manipulation API does not exist in v2 | Complete rewrite (see Test Rewrite Strategy) |
| `internal/gw/client_create_test.go` | `session.Options` / `session.Session` types removed | Rewrite test hook mechanism |
| `internal/gw/client_upload_errors_test.go` | `awserr.New(...)` removed | Use smithy errors or HTTP-level simulation |
| `internal/gw/gw.go` | `ext.awsSessionProvider` references removed types | Replace with v2 hook or remove |
| `internal/log/log.go` | `(*Logger).Log(v ...interface{})` AWS Logger bridge | Replace with smithy adapter (see §Observability) |
| `go.mod` / `go.sum` | Module replacement | Update dependency graph, pin exact versions |

### Potential impacts (require verification against real gateway)

| Component | Risk | Verification |
| --- | --- | --- |
| exodus-gw upload endpoint | v2 may send different HTTP headers, URL structure, or query params | Phase 3 wire comparison (blocking) |
| exodus-gw 404 response | Non-standard body may not parse into v2 error types | **Spike:** re-capture QA HEAD-miss shape. **EXRSYNC-2** implements exhaustive `isNotFound`; **EXRSYNC-6** validates with v2 SDK. |
| Credential/signing behavior | Empty static creds may produce signing artifacts that v1 anonymous did not | **Spike:** parity test + QA PUT/MPU wire headers. Re-verify on SDK pin changes. |
| TLS handshake | Verify no SDK-layer TLS override conflicts with custom transport | Phase 3 mTLS test |
| Multipart support | Manager may send `CreateMultipartUpload` that gateway rejects | **Spike:** confirm MPU on wire. **Default:** **`manager`**. **EXRSYNC-6:** SDK smoke only. |
| Retry timing | Different jitter algorithm; gateway may rate-limit | Log retries; confirm no gateway rejection |
| `HeadObject` context cancellation | v2 aborts in-flight HTTP on context cancel (v1 did not) | Desired behavior; verify no side effects |
| Dry-run mode | `haveBlob` still issues HEAD in dry-run; context cancellation behavior changes | Verify dry-run test pass |
| CI / Dependabot | New module paths trigger new alerts; v1 path must not return | Group `aws-sdk-go-v2/*` updates; grep for `github.com/aws/aws-sdk-go` (v1) after EXRSYNC-3 |

### No impact (unchanged)

| Component | Why |
| --- | --- |
| `internal/gw/publish.go` | Uses `doJSONRequest` (stdlib HTTP), not AWS SDK |
| `internal/gw/task.go` | Uses `doJSONRequest`, not AWS SDK |
| `internal/cmd/` | Interacts via `gw.Client` interface; SDK is an implementation detail |
| `internal/walk/` | No SDK dependency |
| `internal/conf/` | No SDK dependency |
| `internal/rsync/` | No SDK dependency |
| `internal/args/` | No SDK dependency |
| `rehttp` usage | Only on `out.httpClient` (JSON API path), not on S3 client |

---

## Migration Execution Plan

**One Jira story**, **three PRs** — see `aws-sdk-migration-issues.md` for acceptance criteria, per-PR scope, and PR 1 investigation steps.

| PR | Goal |
| --- | --- |
| **1** | Wire captures, v2 pins (v1 retained), parity test, HTTP fake, mTLS test, `isNotFound` + tests |
| **2** | v2 `client.go`, observability adapter, remove v1 from `go.mod`; minimal test fixes |
| **3** | Delete handler fakes, full gw test port, gateway smoke, CHANGELOG, baselines |

```
PR 1 → PR 2 → PR 3
```

**Release:** ship **PR 2 + PR 3** together when possible (single rollback unit).

Older sections below may still say “EXRSYNC-*”; use the legacy mapping table in `aws-sdk-migration-issues.md`.

---

## Rollback Strategy

### If v2 produces incorrect behavior in production

**Symptom:** Upload failures, incorrect skip logic (every HEAD returns error → re-uploads everything), or gateway rejects requests.

**Detection:** Monitor publish success rate and upload-skipped-vs-uploaded ratio. A sudden drop in "blob already present" rate indicates `isNotFound` is broken.

**Rollback plan:**

1. **Immediate:** Revert to the last release built with v1. Binary swap on affected workers; no config change needed (same external contract).
2. **Git:** Revert **PR 2** (restores v1 client). **PR 1** can remain merged (additive). **PR 3** must also revert if test fakes depend on v2 client. Logger bridge deletion reverts with PR 2.
3. **Prevention:** Ship **PR 2 + PR 3** in a **single release** (not bundled with unrelated features) so a release-level rollback is clean.

### Feature-flag alternative (if warranted)

If the team wants a softer rollout, build both v1 and v2 client implementations behind a config flag:

```yaml
# exodus-rsync.conf
experimental:
  s3client: "v2"  # or "v1" (default)
```

This adds complexity and should only be used if gateway behavior is **still** uncertain after **PR 1** evidence and **PR 3** smoke. **Cost of the dual-client path:** both SDK stacks remain in `go.mod` simultaneously, inflating binary size, doubling the dependency audit surface, and creating merge-conflict friction on every `go.sum` update until v1 is removed. If implemented, set a hard removal date for the v1 path. Record the decision in `aws-sdk-migration-decisions.md`.

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation | Addressed in |
| --- | --- | --- | --- | --- |
| 404 error typing mismatch against exodus-gw | **High** | HEAD always errors → duplicate uploads, perf degradation | Exhaustive `isNotFound` with fallback to HTTP status code | PR 1, PR 2 |
| Signing/credential behavior drift produces auth headers | **Medium** | Gateway rejects all requests | PR 1 parity test + QA headers; header diff in **PR 3** | PR 1, PR 3 |
| URL composition differs from v1 (slashes, query params) | **Medium** | Gateway returns 404/400 for every request | Wire comparison (blocking) | PR 3 |
| Test harness simpler than production (TLS, error shapes) | **Medium** | Tests pass, prod fails | Dedicated mTLS test + real-gateway run | PR 1, PR 3 |
| Multipart not supported by gateway | **Low** | Files >5 MiB fail to upload | PR 1 confirms MPU; **PR 3** SDK smoke; **PutObject** / config fallback if needed | PR 1, PR 2 |
| Retry behavior drift under load | **Low** | Different backoff timing | Default retryer + `RetryMaxAttempts`; custom retryer only if **PR 3** proves need | PR 2, PR 3 |
| Logger adapter missing → observability regression | **Low** | SDK errors not visible in logs | **Option A** (planning default); implement in **PR 2** | PR 2 |
| v2 API types change in future patch release | **Low** | Compilation failure on update | Pin exact versions in PR 1; Dependabot | PR 1 |
