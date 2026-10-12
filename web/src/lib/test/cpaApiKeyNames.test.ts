// @vitest-environment happy-dom

import { createHash } from 'node:crypto';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CPA_API_KEY_NAMES_EXPORT_SCRIPT, CPA_API_KEY_NAMES_PREFIX, CPAApiKeyNamesExportError, cpaApiKeyNameFingerprint, describeCPAApiKeyNameSources, filterCPAApiKeyNameSources, matchCPAApiKeyNames, parseCPAApiKeyNamesExport, readAccessibleCPAApiKeyNames, resolveCPAApiBase } from '../cpaApiKeyNames';
import type { CpaApiKeySettingsItem } from '../types';

const apiBase = 'http://cpa.example.test:8317/proxy';
const host = 'panel.example.test:8080';
const userAgent = 'CPA test browser';
const rawKey = 'sk-test-123456';
const fingerprint = createHash('sha256').update(JSON.stringify(['api-key-name', apiBase, rawKey])).digest('hex');
const names = { [fingerprint]: '彭波庭' };
const apiKey: CpaApiKeySettingsItem = { id: '9007199254740993', apiKey: rawKey, displayKey: 'sk-*********123456', keyAlias: '', label: 'sk-*********123456', lastSyncedAt: null };

function obfuscate(value: unknown, sourceHost = host, sourceUA = userAgent) {
  const mask = new TextEncoder().encode(`cli-proxy-api-webui::secure-storage|${sourceHost}|${sourceUA}`);
  const bytes = new TextEncoder().encode(JSON.stringify(value)).map((byte, index) => byte ^ mask[index % mask.length]);
  return `enc::v1::${btoa(String.fromCharCode(...bytes))}`;
}

function makeExport(value: string = JSON.stringify(names), sourceBase = apiBase) {
  return JSON.stringify({ format: 'cpamc-api-key-names', version: 1, host, userAgent, entries: [{ apiBase: sourceBase, value }] });
}

function makeWindow(origin: string, entries: Record<string, string> = {}) {
  const keys = Object.keys(entries);
  const storage = { length: keys.length, key: (index: number) => keys[index] ?? null, getItem: vi.fn((key: string) => entries[key] ?? null) };
  const frame = { location: { origin, host: new URL(origin).host }, navigator: { userAgent }, localStorage: storage, parent: null as unknown };
  frame.parent = frame;
  return frame as unknown as Window;
}

