# Central usage collection

Keeper can receive outbound HTTPS usage uploads from other CPA installations. Receiving is turned on and tokens are created in **Settings → Push receiving**; nothing is configured through environment variables or files, and no Keeper restart is needed.

- A Keeper configured with `CPA_BASE_URL` and `CPA_MANAGEMENT_KEY` keeps pulling its own CPA and can receive pushes from other CPA installations at the same time.
- A Keeper without those two settings is a push-only receiver: it constructs no CPA management clients, hides credential, AI provider and quota pages, and disables API Key self-login.

Each CPA runs an unmodified official v8 release with the CGO native plugin [keeper-usage-push](https://github.com/brooke1220/keeper-usage-push); see its README for host requirements and installation. Official hosts do not send canonical token accounting, so the plugin derives the token breakdown with the CPA SDK. `endpoint`, the request-log ID and session-tree flags arrive empty, and price rules keyed on `endpoint` do not match. Records still queued in CPA memory at a normal restart can be lost. The initial production targets are Debian-based Linux amd64/arm64. Do not collect the same traffic through both pull and push.

## Turn on receiving in Keeper

1. Open **Settings → Push receiving** and turn on **Receive pushed usage**. It is off by default.
2. Enter a name for each CPA (for example `Beijing office CPA`) and choose **Create token**. Copy and save the token from the dialog. After closing it, **Copy token** remains available in that CPA's row while you stay on this settings page. The browser keeps newly issued tokens only in page memory, never in local or session storage. Refreshing, leaving the page or reloading settings clears them; revoking or deleting a source clears its cached token too. Keeper stores only the SHA-256 hash and the last four characters. Previously issued tokens cannot be retrieved: choose **New token**, then update the CPA plugin with the replacement.
3. Copy the upload URL shown on the card. It is built from the address your browser uses; if CPA reaches Keeper through a different address (internal network, reverse proxy), use that address instead and keep any `APP_BASE_PATH`.

Each CPA needs its own token. The token decides which source an upload belongs to, so the plugin does not send a source ID. Renaming a source never changes its internal ID, so deduplication and history are unaffected.

- **New token** replaces the token immediately. Records waiting on that CPA upload after the new token is entered there.
- **Revoke** refuses uploads with that token (401) until restored. Delete is available only after revoking and removes the entry only; received usage and deduplication receipts are kept.
- While receiving is turned off, uploads get 503 `push_ingest_disabled`. The plugin keeps the records and uploads them once receiving is turned back on.

Upgrading from the earlier file-based setup: Keeper refuses to start while `INGEST_SOURCES_FILE` or `INGEST_MODE=push` is still set; a leftover `INGEST_MODE=pull` only logs a warning. Remove them, keep `CPA_BASE_URL` and `CPA_MANAGEMENT_KEY` empty for a push-only receiver, then turn on receiving and create a token for each CPA in the settings page. Tokens from the old sources file are not migrated. Upgrade each plugin and give it its new token; an old plugin that still sends its old `source_id` with a new token is rejected with 403 `source_mismatch` and keeps its records until upgraded. API Keys that the old receiver recorded keep their aliases and are marked as pushed, so they stay visible but cannot be used for API Key self-login.

Configure Keeper's existing TLS settings or use an HTTPS reverse proxy. The proxy must accept 1 MiB bodies and preserve POSTs without redirects.

Always upgrade Keeper before the plugin. The receiver rejects unknown payload fields, so an older Keeper answers 422 `invalid_usage_event` to events carrying fields it does not know yet, and the plugin quarantines them locally. To recover after upgrading Keeper, stop CPA, run `keeper-push-spool --config … --retry-code http_422_invalid_usage_event`, and restart CPA. A future batch-level field would make an older Keeper reject every batch with 400 `invalid_batch`, so the same receiver-first order applies to every protocol change.

## Configure each CPA

Only `endpoint` and `token` are required. The easiest way to set them is CPA's management center: open **Plugins**, choose the settings button on `keeper-usage-push`, paste the upload URL and token from Keeper, and save. CPA reconfigures the plugin immediately. A freshly installed plugin registers without them so the form is available, but it uploads nothing until both are saved. The same settings in `config.yaml`:

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    keeper-usage-push:
      enabled: true
      endpoint: https://keeper.example.com/api/v1/ingest/usage
      token: paste-the-token-from-keeper
```

The local queue defaults to `plugins/keeper-usage-push-spool` under the CPA working directory. The official Docker Compose file already mounts `plugins` as a persistent volume. If you changed `plugins.dir` or run CPA elsewhere, set `spool_dir` to a persistent directory; without one the plugin refuses to start rather than fall back to a temporary location. Use `token_file` instead of `token` to read the token from a mounted secret. `max_spool_bytes` (default 1 GiB), `flush_interval` (5s) and `request_timeout` (15s) are optional.

Only one process may own a spool. The spool is bound to its endpoint; change `spool_dir` together with `endpoint`. Do not copy a spool to another machine: a copy uploaded with another token is counted again under that token's source. Clear the spool directory when cloning a CPA installation.

The uploader sends up to 100 events or 1 MiB. Healthy low-volume queues flush within five seconds. Network/server/authentication/redirect failures retain data and retry with randomized exponential backoff capped at 60 seconds. The 15-second request timeout is an explicitly authorized telemetry-only exception; it does not affect model traffic.

Do not collect the same CPA through both pull and push, or its usage is counted twice.

## Statistics and access

Identical full client API Keys merge across CPA sources. Different keys remain separate, with existing masking and manual aliases. Full keys cross HTTPS and are stored centrally. Upstream credential IDs are scoped to their source, independently of client keys.

Keeper calculates and stores fees using its central pricing rules; absent prices remain unavailable. Execution counting is unchanged: multiple model executions under one inbound request remain separate events. Plugins do not calculate fees.

Administrators see overview, real-time usage, analysis, request events, API Key time usage, pricing, aliases and the existing local ranking for pushed usage. API Keys that arrive only through push are listed for administrators but cannot be used for API Key self-login: Keeper cannot learn when another CPA revokes a key. A push-only Keeper also disables remote credential, quota and request-log operations. The status panel appears whenever receiving is on or a source exists, and shows pending processing, retained processing failures, the last receipt time and each source's last upload.

## Recovery and durability

Both the plugin outbox and Keeper's SQLite writer use WAL and `synchronous=FULL` in every mode, including replacement connections, because receiving can be turned on at any time. Each callback receives one immutable delivery ID. Keeper commits its receipt and raw inbox in one transaction before ACK; the plugin verifies the complete ACK before deleting local rows. Lost responses are harmless retries.

Receipts survive inbox and event retention. Reusing one delivery ID from the same source with different payload bytes returns 409. Restoring an old central backup can revert acknowledged receipts and requires a coordinated recovery procedure.

The plugin quarantines records only for this receiver's own rejections: 400 `invalid_batch`, 409 `delivery_id_conflict`, 413 `batch_too_large` and 422 `invalid_usage_event`, splitting batches to isolate individual events. Responses with these statuses but without these error codes, for example from a reverse proxy or WAF, keep data pending. Keep those codes stable when changing the receiver. Authentication and server failures keep retrying. The plugin logs through CPA's logger without payloads or keys and warns when records are quarantined and logs rate-limited errors when new records are dropped (the first drop immediately, then at most one line per minute with the totals). Stop CPA before using `keeper-push-spool`, the inspection/retry utility shipped as a separate `keeper-push-spool_<version>_<os>_<arch>.zip` in each plugin release, which can also requeue every record with one error code; the exclusive writer lock prevents concurrent access.

Keeper retains acknowledged processing failures. After five attempts they enter persistent quarantine rather than being discarded. Fix the cause and use **Review processing errors → Retry processing** in the status panel. Reprocessing preserves the delivery ID. Administrator APIs also expose `GET /api/v1/ingest/quarantine` (first 100 rows) and `POST /api/v1/ingest/quarantine/:id/retry`, with normal admin authentication and request-intent protection.

The spool limit counts retained payload bytes, including quarantine. Allow extra disk for database pages, metadata and WAL. Disk errors or an exhausted spool preserve existing records and increment a new-record drop counter; model service continues. When storage is unwritable, drops can only be reported in memory/stderr until it recovers.

Delivery guarantees begin after plugin persistence. Abrupt CPA termination can lose records still awaiting callbacks. Normal shutdown drains local callbacks before unloading the plugin and never waits for remote delivery. Permanent storage loss is outside the guarantee.

## Contract and validation

`docs/usage-push.openapi.json` is the version 1 contract. The machine token can only upload; it grants no statistics or management access. Payloads whitelist statistical fields and exclude prompts, answers, upstream secrets, arbitrary response headers and failure bodies.

Run both repositories' backend checks, plugin tests and Keeper frontend checks. Start with two sources and reconcile tokens, costs and execution counts. Then replay 100,000 events across ten sources while querying the dashboard; record throughput, drain time and resource usage. Older capacity reports are not a guarantee for this version.

The plugin intentionally omits CPA's legacy `UsageRecord.Source`: for API Key credentials that field can contain the upstream key itself. The upload token identifies the CPA installation; tokens, pricing and client API Key grouping do not depend on the legacy source label.
