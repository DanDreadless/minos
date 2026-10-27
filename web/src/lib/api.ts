// Thin typed client for the documented REST/WS API. The UI must go through
// this module only — no reaching into backend internals.

export interface Status {
  // Sequence number of the newest warning/error in the application log
  // (0: none since start) — the Logs nav badge compares it with the last seen.
  log_alert_seq?: number;
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
  // Absent entirely when dns.dnssec is off — the UI hides the card on
  // absence rather than reading a separate enabled flag. Counters are
  // process-lifetime totals and reset on restart; the windowed
  // would-block figure lives on Stats.dnssec instead.
  dnssec?: DNSSECStatus;
}

export interface DNSSECStatus {
  mode: 'permissive' | 'enforce';
  secure: number;
  insecure: number;
  bogus: number;
  indeterminate: number;
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
  audit_list?: string; // an audit-mode list would have blocked this
  audit_rule?: string;
  // DNSSEC validation outcome for this answer. Absent when validation was
  // off, when nothing was judged (a block, a local record), or on a cache
  // hit — hits are not re-validated, so this records resolutions.
  dnssec?: 'secure' | 'insecure' | 'bogus' | 'indeterminate';
}

// Suggestion: a device retrying a blocked name in a burst — an app that
// blocking has probably broken.
export interface Suggestion {
  client: string;
  device?: string;
  device_key: string; // For deviceHref
  worked: number; // Normal answers in the week before
  worked_last: string;
  domain: string;
  list: string;
  rule?: string;
  count: number;
  since: string;
  last: string;
}

// Threat: a device behaving like malware (threat detection).
export interface Threat {
  kind: 'dga' | 'tunnel' | 'known_threat';
  client: string;
  device?: string;
  device_key: string; // For deviceHref
  domain?: string;
  list?: string;
  count: number;
  distinct?: number;
  samples: string[];
  first: string;
  last: string;
}

// HistoryEntry is one recorded settings change (Settings → History).
export interface HistoryEntry {
  id: string;
  time: string;
  actor: string;
  note?: string;
  keys: string[];
}

export interface DiffLine {
  op: ' ' | '+' | '-';
  text: string;
}

export interface TemporaryAllow {
  domain: string;
  until: string;
}

export interface UpstreamTestResult {
  ok: boolean;
  ms: number;
  rcode?: string;
  error?: string;
  configured: boolean;
}

export interface UpdateInfo {
  current: string;
  latest?: string;
  available: boolean;
  install_method: string;
  command: string;
  notes_url: string;
  // Self_update is true when the one-click update works on this install;
  // otherwise self_update_blocked says why not.
  self_update: boolean;
  self_update_blocked?: string;
}

// UpdateProgress is the updater's last report.
export interface UpdateProgress {
  state: '' | 'requested' | 'downloading' | 'verifying' | 'installing' | 'restarting' | 'done' | 'failed' | 'rolled_back';
  version?: string;
  from?: string;
  message?: string;
  time?: number;
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
  // Present while validation is on: how many answers permissive mode let
  // through that enforce would have refused, over the same window. The
  // /status counters are process-lifetime; this one is windowed.
  dnssec?: {
    would_block: number;
    top_domains: { qname: string; count: number }[];
  };
}

// The machine Minos runs on. supported is false on container installs and
// then nothing else is present — a container cannot answer "how is the
// machine running Minos doing?", so it reports nothing rather than
// something confidently about the wrong subject.
//
// Every reading is optional: a platform that cannot measure one omits it,
// and the UI must render an em dash rather than treating absence as zero.
export interface Host {
  supported: boolean;
  hostname?: string;
  os?: string;
  arch?: string;
  cpus?: number;
  kernel?: string;
  platform?: string;
  go_version?: string;
  container?: boolean;
  mem_total?: number;
  mem_source?: 'host' | 'cgroup';
  sample?: {
    time: string;
    cpu_percent?: number;
    load1?: number;
    load5?: number;
    load15?: number;
    mem_used?: number;
    mem_available?: number;
    disk_total?: number;
    disk_free?: number;
    temp_celsius?: number;
    uptime_seconds?: number;
    proc_rss?: number;
    goroutines: number;
  };
}

export interface ClientOverview {
  window_hours: number;
  // Where the counted window starts. Windows of 48 h or more count whole
  // days (UTC), so this can be earlier than now minus window_hours.
  window_start?: string;
  total: number;
  blocked: number;
  top_allowed: { qname: string; count: number }[];
  top_blocked: { qname: string; count: number }[];
}

