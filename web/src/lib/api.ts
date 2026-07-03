// Thin typed client for the documented REST/WS API. The UI must go through
// this module only — no reaching into backend internals.

export interface Status {
  version: string;
  uptime_seconds: number;
  paused: boolean;
  paused_until?: string;
  queries_total: number;
  queries_blocked: number;
  entries_dropped: number;
  rules: number;
  allow_rules: number;
  rules_skipped: number;
}

export interface LogEntry {
  time: string;
  client: string;
  qname: string;
  qtype: string;
  verdict: 'blocked' | 'allowed';
  list?: string;
  rule?: string;
  upstream?: string;
  duration_ms: number;
}

export interface TimelineBucket {
  time: string;
  total: number;
  blocked: number;
}

export interface Stats {
  window_hours: number;
  timeline: TimelineBucket[];
  top_blocked: { qname: string; count: number }[];
  top_clients: { client: string; total: number; blocked: number }[];
}

export interface CheckResult {
  domain: string;
  verdict: 'blocked' | 'allowed';
  list: string;
  rule: string;
}

export interface Upstream {
  address: string;
  protocol: 'udp' | 'tcp' | 'dot' | 'doh';
}

export interface ConfigView {
  dns: { listen: string; upstreams: Upstream[]; block_ttl: number };
  blocking: { mode: 'zero_ip' | 'nxdomain' };
  lists: { refresh_interval: string };
  querylog: { ephemeral: boolean; db_path: string; ring_size: number; retention_days: number };
  api: { listen: string; token_set: boolean };
}

// Partial settings update; omitted fields are left untouched by the server.
export interface SettingsUpdate {
  dns?: { upstreams?: Upstream[]; block_ttl?: number };
  blocking?: { mode?: string };
  lists?: { refresh_interval?: string };
  querylog?: { ring_size?: number; retention_days?: number };
  api?: { token?: string };
}

export interface ListStatus {
  name: string;
  url: string;
  format: 'hosts' | 'plain' | 'adblock';
  enabled: boolean;
  rules: number;
  skipped: number;
  last_refresh?: string;
  last_error?: string;
}

const TOKEN_KEY = 'minos-api-token';

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) ?? '';
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token);
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  const token = getToken();
  if (token) headers['X-Api-Token'] = token;
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const resp = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const data: unknown = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    const msg =
      typeof data === 'object' && data !== null && 'error' in data
        ? String((data as { error: unknown }).error)
        : resp.statusText;
    throw new ApiError(resp.status, msg);
  }
  return data as T;
}

export const api = {
  status: () => request<Status>('GET', '/api/status'),
  stats: (hours = 24) => request<Stats>('GET', `/api/stats?hours=${hours}`),
  check: (domain: string) =>
    request<CheckResult>('GET', `/api/check?domain=${encodeURIComponent(domain)}`),
  querylog: (limit = 100) => request<LogEntry[]>('GET', `/api/querylog?limit=${limit}`),

  getConfig: () => request<ConfigView>('GET', '/api/config'),
  updateConfig: (upd: SettingsUpdate) => request<ConfigView>('PUT', '/api/config', upd),

  lists: () => request<ListStatus[]>('GET', '/api/lists'),
  addList: (l: { name: string; url: string; format: string; enabled: boolean }) =>
    request<ListStatus[]>('POST', '/api/lists', l),
  updateList: (name: string, upd: { url?: string; format?: string; enabled?: boolean }) =>
    request<ListStatus[]>('PUT', `/api/lists/${encodeURIComponent(name)}`, upd),
  deleteList: (name: string) =>
    request<ListStatus[]>('DELETE', `/api/lists/${encodeURIComponent(name)}`),
  refreshLists: () => request<ListStatus[]>('POST', '/api/lists/refresh'),

  allowlist: () => request<string[]>('GET', '/api/allowlist'),
  denylist: () => request<string[]>('GET', '/api/denylist'),
  pardon: (domain: string) => request<unknown>('POST', '/api/allowlist', { domain }),
  unpardon: (domain: string) =>
    request<unknown>('DELETE', `/api/allowlist/${encodeURIComponent(domain)}`),
  sentence: (domain: string) => request<unknown>('POST', '/api/denylist', { domain }),
  unsentence: (domain: string) =>
    request<unknown>('DELETE', `/api/denylist/${encodeURIComponent(domain)}`),

  pause: (duration: string) =>
    request<{ paused: boolean; paused_until?: string }>('POST', '/api/pause', { duration }),
  resume: () => request<unknown>('DELETE', '/api/pause'),
};

// exportConfig downloads the config backup. Fetched with auth headers, then
// handed to the browser as a Blob download.
export async function exportConfig(): Promise<void> {
  const headers: Record<string, string> = {};
  const token = getToken();
  if (token) headers['X-Api-Token'] = token;
  const resp = await fetch('/api/config/export', { headers });
  if (!resp.ok) throw new ApiError(resp.status, 'export failed');
  const blob = await resp.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = 'minos.yaml';
  a.click();
  URL.revokeObjectURL(url);
}

// openStream connects to the live query log. Browsers cannot set headers on
// WebSockets, so the token rides a query parameter (stream endpoint only).
export function openStream(onEntry: (e: LogEntry) => void, onDrop: () => void): WebSocket {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const token = getToken();
  const qs = token ? `?token=${encodeURIComponent(token)}` : '';
  const ws = new WebSocket(`${proto}//${location.host}/api/querylog/stream${qs}`);
  ws.onmessage = (ev) => {
    try {
      onEntry(JSON.parse(ev.data as string) as LogEntry);
    } catch {
      // ignore malformed frames
    }
  };
  ws.onclose = onDrop;
  return ws;
}
