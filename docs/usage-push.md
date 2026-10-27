# Central usage collection

`INGEST_MODE=push` receives outbound HTTPS usage uploads from multiple CPA installations. It constructs no CPA management clients and does not require inbound access to department networks. `pull` remains the default.

Use CPA builds containing the canonical usage-plugin accounting fields and drain-before-plugin-shutdown changes. Install the matching CGO native plugin from `examples/plugin/keeper-usage-push` in the CPA checkout. The initial production targets are Debian-based Linux amd64/arm64. Do not collect the same traffic through both pull and push.

## Configure a new central Keeper

Generate an independent credential for each CPA. A model Key, CPA management key or Keeper login password must not be reused as an upload credential.

```sh
umask 077
openssl rand -hex 32 > cpa-one-push.token
python3 - <<'PY'
import hashlib
from pathlib import Path
print(hashlib.sha256(Path('cpa-one-push.token').read_text().strip().encode()).hexdigest())
PY
```

Keep the token on its CPA. Copy `deploy/ingest-sources.example.json` to a private configuration path and replace each placeholder with its token's SHA-256 hex digest. Source IDs must be unique, stable identifiers of 1–128 ASCII letters/digits/dots/underscores/hyphens, starting with a letter or digit. Credential hashes must also be unique. Cloned CPA installations need new IDs, credentials and spool directories. Source configuration is read at startup.

Copy `deploy/keeper-push.env.example` and set a private login password, an absolute source file path and a persistent data directory:

```dotenv
INGEST_MODE=push
INGEST_SOURCES_FILE=/etc/keeper/ingest-sources.json
WORK_DIR=/var/lib/keeper
APP_HOST=0.0.0.0
APP_PORT=8080
LOGIN_PASSWORD=replace-with-your-private-login-password
```

Start Keeper with `--env /path/to/keeper-push.env`. `CPA_BASE_URL` and `CPA_MANAGEMENT_KEY` are unnecessary. Configure Keeper's existing TLS settings or use an HTTPS reverse proxy. Include any `APP_BASE_PATH` in the upload endpoint. The proxy must accept 1 MiB bodies and preserve POSTs without redirects.

Use a fresh central installation for the initial rollout. Normal schema upgrades preserve old databases, but merging histories and coordinating backup restoration are outside this release.

Always upgrade Keeper before the plugin. The receiver rejects unknown payload fields, so an older Keeper answers 422 `invalid_usage_event` to events carrying fields it does not know yet, such as `node_kind`, `is_fork` and `is_compaction`, and the plugin quarantines them locally. To recover after upgrading Keeper, stop CPA, run `keeper-push-spool --config … --retry-code http_422_invalid_usage_event`, and restart CPA. A future batch-level field would make an older Keeper reject every batch with 400 `invalid_batch`, so the same receiver-first order applies to every protocol change.

## Configure each CPA

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    keeper-usage-push:
      enabled: true
      endpoint: https://keeper.example.com/api/v1/ingest/usage
      source_id: cpa-one
      token_file: /run/secrets/keeper-push-token
      spool_dir: /var/lib/cpa/keeper-usage-push
      max_spool_bytes: 1073741824
      flush_interval: 5s
      request_timeout: 15s
```

Persist the spool in a dedicated writable volume. Only one process may own it. The source and endpoint are bound to the spool; do not change them or edit pending payloads. Credential rotation preserves the source: update/restart Keeper, then update the plugin credential. Uploads retry safely during the interval.

The uploader sends up to 100 events or 1 MiB. Healthy low-volume queues flush within five seconds. Network/server/authentication/redirect failures retain data and retry with randomized exponential backoff capped at 60 seconds. The 15-second request timeout is an explicitly authorized telemetry-only exception; it does not affect model traffic.

## Statistics and access

Identical full client API Keys merge across CPA sources. Different keys remain separate, with existing masking and manual aliases. Full keys cross HTTPS and are stored centrally. Upstream credential IDs are scoped to their source, independently of client keys.

Keeper calculates and stores fees using its central pricing rules; absent prices remain unavailable. Execution counting is unchanged: multiple model executions under one inbound request remain separate events. Plugins do not calculate fees.

Administrators retain overview, real-time usage, analysis, request events, API Key time usage, pricing, aliases and existing local ranking. Push mode disables Key self-login and remote credential/quota/request-log operations. The status panel shows durable receipts, pending processing, last receipt time and retained processing failures.

## Recovery and durability

Both the plugin outbox and central push writer use SQLite WAL and `synchronous=FULL`, including replacement connections. Each callback receives one immutable delivery ID. Keeper commits its receipt and raw inbox in one transaction before ACK; the plugin verifies the complete ACK before deleting local rows. Lost responses are harmless retries.

Receipts survive inbox and event retention. Reusing one source/ID with different payload bytes returns 409. Restoring an old central backup can revert acknowledged receipts and requires a coordinated recovery procedure.

The plugin quarantines records only for this receiver's own rejections: 400 `invalid_batch`, 409 `delivery_id_conflict`, 413 `batch_too_large` and 422 `invalid_usage_event`, splitting batches to isolate individual events. Responses with these statuses but without these error codes, for example from a reverse proxy or WAF, keep data pending. Keep those codes stable when changing the receiver. Authentication and server failures keep retrying. The plugin logs through CPA's logger without payloads or keys and warns when new records are quarantined or dropped. Stop CPA before using the plugin's `cmd/spool` inspection/retry utility, which can also requeue every record with one error code; the exclusive writer lock prevents concurrent access.

Keeper retains acknowledged processing failures. After five attempts they enter persistent quarantine rather than being discarded. Fix the cause and use **Review processing errors → Retry processing** in the status panel. Reprocessing preserves the delivery ID. Administrator APIs also expose `GET /api/v1/ingest/quarantine` (first 100 rows) and `POST /api/v1/ingest/quarantine/:id/retry`, with normal admin authentication and request-intent protection.

The spool limit counts retained payload bytes, including quarantine. Allow extra disk for database pages, metadata and WAL. Disk errors or an exhausted spool preserve existing records and increment a new-record drop counter; model service continues. When storage is unwritable, drops can only be reported in memory/stderr until it recovers.

Delivery guarantees begin after plugin persistence. Abrupt CPA termination can lose records still awaiting callbacks. Normal shutdown drains local callbacks before unloading the plugin and never waits for remote delivery. Permanent storage loss is outside the guarantee.

## Contract and validation

`docs/usage-push.openapi.json` is the version 1 contract. The machine token can only upload; it grants no statistics or management access. Payloads whitelist statistical fields and exclude prompts, answers, upstream secrets, arbitrary response headers and failure bodies.

Run both repositories' backend checks, plugin tests and Keeper frontend checks. Start with two sources and reconcile tokens, costs and execution counts. Then replay 100,000 events across ten sources while querying the dashboard; record throughput, drain time and resource usage. Older capacity reports are not a guarantee for this version.

The plugin intentionally omits CPA's legacy `UsageRecord.Source`: for API Key credentials that field can contain the upstream key itself. The independent `source_id` still identifies the CPA installation; tokens, pricing and client API Key grouping do not depend on the legacy source label.
