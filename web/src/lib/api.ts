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
  cache_enabled: boolean;
  cache_hits: number;
  cache_misses: number;
  cache_entries: number;
  latest_version?: string;
  update_available: boolean;
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

export interface UpdateInfo {
  current: string;
  latest?: string;
  available: boolean;
  install_method: string;
  command: string;
  notes_url: string;
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

export interface ClientOverview {
  window_hours: number;
  total: number;
  blocked: number;
  top_allowed: { qname: string; count: number }[];
  top_blocked: { qname: string; count: number }[];
}

export interface ListStats {
  window_hours: number;
  lists: { list: string; count: number }[];
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

export interface CacheSettings {
  enabled: boolean;
  max_entries: number;
  min_ttl: number;
  max_ttl: number;
  serve_stale: boolean;
}

export interface Route {
  domains: string[];
  upstream: Upstream;
}

export interface LocalRecord {
  name: string;
  a?: string[];
  aaaa?: string[];
  cname?: string;
}

export interface ConfigView {
  dns: {
    listen: string;
    upstreams: Upstream[];
    block_ttl: number;
    cache: CacheSettings;
    local_records: LocalRecord[];
    local_ttl: number;
    routes: Route[];
  };
  blocking: { mode: 'zero_ip' | 'nxdomain'; safe_search: boolean };
  lists: { refresh_interval: string };
  querylog: { ephemeral: boolean; db_path: string; ring_size: number; retention_days: number };
  api: { listen: string; token_set: boolean };
  update_check: boolean;
  notifications: { webhook_url: string; ntfy_url: string; ntfy_token_set: boolean };
}

// Partial settings update; omitted fields are left untouched by the server.
export interface SettingsUpdate {
  dns?: {
    upstreams?: Upstream[];
    block_ttl?: number;
    cache?: Partial<CacheSettings>;
    local_records?: LocalRecord[];
    local_ttl?: number;
    routes?: Route[];
  };
  blocking?: { mode?: string; safe_search?: boolean };
  lists?: { refresh_interval?: string };
  querylog?: { ring_size?: number; retention_days?: number };
  api?: { token?: string };
  update_check?: boolean;
  notifications?: { webhook_url?: string; ntfy_url?: string; ntfy_token?: string };
}

export interface ListStatus {
  name: string;
  url: string;
  format: 'hosts' | 'plain' | 'adblock';
  action: 'block' | 'allow';
  enabled: boolean;
  rules: number;
  skipped: number;
  last_refresh?: string;
  last_error?: string;
}

export interface Device {
  ip: string; // primary (most recently active) address
  ips?: string[]; // every address this device has used; primary included
  mac?: string;
  vendor?: string;
  hostname?: string;
  name?: string;
  group: string;
  blocked: boolean;
  seen: boolean;
  queries: number;
  queries_blocked: number;
  first_seen?: string;
  last_seen?: string;
}

export interface Schedule {
  days?: string[]; // mon..sun; empty = every day
  start: string; // "HH:MM"
  end: string; // "HH:MM"; earlier than start wraps past midnight
}

export interface Group {
  name: string;
  mode: 'filter' | 'bypass' | 'block';
  allowlist: string[] | null;
  denylist: string[] | null;
  services: string[] | null;
  allowed_services: string[] | null;
  safe_search: boolean;
  schedule?: Schedule | null;
}

export interface Service {
  name: string;
  label: string;
  domains: string[];
}

export interface ServicesView {
  catalog: Service[];
  blocked: string[];
  allowed: string[];
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

export interface ImportReport {
  lists: number;
  allow: number;
  deny: number;
  local_records: number;
  services: number;
  skipped: string[];
}

// upload posts multipart form data (file uploads) with auth.
async function upload<T>(path: string, form: FormData): Promise<T> {
  const headers: Record<string, string> = {};
  const token = getToken();
  if (token) headers['X-Api-Token'] = token;
  const resp = await fetch(path, { method: 'POST', headers, body: form });
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

// uploadRaw posts a raw body (a config YAML) with auth.
async function uploadRaw<T>(path: string, body: Blob): Promise<T> {
  const headers: Record<string, string> = {};
  const token = getToken();
  if (token) headers['X-Api-Token'] = token;
  const resp = await fetch(path, { method: 'POST', headers, body });
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
  update: () => request<UpdateInfo>('GET', '/api/update'),
  stats: (hours = 24) => request<Stats>('GET', `/api/stats?hours=${hours}`),
  clientStats: (clients: string[], hours = 24) =>
    request<ClientOverview>(
      'GET',
      `/api/stats/client?client=${encodeURIComponent(clients.join(','))}&hours=${hours}`,
    ),
  listStats: (hours = 168) => request<ListStats>('GET', `/api/stats/lists?hours=${hours}`),
  check: (domain: string) =>
    request<CheckResult>('GET', `/api/check?domain=${encodeURIComponent(domain)}`),
  querylog: (limit = 100) => request<LogEntry[]>('GET', `/api/querylog?limit=${limit}`),
  querylogHistory: (params: {
    q?: string;
    client?: string; // exact address(es), comma-separated for a multi-IP device
    verdict?: string;
    before?: number;
    limit?: number;
  }) => {
    const sp = new URLSearchParams();
    if (params.q) sp.set('q', params.q);
    if (params.client) sp.set('client', params.client);
    if (params.verdict && params.verdict !== 'all') sp.set('verdict', params.verdict);
    if (params.before) sp.set('before', String(params.before));
    sp.set('limit', String(params.limit ?? 200));
    return request<LogEntry[]>('GET', `/api/querylog/history?${sp.toString()}`);
  },

  getConfig: () => request<ConfigView>('GET', '/api/config'),
  updateConfig: (upd: SettingsUpdate) => request<ConfigView>('PUT', '/api/config', upd),

  importPihole: (gravity: File, customList?: File) => {
    const form = new FormData();
    form.append('gravity', gravity);
    if (customList) form.append('custom_list', customList);
    return upload<ImportReport>('/api/import/pihole', form);
  },
  importAdGuard: (config: File) => {
    const form = new FormData();
    form.append('config', config);
    return upload<ImportReport>('/api/import/adguard', form);
  },
  importConfig: (file: File) => uploadRaw<ConfigView>('/api/config/import', file),

  lists: () => request<ListStatus[]>('GET', '/api/lists'),
  addList: (l: { name: string; url: string; format: string; action?: string; enabled: boolean }) =>
    request<ListStatus[]>('POST', '/api/lists', l),
  updateList: (name: string, upd: { url?: string; format?: string; action?: string; enabled?: boolean }) =>
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

  clients: () => request<Device[]>('GET', '/api/clients'),
  // key is the device's MAC when it has one (so the assignment follows it
  // across DHCP leases), else its IP. `ip` is a last-known-address hint used
  // only when creating a MAC-keyed assignment for a device that's offline.
  updateClient: (
    key: string,
    upd: { name?: string; mac?: string; group?: string; blocked?: boolean; ip?: string },
  ) => request<Device[]>('PUT', `/api/clients/${encodeURIComponent(key)}`, upd),
  deleteClient: (key: string) =>
    request<Device[]>('DELETE', `/api/clients/${encodeURIComponent(key)}`),

  groups: () => request<Group[]>('GET', '/api/groups'),
  addGroup: (g: { name: string; mode: string; allowlist?: string[]; denylist?: string[] }) =>
    request<Group[]>('POST', '/api/groups', g),
  updateGroup: (
    name: string,
    upd: {
      mode?: string;
      allowlist?: string[];
      denylist?: string[];
      services?: string[];
      allowed_services?: string[];
      safe_search?: boolean;
      schedule?: Schedule | null;
    },
  ) => request<Group[]>('PUT', `/api/groups/${encodeURIComponent(name)}`, upd),

  services: () => request<ServicesView>('GET', '/api/services'),
  // Partial update: an omitted field leaves that set unchanged.
  updateServices: (upd: { blocked?: string[]; allowed?: string[] }) =>
    request<ServicesView>('PUT', '/api/services', upd),
  deleteGroup: (name: string) =>
    request<Group[]>('DELETE', `/api/groups/${encodeURIComponent(name)}`),

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