export interface ListStats {
  window_hours: number;
  lists: { list: string; count: number }[];
}

// The dashboard's weekly summary (GET /api/summary).
export interface SummaryDevice {
  key: string;
  name: string;
  address: string;
  lookups?: number;
}

export interface WeekSummary {
  start: string;
  lookups: number;
  blocked: number;
  prev_lookups: number;
  prev_blocked: number;
  new_devices: SummaryDevice[];
  new_device_count: number;
  busiest?: SummaryDevice;
  security: { blocked: number; devices: SummaryDevice[]; lists: string[] };
  security_lists: string[];
  tracking: { companies: number; allowed: number; blocked: number; top: { id: string; name: string; allowed: number }[] };
  settings_changes: number;
  last_change?: HistoryEntry;
}

// The privacy report (GET /api/privacy).
export interface CompanyReport {
  id: string;
  name: string;
  categories: string[];
  tracking: boolean;
  allowed: number; // Tracking lookups that got through
  blocked: number;
  other: number; // Lookups of its own sites and services
  domains: { name: string; tracker: string; category: string; tracking: boolean; allowed: number; blocked: number }[];
  devices?: { key: string; name: string }[];
  blockable: boolean;
  blocked_all: boolean;
}

export interface PrivacyReport {
  days: number;
  window_start: string;
  attribution: string;
  companies: CompanyReport[];
}

// The blocklist value report (GET /api/lists/value).
export interface ListValue {
  list: string;
  security: boolean; // A threat-intelligence list
  rules: number;
  unique_rules: number;
  covered_by: { list: string; rules: number }[];
  freed_bytes: number;
  blocks: number;
  only_blocks: number;
  only_examples: { qname: string; count: number }[];
}

export interface ListValueReport {
  days: number;
  compared: string[];
  blocked_lookups: number;
  names_truncated: boolean;
  lists: ListValue[];
}

export interface CheckResult {
  domain: string;
  verdict: 'blocked' | 'allowed';
  list: string;
  rule: string;
}

export interface Upstream {
  address: string;
  protocol: 'udp' | 'tcp' | 'dot' | 'doh' | 'doh3' | 'doq';
}

// Live health per upstream (keyed by the configured address). state is the
// server's single verdict — the UI shows it rather than re-deriving one:
//   healthy: has answered and is not currently failing
//   failing: the breaker is open, or it has never answered at all
//   unused:  never asked since start (the resolvers ahead answered everything)
export interface UpstreamHealth {
  address: string;
  requests: number;
  failures: number;
  avg_ms: number;
  sick: boolean;
  state: 'healthy' | 'failing' | 'unused';
  last_success?: string;
  last_failure?: string;
  last_error?: string;
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
    allowed_networks: string[]; // [] = private and local networks only
    rate_limit: number; // queries a minute per device; 0 = off
    routes: Route[];
    allow_firefox_doh: boolean;
    rebind_protection: boolean;
    dnssec: 'off' | 'permissive' | 'enforce';
  };
  blocking: {
    mode: 'zero_ip' | 'nxdomain';
    safe_search: boolean;
    block_icloud_private_relay: boolean;
    cname_inspection: boolean;
  };
  lists: { refresh_interval: string };
  querylog: {
    ephemeral: boolean;
    db_path: string;
    ring_size: number;
    retention_days: number;
    memory_mb: number;
  };
  api: { listen: string; token_set: boolean; password_set: boolean; totp_enabled: boolean };
  update_check: boolean;
  notifications: {
    webhook_url: string;
    ntfy_url: string;
    ntfy_token_set: boolean;
    digest: 'off' | 'daily' | 'weekly';
    digest_time: string; // 24h "HH:MM", server-local
    digest_day: string; // lowercase day name, weekly only
    ui_url: string; // where notification links point; '' = ui_url_default
    ui_url_default: string;
    quiet_hours: string; // "22:00-07:00" or ''
    mute: string[]; // event types never sent
    types: string[];
  };
  discovery: { ssdp: boolean; dhcp_listen: boolean };
  threats: { enabled: boolean; ignore: string[] };
  home_assistant: HomeAssistantSettings & { password_set: boolean };
  log_level: string; // '' = the -log-level flag's (info by default)
}

