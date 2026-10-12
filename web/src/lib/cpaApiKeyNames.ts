import { sha256 } from '@noble/hashes/sha2.js';
import { bytesToHex } from '@noble/hashes/utils.js';
import type { CpaApiKeySettingsItem } from './types';

export const CPA_API_KEY_NAMES_PREFIX = 'api-key-names:v1:';
const EXPORT_FORMAT = 'cpamc-api-key-names';
const OBFUSCATION_PREFIX = 'enc::v1::';
const OBFUSCATION_SALT = 'cli-proxy-api-webui::secure-storage';
const MAX_IMPORT_LENGTH = 1 << 20;

export interface CPAApiKeyNameSource {
  apiBase: string;
  names: Record<string, string>;
}

export interface CPAApiKeyAliasImport {
  id: string;
  keyAlias: string;
}

// invalid: not a CPAMC export at all. undecodable: a valid export whose name records
// cannot be decoded, which happens when CPAMC wrote them under an older browser
// User-Agent (its obfuscation key includes the User-Agent).
export class CPAApiKeyNamesExportError extends Error {
  constructor(readonly reason: 'invalid' | 'undecodable') {
    super(reason === 'invalid' ? 'Invalid CPA names export' : 'CPA names could not be decoded');
    this.name = 'CPAApiKeyNamesExportError';
  }
}

const LOOPBACK_HOSTS = new Set(['localhost', '127.0.0.1', '[::1]']);

// Fingerprint the same tuple as CPAMC; never persist or export the API key itself.
export function cpaApiKeyNameFingerprint(apiBase: string, apiKey: string): string {
  return bytesToHex(sha256(new TextEncoder().encode(JSON.stringify(['api-key-name', apiBase, apiKey]))));
}

