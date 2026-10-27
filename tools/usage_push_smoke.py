#!/usr/bin/env python3
"""Exercise the real native push plugin and Keeper using an isolated temporary database."""
import argparse
import base64
import concurrent.futures
import ctypes
import hashlib
import http.client
import json
import os
from pathlib import Path
import secrets
import resource
import sys
import signal
import socket
import sqlite3
import subprocess
import tempfile
import threading
import time
import urllib.request
from datetime import datetime, timezone


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--keeper', required=True)
    parser.add_argument('--plugin', required=True)
    parser.add_argument('--events', type=int, default=100000)
    parser.add_argument('--report', required=True)
    args = parser.parse_args()
    if args.events < 10 or args.events % 10:
        parser.error('--events must be a positive multiple of ten')
    started = time.monotonic()
    with tempfile.TemporaryDirectory(prefix='keeper-push-smoke-') as directory:
        root = Path(directory)
        listener = socket.socket()
        listener.bind(('127.0.0.1', 0))
        port = listener.getsockname()[1]
        listener.close()
        base = f'http://127.0.0.1:{port}'
        tokens = {f'cpa-smoke-{i:02}': secrets.token_hex(32) for i in range(10)}
        source_file = root / 'sources.json'
        source_file.write_text(json.dumps([{'source_id': k, 'token_sha256': hashlib.sha256(v.encode()).hexdigest()} for k, v in tokens.items()]))
        env_file = root / 'keeper.env'
        env_file.write_text(f'INGEST_MODE=push\nINGEST_SOURCES_FILE={source_file}\nWORK_DIR={root / "data"}\nAPP_HOST=127.0.0.1\nAPP_PORT={port}\nAUTH_ENABLED=false\nBACKUP_ENABLED=false\nLOG_FILE_ENABLED=false\nLOG_LEVEL=error\n')
        env_file.chmod(0o600)
        log = (root / 'keeper.log').open('w')
        process = None
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        db_path = root / 'data' / 'app.db'

        def request(path, payload=None, method=None):
            data = None if payload is None else json.dumps(payload, separators=(',', ':')).encode()
            req = urllib.request.Request(base + path, data=data, method=method, headers={'Content-Type': 'application/json', 'X-CPA-Usage-Keeper-Request': 'fetch'})
            with opener.open(req, timeout=20) as response:
                return json.load(response)

        def start_keeper():
            nonlocal process
            child_env = dict(os.environ)
            for key in ('CPA_BASE_URL', 'CPA_MANAGEMENT_KEY', 'APP_BASE_PATH', 'AUTH_OIDC_ENABLED', 'TLS_ENABLED'):
                child_env.pop(key, None)
            process = subprocess.Popen([args.keeper, '--env', str(env_file)], stdout=log, stderr=log, env=child_env)
            deadline = time.monotonic() + 60
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise RuntimeError('Keeper exited during startup: ' + (root / 'keeper.log').read_text()[-2000:])
                try:
                    request('/readyz')
                    return
                except Exception as error:
                    if 'HTTP Error 503' in str(error):
                        try:
                            state = request('/api/v1/startup/status')
                            if state.get('phase') == 'failed':
                                raise RuntimeError('Keeper initialization failed: ' + (root / 'keeper.log').read_text()[-7000:])
                        except urllib.error.HTTPError:
                            pass
                    time.sleep(.05)
            raise RuntimeError('Keeper startup timed out: ' + (root / 'keeper.log').read_text()[-7000:])

        def stop_keeper():
            nonlocal process
            if process is not None and process.poll() is None:
                process.send_signal(signal.SIGTERM)
                process.wait(timeout=15)
            process = None

        def stored_count():
            with sqlite3.connect(f'file:{db_path}?mode=ro', uri=True) as db:
                return db.execute('SELECT COUNT(*) FROM usage_events').fetchone()[0]

        def wait_count(expected, timeout=120):
            deadline = time.monotonic() + timeout
            while time.monotonic() < deadline:
                if stored_count() == expected:
                    return
                time.sleep(.05)
            raise RuntimeError(f'expected {expected} stored events, got {stored_count()}')

        class Buffer(ctypes.Structure):
            _fields_ = [('ptr', ctypes.c_void_p), ('len', ctypes.c_size_t)]
        Call = ctypes.CFUNCTYPE(ctypes.c_int, ctypes.c_char_p, ctypes.POINTER(ctypes.c_uint8), ctypes.c_size_t, ctypes.POINTER(Buffer))
        Free = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_size_t)
        Shutdown = ctypes.CFUNCTYPE(None)
        class API(ctypes.Structure):
            _fields_ = [('abi_version', ctypes.c_uint32), ('call', Call), ('free_buffer', Free), ('shutdown', Shutdown)]
        library = ctypes.CDLL(args.plugin)
        library.cliproxy_plugin_init.argtypes = [ctypes.c_void_p, ctypes.POINTER(API)]
        library.cliproxy_plugin_init.restype = ctypes.c_int
        api = API()
        if library.cliproxy_plugin_init(None, ctypes.byref(api)) != 0:
            raise RuntimeError('native plugin initialization failed')

        def plugin_call(method, value):
            raw = json.dumps(value, separators=(',', ':')).encode()
            buffer = (ctypes.c_uint8 * len(raw)).from_buffer_copy(raw)
            response = Buffer()
            code = api.call(method.encode(), buffer, len(raw), ctypes.byref(response))
            try:
                result = json.loads(ctypes.string_at(response.ptr, response.len))
            finally:
                api.free_buffer(response.ptr, response.len)
            if code or not result.get('ok'):
                raise RuntimeError(f'{method} failed: {result}')

        timestamp = datetime.now(timezone.utc).isoformat().replace('+00:00', 'Z')
        breakdown = {'schema_version': 2, 'quality': 'complete', 'total_tokens': 140, 'unclassified_tokens': 0,
                     'input': {'total_tokens': 100, 'uncached_tokens': 80, 'cache_read_tokens': 20, 'cache_write_tokens': 0},
                     'output': {'total_tokens': 40, 'non_reasoning_tokens': 30, 'reasoning_tokens': 10}}
        token_stats = {'input_tokens': 100, 'output_tokens': 40, 'reasoning_tokens': 10, 'cached_tokens': 20,
                       'cache_read_tokens': 20, 'cache_read_tokens_present': True, 'cache_creation_tokens': 0, 'total_tokens': 140}

        def register(source):
            config = f'endpoint: {base}/api/v1/ingest/usage\nsource_id: {source}\ntoken: {tokens[source]}\nspool_dir: {root / source}\nflush_interval: 100ms\nrequest_timeout: 15s\nallow_http_loopback: true\n'
            plugin_call('plugin.register', {'schema_version': 6, 'config_yaml': base64.b64encode(config.encode()).decode()})

        def native_event(index):
            plugin_call('usage.handle', {'AccountingVersion': 2, 'TokenBreakdown': breakdown, 'RequestID': f'native-{index}',
                        'RequestLogID': 'same-parent-request', 'APIKey': 'sk-smoke-shared', 'Provider': 'codex', 'Model': 'smoke-model',
                        'ExecutorType': 'CodexExecutor', 'AuthType': 'api_key', 'AuthIndex': 'same-credential', 'Source': 'upstream-account',
                        'RequestedAt': timestamp, 'Generate': True, 'Stream': True, 'Latency': 1000000000, 'TTFT': 200000000,
                        'Failure': {'StatusCode': 200, 'Body': 'excluded-synthetic-failure-body'},
                        'ResponseHeaders': {'Authorization': ['excluded-synthetic-upstream-secret']},
                        'Detail': {'InputTokens': 100, 'OutputTokens': 40, 'ReasoningTokens': 10, 'CachedTokens': 20, 'CacheReadTokens': 20, 'TotalTokens': 140}})

        try:
            start_keeper()
            request('/api/v1/pricing/models', {'model': 'smoke-model', 'pricing_style': 'openai', 'base_prices': {'input': 2, 'output': 6, 'cache_read': .5, 'cache_write': 0}, 'model_multiplier': 1, 'conditional_multipliers': [], 'branches': []}, 'PUT')
            register('cpa-smoke-00')
            for i in range(3):
                native_event(i)
            wait_count(3)
            api.shutdown()
            register('cpa-smoke-01')
            stop_keeper()
            for i in range(3, 6):
                native_event(i)
            shutdown_started = time.monotonic()
            api.shutdown()
            shutdown_seconds = time.monotonic() - shutdown_started
            if shutdown_seconds > 2:
                raise RuntimeError('plugin shutdown waited for remote connectivity')
            start_keeper()
            register('cpa-smoke-01')
            wait_count(6)
            api.shutdown()
            for i in range(2):
                with sqlite3.connect(root / f'cpa-smoke-{i:02}' / 'outbox.db') as db:
                    if db.execute('SELECT COUNT(*) FROM outbox').fetchone()[0] != 0:
                        raise RuntimeError('native plugin did not consume ACK')

            dashboard_stop = threading.Event()
            dashboard_samples = []
            def dashboard():
                while not dashboard_stop.is_set():
                    before = time.monotonic()
                    try:
                        request('/api/v1/usage/overview?range=today')
                        dashboard_samples.append(time.monotonic() - before)
                    except Exception as error:
                        dashboard_samples.append(str(error))
                    dashboard_stop.wait(.25)
            reader = threading.Thread(target=dashboard)
            reader.start()
            load_started = time.monotonic()
            def upload(source_index):
                source = f'cpa-smoke-{source_index:02}'
                connection = http.client.HTTPConnection('127.0.0.1', port, timeout=60)
                per_source = args.events // 10
                duplicate_batches = 0
                try:
                    for start in range(0, per_source, 100):
                        events = []
                        for i in range(start, min(start + 100, per_source)):
                            usage = {'timestamp': timestamp, 'api_key': f'sk-smoke-{source_index % 3}', 'provider': 'codex', 'model': 'smoke-model',
                                     'executor_type': 'CodexExecutor', 'auth_type': 'api_key', 'auth_index': 'same-credential', 'source': 'upstream-account',
                                     'request_id': 'same-parent-request', 'execution_id': f'load-exec-{source_index}-{i}', 'tokens': token_stats,
                                     'generate': True, 'stream': True, 'failed': False, 'latency_ms': 1000, 'ttft_ms': 200,
                                     'fail': {'status_code': 200}, 'accounting_version': 2, 'token_breakdown': breakdown}
                            events.append({'delivery_event_id': f'load-{source_index}-{i}', 'usage': usage})
                        body = json.dumps({'schema_version': 1, 'source_id': source, 'events': events}, separators=(',', ':'))
                        copies = 2 if start % 1000 == 0 else 1
                        duplicate_batches += copies - 1
                        for _ in range(copies):
                            connection.request('POST', '/api/v1/ingest/usage', body, {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + tokens[source]})
                            response = connection.getresponse()
                            raw = response.read()
                            if response.status != 200:
                                raise RuntimeError(f'upload failed: {response.status} {raw[:200]!r}')
                            ack = json.loads(raw)
                            if ack.get('accepted_event_ids') != [event['delivery_event_id'] for event in events]:
                                raise RuntimeError('incorrect acknowledgement')
                    return duplicate_batches
                finally:
                    connection.close()
            try:
                with concurrent.futures.ThreadPoolExecutor(max_workers=10) as pool:
                    duplicates = sum(pool.map(upload, range(10)))
                accepted_seconds = time.monotonic() - load_started
                wait_count(args.events + 6, 180)
                drained_seconds = time.monotonic() - load_started
            finally:
                dashboard_stop.set()
                reader.join(timeout=25)
            with sqlite3.connect(f'file:{db_path}?mode=ro', uri=True) as db:
                count, total, cost = db.execute('SELECT COUNT(*),SUM(total_tokens),SUM(cost_usd) FROM usage_events').fetchone()
                receipts = db.execute('SELECT COUNT(*) FROM push_receipts').fetchone()[0]
                keys = db.execute('SELECT COUNT(*) FROM cpa_api_keys WHERE is_deleted=0').fetchone()[0]
                scoped = db.execute('SELECT COUNT(DISTINCT auth_index) FROM usage_events').fetchone()[0]
                failed = db.execute("SELECT COUNT(*) FROM redis_usage_inboxes WHERE source_id<>'' AND status IN ('quarantined','decode_failed','discarded')").fetchone()[0]
                leaked = db.execute("SELECT COUNT(*) FROM redis_usage_inboxes WHERE raw_message LIKE '%excluded-synthetic-%'").fetchone()[0]
            expected = args.events + 6
            assert count == receipts == expected, (count, receipts, expected)
            assert total == 140 * expected, total
            assert abs(cost - .00041 * expected) < 1e-6, cost
            assert keys == 4 and scoped == 10 and failed == leaked == 0, (keys, scoped, failed, leaked)
            # Verify the actual dashboard contract and each Key filter, beyond database totals.
            aggregate_deadline = time.monotonic() + 60
            while True:
                overview = request('/api/v1/usage/overview?range=today')
                if overview['usage']['total_requests'] == expected:
                    break
                if time.monotonic() >= aggregate_deadline:
                    raise RuntimeError('dashboard aggregates did not catch up')
                time.sleep(.1)
            assert overview['usage']['total_tokens'] == total, overview['usage']
            assert overview['summary']['cost_available'] and abs(overview['summary']['total_cost'] - cost) < 1e-6, overview['summary']
            with sqlite3.connect(f'file:{db_path}?mode=ro', uri=True) as db:
                key_totals = db.execute('SELECT k.id, COUNT(e.id), SUM(e.total_tokens), SUM(e.cost_usd) FROM cpa_api_keys k JOIN usage_events e ON e.api_group_key=k.api_key GROUP BY k.id').fetchall()
            options = request('/api/v1/usage/api-keys/options')['options']
            assert {int(option['id']) for option in options} == {row[0] for row in key_totals}
            for key_id, key_count, key_tokens, key_cost in key_totals:
                filtered = request(f'/api/v1/usage/overview?range=today&api_key_id={key_id}')
                assert filtered['usage']['total_requests'] == key_count and filtered['usage']['total_tokens'] == key_tokens, filtered['usage']
                assert filtered['summary']['cost_available'] and abs(filtered['summary']['total_cost'] - key_cost) < 1e-6, filtered['summary']
            numeric_samples = [sample for sample in dashboard_samples if isinstance(sample, float)]
            errors = [sample for sample in dashboard_samples if isinstance(sample, str)]
            if errors or not numeric_samples:
                raise RuntimeError(f'dashboard errors during ingestion: {errors}')
            stop_keeper()
            usage = resource.getrusage(resource.RUSAGE_CHILDREN)
            keeper_rss = usage.ru_maxrss if sys.platform == 'darwin' else usage.ru_maxrss * 1024
            report = {'overview_verified': True, 'api_key_views_verified': len(key_totals), 'peak_keeper_rss_bytes': keeper_rss, 'keeper_child_user_cpu_seconds': usage.ru_utime, 'keeper_child_system_cpu_seconds': usage.ru_stime, 'native_plugin_events': 6, 'replayed_events': args.events, 'sources': 10,
                      'duplicate_batches_retried': duplicates, 'stored_events': count, 'durable_receipts': receipts,
                      'tokens': total, 'cost_usd': cost, 'keys': keys, 'source_scoped_credentials': scoped,
                      'quarantined': failed, 'native_payload_leaks': leaked, 'accept_seconds': accepted_seconds,
                      'drain_seconds': drained_seconds, 'dashboard_samples': len(numeric_samples),
                      'dashboard_max_seconds': max(numeric_samples), 'offline_plugin_shutdown_seconds': shutdown_seconds,
                      'elapsed_seconds': time.monotonic() - started}
            Path(args.report).write_text(json.dumps(report, indent=2) + '\n')
            print(json.dumps(report, indent=2))
        finally:
            api.shutdown()
            stop_keeper()
            log.close()


if __name__ == '__main__':
    main()