// Home Assistant over MQTT (the password is write-only).
export interface HomeAssistantSettings {
  enabled: boolean;
  broker: string; // mqtt://host:1883 or mqtts://host:8883
  username: string;
  discovery_prefix: string; // '' = homeassistant
  topic_prefix: string; // '' = minos
  device_switches: boolean;
}

// The MQTT connection as it stands.
export interface HomeAssistantStatus {
  enabled: boolean;
  connected: boolean;
  error?: string;
  since?: string;
}

// Partial settings update; omitted fields are left untouched by the server.
export interface SettingsUpdate {
  dns?: {
    upstreams?: Upstream[];
    block_ttl?: number;
    cache?: Partial<CacheSettings>;
    local_records?: LocalRecord[];
    local_ttl?: number;
    allowed_networks?: string[];
    rate_limit?: number;
    routes?: Route[];
    allow_firefox_doh?: boolean;
    rebind_protection?: boolean;
    dnssec?: string;
  };
  blocking?: {
    mode?: string;
    safe_search?: boolean;
    block_icloud_private_relay?: boolean;
    cname_inspection?: boolean;
  };
  lists?: { refresh_interval?: string };
  threats?: { enabled?: boolean; ignore?: string[] };
  home_assistant?: Partial<HomeAssistantSettings> & { password?: string };
  querylog?: { ring_size?: number; retention_days?: number; memory_mb?: number };
  api?: { token?: string };
  update_check?: boolean;
  notifications?: {
    webhook_url?: string;
    ntfy_url?: string;
    ntfy_token?: string;
    digest?: string;
    digest_time?: string;
    digest_day?: string;
    ui_url?: string;
    quiet_hours?: string;
    mute?: string[];
  };
  log_level?: string;
  discovery?: { ssdp?: boolean; dhcp_listen?: boolean };
}

export interface ListStatus {
  name: string;
  url: string;
  format: 'hosts' | 'plain' | 'adblock';
  action: 'block' | 'allow';
  enabled: boolean;
  audit: boolean;
  rules: number;
  skipped: number;
  // last_refresh: the last time the publisher was reached (200 or 304) — the
  // age of the copy in force. last_attempt: the latest try, success or not.
  last_refresh?: string;
  last_error?: string;
  state: ListState;
  last_attempt?: string;
  consecutive_failures?: number;
  mirrors?: string[];
  // served_by: the mirror the copy in force came from (absent = primary);
  // primary_error: why the primary was passed over.
  served_by?: string;
  primary_error?: string;
}

// The server's single health verdict per list (internal/lists ListState).
export type ListState =
  | 'pending'
  | 'ok'
  | 'failing'
  | 'stale'
  | 'unavailable'
  | 'retired'
  | 'unauthorized'
  | 'suspicious'
  | 'disabled';

export interface Device {
  ip: string; // primary (most recently active) address
  ips?: string[]; // every address this device has used; primary included
  mac?: string;
  vendor?: string;
  private_mac?: boolean; // randomized (locally administered) MAC
  hostname?: string;
  name_source?: string; // ptr | netbios | mdns | ssdp | dhcp
  model?: string; // discovered self-description (mDNS/UPnP)
  hint?: string; // OS/type guess (traffic patterns, DHCP fingerprint)
  name?: string;
  notes?: string; // free-form user text, persisted with the assignment
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
  custom_services: string[] | null;
  allowed_custom_services: string[] | null;
  safe_search: boolean;
  schedule?: Schedule | null;
}

export interface Service {
  name: string;
  label: string;
  domains: string[];
}

// A user-defined service bundle. Unlike catalog services (whose global
// selection lives in ServicesView.blocked/allowed), a custom's global
// toggles ride on the definition itself.
export interface CustomService {
  name: string;
  label: string;
  domains: string[];
  allow_extra?: string[];
  blocked: boolean;
  allowed: boolean;
}

export interface ServicesView {
  catalog: Service[];
  blocked: string[];
  allowed: string[];
  custom: CustomService[];
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
    // NeedCode is set when a sign-in also needs an authenticator code.
    public needCode = false,
  ) {
    super(sentence(message));
  }
}

