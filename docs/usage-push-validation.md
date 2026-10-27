# Usage push validation — 2026-10-26

Validation used this checkout on macOS arm64, Go 1.26.10 and Node.js 24.22.1 (matching `.nvmrc`). All events and credentials were synthetic and the server used an isolated temporary database. No department installation or real model endpoint was contacted.

## Automated checks

- CPA: `go test -p 2 ./...` and the required server build passed.
- Plugin: `go test -race ./...` passed, covering durable restart, a lost ACK, invalid ACKs, per-event quarantine, queue ownership/capacity, timeout, configuration failure recovery, credentials, redirects, untrusted TLS and payload privacy.
- Keeper: `go test -p 2 ./cmd/... ./internal/...` passed, including legacy migrations, pricing, local ranking and receive-only startup. `go test -race ./internal/ingest` passed.
- Frontend: 203 test files / 1609 tests passed; lint, typecheck and production build passed.
- OpenAPI v1 was parsed successfully by the Postman mock generator.
- The saved Postman simulation logged a real plugin POST returning 503 followed by 200 with the same delivery ID. It was local only; no workspace publication occurred.
- Linux amd64 and arm64 plugin/utility builds passed. Each container build loaded the shared library, initialized and shut down its native ABI, and executed the utility help command. This validates loading; the complete HTTP scenario below ran on the macOS native library.

Two existing CPA timing tests and a Keeper shell healthcheck initially timed out while all suites were running concurrently. They passed in isolation or in the final complete runs with package parallelism limited to two. Schema expectations and the capacity fixture writer were updated for the added provenance fields; no old migration was reordered.

## Native integration and replay

`tools/usage_push_smoke.py` starts the real Keeper binary and loads the real native plugin through the C ABI. It registers two sources, persists six native callback events, stops Keeper, shuts down the plugin while offline, restarts both and verifies recovery. It then uploads 100,000 additional events from ten concurrent sources, deliberately repeats 100 batches and queries overview during ingestion.

The final assertions check receipt and event totals, token/fee sums, four client Key groups, ten source-scoped upstream identities, absence of leaked failure bodies/headers, zero quarantine, the actual overview response and every API Key-filtered overview.

| Result | Observed |
| --- | ---: |
| Unique stored events / durable receipts | 100,006 / 100,006 |
| Duplicate batches replayed | 100 |
| Tokens | 14,000,840 |
| Stored cost at the synthetic central prices | USD 41.00246 |
| API Key views reconciled | 4 |
| Source-scoped credential identities | 10 |
| Quarantined records / payload leaks | 0 / 0 |
| Acceptance time | 25.80 seconds |
| Processing drain time from load start | 30.36 seconds |
| Concurrent successful overview samples | 102 |
| Slowest sampled overview response | 115.3 milliseconds |
| Peak Keeper child RSS | 448.1 MiB |
| Keeper child user + system CPU time | 39.25 seconds |
| Plugin shutdown while Keeper was offline | 2.96 milliseconds |

These are a short synthetic burst on a development machine, with other build/test activity. They are not a production sustained-capacity claim, a power-loss test or a department-network deployment. The replay uses loopback HTTP explicitly enabled for testing; independent real HTTPS tests validate the upload client. Normal operation requires HTTPS and a persistent spool volume.

The raw summary is in `docs/usage-push-validation.json`. To reproduce after building Keeper and the matching native library:

```sh
python3 tools/usage_push_smoke.py --keeper /absolute/path/to/keeper \
  --plugin /absolute/path/to/keeper-usage-push.dylib \
  --events 100000 --report /tmp/usage-push-report.json
```

The script verifies its own temporary receiver and cleans it up. The native library must match the test machine. Linux shared-library loading is additionally checked by the plugin Dockerfile.