describe('CPA browser-local API key names', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('matches CPAMC SHA-256 even without Web Crypto', () => {
    vi.stubGlobal('crypto', {});
    expect(cpaApiKeyNameFingerprint(apiBase, rawKey)).toBe(fingerprint);
    expect(cpaApiKeyNameFingerprint(apiBase, rawKey + 'other')).not.toBe(fingerprint);
    expect(cpaApiKeyNameFingerprint(apiBase + '/other', rawKey)).not.toBe(fingerprint);
  });

  it('resolves the management page without losing a reverse proxy subpath', () => {
    expect(resolveCPAApiBase(`${apiBase}/management.html?foo=1#config`)).toBe(apiBase);
    expect(resolveCPAApiBase(`${apiBase}/v0/management/`)).toBe(apiBase);
    expect(resolveCPAApiBase('/proxy/management.html', 'http://cpa.example.test:8317')).toBe(apiBase);
    expect(resolveCPAApiBase('javascript:alert(1)')).toBe('');
    expect(resolveCPAApiBase('https://user:password@cpa.example.test')).toBe('');
  });

  it('decodes plaintext and browser-obfuscated names with Unicode', () => {
    for (const value of [JSON.stringify(names), obfuscate(names)]) {
      expect(parseCPAApiKeyNamesExport(makeExport(value))).toEqual([{ apiBase, names }]);
    }
  });

  it('uses the export source host and browser, not the Keeper host, to decode names', () => {
    vi.stubGlobal('navigator', { userAgent: 'Another browser' });
    const result = parseCPAApiKeyNamesExport(makeExport(obfuscate(names)));
    expect(matchCPAApiKeyNames([apiKey], result, `${apiBase}/management.html`)).toEqual([{ id: apiKey.id, keyAlias: '彭波庭' }]);
  });

  it('matches only the configured CPA server and exact raw key', () => {
    const otherBase = 'http://other-cpa.example.test:8317';
    const otherNames = { [cpaApiKeyNameFingerprint(otherBase, rawKey)]: 'Wrong server' };
    const result = matchCPAApiKeyNames([
      apiKey,
      { ...apiKey, id: '2', apiKey: 'sk-different-123456' },
      { ...apiKey, id: '3', keyAlias: 'Keeper custom alias' },
    ], [{ apiBase: otherBase, names: otherNames }, { apiBase, names }], apiBase);
    expect(result).toEqual([{ id: apiKey.id, keyAlias: '彭波庭' }]);
    expect(matchCPAApiKeyNames([apiKey], [{ apiBase: otherBase, names: otherNames }], apiBase)).toEqual([]);
  });

  it('never reads unrelated storage or names for other servers', () => {
    const frame = makeWindow('http://cpa.example.test:8317', {
      auth: 'management secret',
      [CPA_API_KEY_NAMES_PREFIX + apiBase]: JSON.stringify(names),
      [CPA_API_KEY_NAMES_PREFIX + 'http://other-cpa.test']: JSON.stringify(names),
    });
    expect(readAccessibleCPAApiKeyNames(apiBase, frame)).toEqual([{ apiBase, names }]);
    expect(frame.localStorage.getItem).toHaveBeenCalledTimes(1);
    expect(frame.localStorage.getItem).toHaveBeenCalledWith(CPA_API_KEY_NAMES_PREFIX + apiBase);
  });

  it('reads browser-local names from an accessible same-origin CPA parent', () => {
    const frame = makeWindow('http://cpa.example.test:8317');
    const parent = makeWindow('http://cpa.example.test:8317', {
      [CPA_API_KEY_NAMES_PREFIX + apiBase]: obfuscate(names, 'cpa.example.test:8317'),
    });
    Object.defineProperty(frame, 'parent', { value: parent });
    expect(readAccessibleCPAApiKeyNames(apiBase, frame)).toEqual([{ apiBase, names }]);
  });

  it('degrades safely for cross-origin parents, blocked storage, and corrupted names', () => {
    const frame = makeWindow('http://keeper.example.test');
    Object.defineProperty(frame, 'parent', { value: { get location() { throw new Error('cross origin'); } } });
    expect(readAccessibleCPAApiKeyNames(apiBase, frame)).toEqual([]);
    const blocked = makeWindow('http://cpa.example.test:8317');
    Object.defineProperty(blocked, 'localStorage', { get() { throw new Error('blocked storage'); } });
    expect(readAccessibleCPAApiKeyNames(apiBase, blocked)).toEqual([]);
    const corrupted = makeWindow('http://cpa.example.test:8317', { [CPA_API_KEY_NAMES_PREFIX + apiBase]: 'enc::v1::garbage' });
    expect(readAccessibleCPAApiKeyNames(apiBase, corrupted)).toEqual([]);
  });

  it('filters invalid fingerprints, empty, oversized, and control-character aliases', () => {
    const invalidNames = {
      ...names,
      'not-a-hash': 'Invalid',
      ['a'.repeat(64)]: '  ',
      ['b'.repeat(64)]: 'Bad\nname',
      ['c'.repeat(64)]: '多'.repeat(129),
      ['d'.repeat(64)]: 123,
    };
    expect(parseCPAApiKeyNamesExport(makeExport(JSON.stringify(invalidNames)))).toEqual([{ apiBase, names }]);
  });

  it.each(['{}', '[]', 'null', 'bad json', JSON.stringify({ format: 'cpamc-api-key-names', version: 2 }), ' '.repeat((1 << 20) + 1)])('rejects malformed exports', (value) => {
    expect(() => parseCPAApiKeyNamesExport(value)).toThrow(CPAApiKeyNamesExportError);
  });

  it('skips stale records written under an older browser and keeps the readable ones', () => {
    // CPAMC's obfuscation key includes the User-Agent; a record saved before a browser update decodes to garbage.
    const stale = { apiBase: 'http://old-cpa.example.test:8317', value: obfuscate(names, host, 'Older browser') };
    const valid = { apiBase, value: obfuscate(names) };
    const exported = JSON.stringify({ format: 'cpamc-api-key-names', version: 1, host, userAgent, entries: [stale, { apiBase: 'not a url', value: 'x' }, valid] });
    expect(parseCPAApiKeyNamesExport(exported)).toEqual([{ apiBase, names }]);
  });

  it('explains when no record in the export can be decoded', () => {
    const exported = JSON.stringify({ format: 'cpamc-api-key-names', version: 1, host, userAgent, entries: [{ apiBase, value: obfuscate(names, host, 'Older browser') }] });
    let error: unknown;
    try {
      parseCPAApiKeyNamesExport(exported);
    } catch (caught) {
      error = caught;
    }
    expect(error).toBeInstanceOf(CPAApiKeyNamesExportError);
    expect((error as CPAApiKeyNamesExportError).reason).toBe('undecodable');
    expect(parseCPAApiKeyNamesExport(JSON.stringify({ format: 'cpamc-api-key-names', version: 1, host, userAgent, entries: [] }))).toEqual([]);
  });

  it('treats loopback aliases as the same CPA server but keeps other hosts apart', () => {
    const loopbackBase = 'http://localhost:8317';
    const loopbackNames = { [cpaApiKeyNameFingerprint(loopbackBase, rawKey)]: 'Loopback name' };
    const sources = [{ apiBase: loopbackBase, names: loopbackNames }];
    expect(matchCPAApiKeyNames([apiKey], sources, 'http://127.0.0.1:8317/management.html')).toEqual([{ id: apiKey.id, keyAlias: 'Loopback name' }]);
    expect(matchCPAApiKeyNames([apiKey], sources, 'http://127.0.0.1:9000/management.html')).toEqual([]);
    expect(filterCPAApiKeyNameSources(sources, 'https://cpa.example.test/management.html')).toEqual([]);
    expect(describeCPAApiKeyNameSources([...sources, { apiBase, names }, { apiBase, names: {} }])).toEqual([loopbackBase, apiBase]);
  });

  it('exports only browser name records, never credentials or configuration', () => {
    const entries = { auth: 'management-key-secret', config: rawKey, [CPA_API_KEY_NAMES_PREFIX + apiBase]: obfuscate(names) };
    const copy = vi.fn();
    const script = new Function('localStorage', 'location', 'navigator', 'copy', CPA_API_KEY_NAMES_EXPORT_SCRIPT);
    script({ ...entries, getItem: (key: string) => entries[key as keyof typeof entries] ?? null }, { host }, { userAgent }, copy);
    expect(copy).toHaveBeenCalledTimes(1);
    const value = copy.mock.calls[0][0] as string;
    expect(value).not.toContain(rawKey);
    expect(value).not.toContain('management-key-secret');
    expect(parseCPAApiKeyNamesExport(value)).toEqual([{ apiBase, names }]);
  });
});