export function resolveCPAApiBase(url: string, currentOrigin = window.location.origin): string {
  try {
    const parsed = new URL(url || currentOrigin, currentOrigin);
    if (!['http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password) return '';
    const path = parsed.pathname.replace(/\/(?:management\.html|v[08]\/management)\/?$/i, '').replace(/\/+$/, '');
    return parsed.origin + path;
  } catch {
    return '';
  }
}

// Comparison key for "is this the same CPA server". Loopback aliases are the same
// machine, so CPAMC opened via localhost still matches a Keeper configured with
// 127.0.0.1. The raw apiBase is still used for fingerprints, which must match exactly.
export function cpaApiBaseMatchKey(url: string, currentOrigin = window.location.origin): string {
  const base = resolveCPAApiBase(url, currentOrigin);
  if (!base) return '';
  try {
    const parsed = new URL(base);
    if (LOOPBACK_HOSTS.has(parsed.hostname)) parsed.hostname = 'localhost';
    return parsed.origin + parsed.pathname.replace(/\/+$/, '');
  } catch {
    return base;
  }
}

// Distinct CPA addresses found in an export, for explaining an address mismatch.
export function describeCPAApiKeyNameSources(sources: CPAApiKeyNameSource[], currentOrigin = window.location.origin): string[] {
  return [...new Set(sources.map((source) => resolveCPAApiBase(source.apiBase, currentOrigin) || source.apiBase))];
}

export function filterCPAApiKeyNameSources(sources: CPAApiKeyNameSource[], cpaURL: string, currentOrigin = window.location.origin): CPAApiKeyNameSource[] {
  const target = cpaApiBaseMatchKey(cpaURL, currentOrigin);
  if (!target) return [];
  return sources.filter((source) => cpaApiBaseMatchKey(source.apiBase, currentOrigin) === target);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function decodeNames(value: string, host: string, userAgent: string): Record<string, string> {
  let text = value;
  if (text.startsWith(OBFUSCATION_PREFIX)) {
    const bytes = Uint8Array.from(atob(text.slice(OBFUSCATION_PREFIX.length)), (char) => char.charCodeAt(0));
    const mask = new TextEncoder().encode(`${OBFUSCATION_SALT}|${host}|${userAgent}`);
    text = new TextDecoder().decode(bytes.map((byte, index) => byte ^ mask[index % mask.length]));
  }
  const parsed: unknown = JSON.parse(text);
  if (!isRecord(parsed)) throw new Error('Invalid CPA names');
  return Object.fromEntries(Object.entries(parsed).filter((entry): entry is [string, string] => {
    const [fingerprint, name] = entry;
    return /^[a-f0-9]{64}$/.test(fingerprint) && typeof name === 'string'
      && !!name.trim() && Array.from(name.trim()).length <= 128 && !/\p{Cc}/u.test(name.trim());
  }).map(([fingerprint, name]) => [fingerprint, name.trim()]));
}

// Only inspect the name namespace for the configured CPA API base. Other storage can contain credentials.
export function readAccessibleCPAApiKeyNames(cpaURL: string, currentWindow: Window = window): CPAApiKeyNameSource[] {
  const targetBase = cpaApiBaseMatchKey(cpaURL, currentWindow.location.origin);
  if (!targetBase) return [];
  const sources: CPAApiKeyNameSource[] = [];
  const seenSources = new Set<string>();
  const frames = [currentWindow];
  if (currentWindow.parent && currentWindow.parent !== currentWindow) frames.push(currentWindow.parent);
  for (const frame of frames) {
    try {
      const storage = frame.localStorage;
      for (let index = 0; index < storage.length; index++) {
        const key = storage.key(index);
        if (!key?.startsWith(CPA_API_KEY_NAMES_PREFIX)) continue;
        const apiBase = key.slice(CPA_API_KEY_NAMES_PREFIX.length);
        if (cpaApiBaseMatchKey(apiBase, currentWindow.location.origin) !== targetBase) continue;
        try {
          const value = storage.getItem(key);
          // Keep a malformed or unexpectedly large browser value from blocking
          // every settings render while parsing and decoding it.
          const sourceID = value ? `${apiBase}\u0000${value}` : '';
          if (value && value.length <= MAX_IMPORT_LENGTH && !seenSources.has(sourceID)) {
            seenSources.add(sourceID);
            sources.push({ apiBase, names: decodeNames(value, frame.location.host, frame.navigator.userAgent) });
          }
        } catch {
          // Corrupted or outdated browser obfuscation must not break Keeper settings.
        }
      }
    } catch {
      // Cross-origin or blocked storage needs an explicit export/import instead.
    }
  }
  return sources;
}

export function matchCPAApiKeyNames(
  apiKeys: CpaApiKeySettingsItem[],
  sources: CPAApiKeyNameSource[],
  cpaURL: string,
  currentOrigin = window.location.origin,
): CPAApiKeyAliasImport[] {
  const matchingSources = filterCPAApiKeyNameSources(sources, cpaURL, currentOrigin);
  if (!matchingSources.length) return [];
  return apiKeys.flatMap((item) => {
    if (item.keyAlias.trim() || !item.apiKey) return [];
    for (const source of matchingSources) {
      const name = source.names[cpaApiKeyNameFingerprint(source.apiBase, item.apiKey)];
      if (name) return [{ id: item.id, keyAlias: name }];
    }
    return [];
  });
}

// Export only the name namespace and its browser decoding context, never auth/config storage.
export const CPA_API_KEY_NAMES_EXPORT_SCRIPT = `copy(JSON.stringify({
  format: 'cpamc-api-key-names', version: 1,
  host: location.host, userAgent: navigator.userAgent,
  entries: Object.keys(localStorage)
    .filter(key => key.startsWith('api-key-names:v1:'))
    .map(key => ({ apiBase: key.slice('api-key-names:v1:'.length), value: localStorage.getItem(key) }))
}))`;

// The export contains every CPA address this browser ever used. One stale or
// corrupted record must not block the others, so bad entries are skipped and only
// an export with nothing usable is rejected.
export function parseCPAApiKeyNamesExport(text: string): CPAApiKeyNameSource[] {
  if (text.length > MAX_IMPORT_LENGTH) throw new CPAApiKeyNamesExportError('invalid');
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    throw new CPAApiKeyNamesExportError('invalid');
  }
  if (!isRecord(parsed) || parsed.format !== EXPORT_FORMAT || parsed.version !== 1
    || typeof parsed.host !== 'string' || !parsed.host || parsed.host.length > 1024
    || typeof parsed.userAgent !== 'string' || parsed.userAgent.length > 4096
    || !Array.isArray(parsed.entries) || parsed.entries.length > 1000) {
    throw new CPAApiKeyNamesExportError('invalid');
  }
  const host = parsed.host;
  const userAgent = parsed.userAgent;
  const sources: CPAApiKeyNameSource[] = [];
  let undecodable = 0;
  for (const entry of parsed.entries as unknown[]) {
    if (!isRecord(entry) || typeof entry.apiBase !== 'string' || !/^https?:\/\//i.test(entry.apiBase)
      || !resolveCPAApiBase(entry.apiBase) || typeof entry.value !== 'string') {
      continue;
    }
    try {
      sources.push({ apiBase: entry.apiBase, names: decodeNames(entry.value, host, userAgent) });
    } catch {
      undecodable++;
    }
  }
  if (!sources.length && parsed.entries.length > 0) {
    throw new CPAApiKeyNamesExportError(undecodable > 0 ? 'undecodable' : 'invalid');
  }
  return sources;
}
