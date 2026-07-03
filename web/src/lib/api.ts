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
  querylog: (limit = 100) => request<LogEntry[]>('GET', `/api/querylog?limit=${limit}`),
  pardon: (domain: string) => request<unknown>('POST', '/api/allowlist', { domain }),
  pause: (duration: string) =>
    request<{ paused: boolean; paused_until?: string }>('POST', '/api/pause', { duration }),
  resume: () => request<unknown>('DELETE', '/api/pause'),
};

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