// Server messages are lowercase Go errors; every message the UI shows
// starts with a capital.
function sentence(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

export interface AuthStatus {
  password_set: boolean;
  totp_enabled: boolean;
  token_set: boolean;
  authenticated: boolean;
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const headers: Record<string, string> = {};
  const token = getToken();
  if (token) headers['X-Api-Token'] = token;
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const resp = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    signal,
  });
  const data: unknown = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    const obj = typeof data === 'object' && data !== null ? (data as { error?: unknown; need_code?: unknown }) : {};
    const msg = obj.error !== undefined ? String(obj.error) : resp.statusText;
    throw new ApiError(resp.status, msg, obj.need_code === true);
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
  // Sign-in. The session is an HttpOnly cookie the browser sends by itself.
  authStatus: () => request<AuthStatus>('GET', '/api/auth'),
  login: (password: string, code = '') => request<unknown>('POST', '/api/auth/login', { password, code }),
  logout: () => request<unknown>('POST', '/api/auth/logout'),
  setPassword: (current: string, next: string) => request<unknown>('PUT', '/api/auth/password', { current, new: next }),
  removePassword: (current: string) => request<unknown>('DELETE', '/api/auth/password', { current }),
  signOutEverywhere: () => request<unknown>('POST', '/api/auth/signout-all'),
  totpSetup: () => request<{ secret: string; uri: string }>('POST', '/api/auth/totp/setup'),
  totpEnable: (code: string) => request<unknown>('POST', '/api/auth/totp/enable', { code }),
  totpDisable: (current: string) => request<unknown>('DELETE', '/api/auth/totp', { current }),
  upstreams: () => request<UpstreamHealth[]>('GET', '/api/upstreams'),
  // TestUpstream asks one resolver a test question now; a saved one's
  // health light updates with the result.
  testUpstream: (u: Upstream) => request<UpstreamTestResult>('POST', '/api/upstreams/test', u),
  update: () => request<UpdateInfo>('GET', '/api/update'),
  updateProgress: () => request<UpdateProgress>('GET', '/api/update/progress'),
  installUpdate: () => request<unknown>('POST', '/api/update/install'),
  stats: (hours = 24) => request<Stats>('GET', `/api/stats?hours=${hours}`),
  clientStats: (clients: string[], hours = 24) =>
    request<ClientOverview>(
      'GET',
      `/api/stats/client?client=${encodeURIComponent(clients.join(','))}&hours=${hours}`,
    ),
  summary: () => request<WeekSummary>('GET', '/api/summary'),
  privacy: (days = 7, clients: string[] = []) =>
    request<PrivacyReport>(
      'GET',
      `/api/privacy?days=${days}` + (clients.length ? `&client=${encodeURIComponent(clients.join(','))}` : ''),
    ),
  blockCompany: (id: string, blocked: boolean) =>
    request<{ id: string; blocked: boolean; domains: number }>('POST', '/api/privacy/companies', { id, blocked }),
  listValue: (days = 30) => request<ListValueReport>('GET', `/api/lists/value?days=${days}`),
  listStats: (hours = 168) => request<ListStats>('GET', `/api/stats/lists?hours=${hours}`),
  check: (domain: string) =>
    request<CheckResult>('GET', `/api/check?domain=${encodeURIComponent(domain)}`),
  querylog: (limit = 100) => request<LogEntry[]>('GET', `/api/querylog?limit=${limit}`),
  // signal lets a superseded search be abandoned; the server sees the
  // disconnect and interrupts the SQLite scan rather than finishing it.
  querylogHistory: (
    params: {
      q?: string;
      client?: string; // exact address(es), comma-separated for a multi-IP device
      verdict?: string;
      would_block?: boolean;
      list?: string; // exact list name (enforcing or audit attribution)
      dnssec?: string; // one validation outcome, for the dashboard drill-down
      before?: number;
      limit?: number;
    },
    signal?: AbortSignal,
  ) => {
    const sp = new URLSearchParams();
    if (params.q) sp.set('q', params.q);
    if (params.client) sp.set('client', params.client);
    if (params.verdict && params.verdict !== 'all') sp.set('verdict', params.verdict);
    if (params.would_block) sp.set('would_block', 'true');
    if (params.list) sp.set('list', params.list);
    if (params.dnssec) sp.set('dnssec', params.dnssec);
    if (params.before) sp.set('before', String(params.before));
    sp.set('limit', String(params.limit ?? 200));
    return request<LogEntry[]>('GET', `/api/querylog/history?${sp.toString()}`, undefined, signal);
  },
  // The Query Log's search: params come from querysyntax.toParams, plus
  // since/before/limit.
  querylogSearch: (params: URLSearchParams, signal?: AbortSignal) =>
    request<LogEntry[]>('GET', `/api/querylog/history?${params.toString()}`, undefined, signal),
  // Downloads every match as one file. Fetched rather than linked so the API
  // token travels in its header, never in a URL.
  querylogExport: async (params: URLSearchParams): Promise<{ blob: Blob; filename: string }> => {
    const headers: Record<string, string> = {};
    const token = getToken();
    if (token) headers['X-Api-Token'] = token;
    const resp = await fetch(`/api/querylog/export?${params.toString()}`, { headers });
    if (!resp.ok) {
      const data: unknown = await resp.json().catch(() => ({}));
      const msg =
        typeof data === 'object' && data !== null && 'error' in data
          ? String((data as { error: unknown }).error)
          : resp.statusText;
      throw new ApiError(resp.status, msg);
    }
    const cd = resp.headers.get('Content-Disposition') ?? '';
    const filename = /filename="([^"]+)"/.exec(cd)?.[1] ?? 'minos-querylog';
    return { blob: await resp.blob(), filename };
  },
  // Distinct list names attributed in the window, for the Query Log's filter.
  querylogLists: (hours = 168) => request<string[]>('GET', `/api/querylog/lists?hours=${hours}`),

  host: () => request<Host>('GET', '/api/host'),

  getConfig: () => request<ConfigView>('GET', '/api/config'),
  updateConfig: (upd: SettingsUpdate) => request<ConfigView>('PUT', '/api/config', upd),
  appLogs: () => request<AppLogs>('GET', '/api/logs'),
  // The held log as a text file, fetched so the token stays in a header.
  appLogsDownload: async (): Promise<{ blob: Blob; filename: string }> => {
    const headers: Record<string, string> = {};
    const token = getToken();
    if (token) headers['X-Api-Token'] = token;
    const resp = await fetch('/api/logs/download', { headers });
    if (!resp.ok) throw new ApiError(resp.status, resp.statusText);
    const cd = resp.headers.get('Content-Disposition') ?? '';
    return {
      blob: await resp.blob(),
      filename: /filename="([^"]+)"/.exec(cd)?.[1] ?? 'minos-log.txt',
    };
  },
  // Sends a test notification now; the result maps each destination
  // ("webhook", "ntfy") to "ok" or the reason it failed.
  homeAssistantStatus: () => request<HomeAssistantStatus>('GET', '/api/home-assistant/status'),
  testNotification: () => request<Record<string, string>>('POST', '/api/notifications/test'),

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
  addList: (l: {
    name: string;
    url: string;
    format: string;
    action?: string;
    audit?: boolean;
    enabled: boolean;
    mirrors?: string[];
  }) =>
    request<ListStatus[]>('POST', '/api/lists', l),
  updateList: (
    name: string,
    upd: {
      url?: string;
      format?: string;
      action?: string;
      audit?: boolean;
      enabled?: boolean;
      mirrors?: string[];
    },
  ) =>
    request<ListStatus[]>('PUT', `/api/lists/${encodeURIComponent(name)}`, upd),
  deleteList: (name: string) =>
    request<ListStatus[]>('DELETE', `/api/lists/${encodeURIComponent(name)}`),
  refreshLists: () => request<ListStatus[]>('POST', '/api/lists/refresh'),

  allowlist: () => request<string[]>('GET', '/api/allowlist'),
  denylist: () => request<string[]>('GET', '/api/denylist'),
  pardon: (domain: string) => request<unknown>('POST', '/api/allowlist', { domain }),
  // AllowFor allows a domain for a while; it is blocked again afterwards.
  allowFor: (domain: string, minutes: number) =>
    request<{ domain: string; until?: string; status: string }>('POST', '/api/allowlist', { domain, minutes }),
  temporaryAllows: () => request<TemporaryAllow[]>('GET', '/api/allowlist/temporary'),
  suggestions: () => request<Suggestion[]>('GET', '/api/suggestions'),
  threats: () => request<Threat[]>('GET', '/api/threats'),
  settingsHistory: () => request<HistoryEntry[]>('GET', '/api/settings/history'),
  settingsDiff: (id: string) => request<DiffLine[]>('GET', `/api/settings/history/${encodeURIComponent(id)}/diff`),
  restoreSettings: (id: string) => request<unknown>('POST', `/api/settings/history/${encodeURIComponent(id)}/restore`),
  dismissThreat: (t: Threat) =>
    request<unknown>('POST', '/api/threats/dismiss', { kind: t.kind, client: t.client, domain: t.kind === 'known_threat' ? '' : (t.domain ?? '') }),
  ignoreThreatDomain: (domain: string) => request<unknown>('POST', '/api/threats/ignore', { domain }),
  dismissSuggestion: (client: string, domain: string) =>
    request<unknown>('POST', '/api/suggestions/dismiss', { client, domain }),
  removeTemporaryAllow: (domain: string) =>
    request<unknown>('DELETE', `/api/allowlist/temporary?domain=${encodeURIComponent(domain)}`),
  unpardon: (domain: string) =>
    request<unknown>('DELETE', `/api/allowlist?domain=${encodeURIComponent(domain)}`),
  sentence: (domain: string) => request<unknown>('POST', '/api/denylist', { domain }),
  unsentence: (domain: string) =>
    request<unknown>('DELETE', `/api/denylist?domain=${encodeURIComponent(domain)}`),

  clients: () => request<Device[]>('GET', '/api/clients'),
  // key is the device's MAC when it has one (so the assignment follows it
  // across DHCP leases), else its IP. `ip` is a last-known-address hint used
  // only when creating a MAC-keyed assignment for a device that's offline.
  updateClient: (
    key: string,
    upd: {
      name?: string;
      mac?: string;
      group?: string;
      blocked?: boolean;
      notes?: string;
      ip?: string;
    },
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
      custom_services?: string[];
      allowed_custom_services?: string[];
      safe_search?: boolean;
      schedule?: Schedule | null;
    },
  ) => request<Group[]>('PUT', `/api/groups/${encodeURIComponent(name)}`, upd),

  services: () => request<ServicesView>('GET', '/api/services'),
  // Partial update: an omitted field leaves that set unchanged.
  updateServices: (upd: { blocked?: string[]; allowed?: string[] }) =>
    request<ServicesView>('PUT', '/api/services', upd),
  // Custom services: name omitted → the server slugifies the label.
  addCustomService: (c: {
    name?: string;
    label: string;
    domains: string[];
    allow_extra?: string[];
    blocked?: boolean;
    allowed?: boolean;
  }) => request<ServicesView>('POST', '/api/services/custom', c),
  // Partial update; the name is the stable key (no rename).
  updateCustomService: (
    name: string,
    upd: {
      label?: string;
      domains?: string[];
      allow_extra?: string[];
      blocked?: boolean;
      allowed?: boolean;
    },
  ) => request<ServicesView>('PUT', `/api/services/custom/${encodeURIComponent(name)}`, upd),
  deleteCustomService: (name: string) =>
    request<ServicesView>('DELETE', `/api/services/custom/${encodeURIComponent(name)}`),
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
// One line of Minos's own application log (secrets redacted server-side).
export interface AppLogRecord {
  seq: number;
  time: string;
  level: 'debug' | 'info' | 'warn' | 'error';
  message: string;
  attrs?: { key: string; value: string }[];
}

export interface AppLogs {
  level: AppLogRecord['level']; // what is being recorded now
  records: AppLogRecord[]; // oldest first
  alert_seq: number;
}

export function openLogStream(onRecord: (r: AppLogRecord) => void, onDrop: () => void): WebSocket {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const token = getToken();
  const qs = token ? `?token=${encodeURIComponent(token)}` : '';
  const ws = new WebSocket(`${proto}//${location.host}/api/logs/stream${qs}`);
  ws.onmessage = (ev) => {
    try {
      onRecord(JSON.parse(ev.data as string) as AppLogRecord);
    } catch {
      // Ignore malformed frames
    }
  };
  ws.onclose = onDrop;
  return ws;
}

export function openStream(onEntry: (e: LogEntry) => void, onDrop: () => void): WebSocket {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const token = getToken();
  const qs = token ? `?token=${encodeURIComponent(token)}` : '';
  const ws = new WebSocket(`${proto}//${location.host}/api/querylog/stream${qs}`);
  ws.onmessage = (ev) => {
    try {
      onEntry(JSON.parse(ev.data as string) as LogEntry);
    } catch {
      // Ignore malformed frames
    }
  };
  ws.onclose = onDrop;
  return ws;
}
