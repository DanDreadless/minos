// The Query Log's filter model and its small search syntax:
//
//   blocked  allowed  would-block      verdict
//   client:192.168.1.5                 exact device address (comma for several)
//   domain:example.com                 exact name — not its subdomains
//   type:AAAA                          record type
//   list:StevenBlack  list:"My list"   the list that matched (quote spaces)
//   upstream:1.1.1.1:853               the resolver that answered
//   dnssec:bogus                       validation outcome
//   -cdn                               leave out names containing "cdn"
//   anything else                      domain or client contains this text
//
// Pure functions only (no Svelte, no fetch) so the page stays thin and the
// rules live in one place. The same filter drives the server request, the
// URL hash, the chips, and matching live entries client-side.

import type { LogEntry } from './api';

export type Verdict = 'all' | 'blocked' | 'allowed' | 'would_block';
export type RangeKey = 'all' | '1h' | '24h' | '7d' | '30d' | '90d';

export interface QueryFilter {
  q: string; // substring of the domain or the client address
  domain: string; // one exact name
  exclude: string[];
  clients: string[];
  verdict: Verdict;
  list: string;
  qtype: string;
  upstream: string;
  dnssec: string;
  range: RangeKey;
}

export const RANGES: { key: RangeKey; label: string; ms: number }[] = [
  { key: '1h', label: 'Last hour', ms: 3600e3 },
  { key: '24h', label: 'Last 24 hours', ms: 24 * 3600e3 },
  { key: '7d', label: 'Last 7 days', ms: 7 * 24 * 3600e3 },
  { key: '30d', label: 'Last 30 days', ms: 30 * 24 * 3600e3 },
  { key: '90d', label: 'Last 90 days', ms: 90 * 24 * 3600e3 },
  { key: 'all', label: 'All history', ms: 0 },
];

const DNSSEC = ['secure', 'insecure', 'bogus', 'indeterminate'];
const MAX_EXCLUDE = 8; // the API's cap

export function emptyFilter(): QueryFilter {
  return {
    q: '',
    domain: '',
    exclude: [],
    clients: [],
    verdict: 'all',
    list: '',
    qtype: '',
    upstream: '',
    dnssec: '',
    range: 'all',
  };
}

// hasFilter: anything that turns the live tail into a search. The time
// range counts — "the last hour" is a question about history.
export function hasFilter(f: QueryFilter): boolean {
  return (
    f.q !== '' ||
    f.domain !== '' ||
    f.exclude.length > 0 ||
    f.clients.length > 0 ||
    f.verdict !== 'all' ||
    f.list !== '' ||
    f.qtype !== '' ||
    f.upstream !== '' ||
    f.dnssec !== '' ||
    f.range !== 'all'
  );
}

export function sinceMs(f: QueryFilter, now = Date.now()): number | undefined {
  const r = RANGES.find((r) => r.key === f.range);
  return r && r.ms ? now - r.ms : undefined;
}

// tokenize splits on whitespace, keeping "quoted values" (after a key:)
// whole. Returns each token with whether whitespace followed it — a token
// still being typed is not yet complete.
function tokenize(text: string): { tok: string; done: boolean }[] {
  const out: { tok: string; done: boolean }[] = [];
  let i = 0;
  while (i < text.length) {
    while (i < text.length && /\s/.test(text[i])) i++;
    if (i >= text.length) break;
    let tok = '';
    let quoted = false;
    while (i < text.length && (quoted || !/\s/.test(text[i]))) {
      if (text[i] === '"') quoted = !quoted;
      else tok += text[i];
      i++;
    }
    out.push({ tok, done: i < text.length });
  }
  return out;
}

// applyToken folds one recognised token into f and reports whether it was
// one; unrecognised tokens are free text.
function applyToken(f: QueryFilter, tok: string): boolean {
  const lower = tok.toLowerCase();
  switch (lower) {
    case 'blocked':
    case 'condemned':
      f.verdict = 'blocked';
      return true;
    case 'allowed':
    case 'passed':
      f.verdict = 'allowed';
      return true;
    case 'would-block':
    case 'would_block':
      f.verdict = 'would_block';
      return true;
  }
  if (tok.length > 1 && tok.startsWith('-')) {
    const x = tok.slice(1).toLowerCase();
    if (!f.exclude.includes(x) && f.exclude.length < MAX_EXCLUDE) f.exclude = [...f.exclude, x];
    return true;
  }
  const colon = tok.indexOf(':');
  if (colon <= 0) return false;
  const key = lower.slice(0, colon);
  const val = tok.slice(colon + 1).trim();
  if (!val) return false;
  switch (key) {
    case 'client':
    case 'device':
      f.clients = val.split(',').map((s) => s.trim()).filter(Boolean);
      return true;
    case 'domain':
      f.domain = val.toLowerCase().replace(/\.$/, '');
      return true;
    case 'type':
    case 'qtype':
      f.qtype = val.toUpperCase();
      return true;
    case 'list':
      f.list = val;
      return true;
    case 'upstream':
    case 'resolver':
      f.upstream = val;
      return true;
    case 'dnssec':
      if (!DNSSEC.includes(val.toLowerCase())) return false;
      f.dnssec = val.toLowerCase();
      return true;
  }
  // Not a key we know — "example.com:443" or an IPv6 address is free text.
  return false;
}

// parseInput folds the recognised tokens of the search box into a copy of
// base, returning the new filter and the text left in the box. With
// `complete` false (while typing), a token not yet followed by a space is
// left alone, so typing "type:A" on the way to "type:AAAA" does not
// become a chip early.
export function parseInput(
  text: string,
  base: QueryFilter,
  complete: boolean,
): { filter: QueryFilter; rest: string } {
  const f: QueryFilter = { ...base, exclude: [...base.exclude], clients: [...base.clients] };
  const rest: string[] = [];
  const free: string[] = [];
  for (const { tok, done } of tokenize(text)) {
    if ((done || complete) && applyToken(f, tok)) continue;
    rest.push(/\s/.test(tok) ? `"${tok}"` : tok);
    // A key still being typed ("type:AA", "-cd") is not a search for that
    // text; everything else searches as it is typed.
    if (!(done || complete) && IN_PROGRESS.test(tok)) continue;
    free.push(tok);
  }
  let restText = rest.join(' ');
  if (!complete && /\s$/.test(text) && restText) restText += ' ';
  f.q = free.join(' ');
  return { filter: f, rest: restText };
}

const IN_PROGRESS = /^(-|(client|device|domain|type|qtype|list|upstream|resolver|dnssec):)/i;

export interface Chip {
  key: string; // stable per filter field, for removal
  label: string;
  value: string;
  title: string;
}

const DNSSEC_NAMES: Record<string, string> = {
  secure: 'Verified',
  insecure: 'Unsigned',
  bogus: 'Failed',
  indeterminate: 'Not checkable',
};

// chips lists the active filters other than the free text (which stays in
// the box) and the time range (which has its own picker).
export function chips(f: QueryFilter, verdictLabel: (v: Verdict) => string): Chip[] {
  const out: Chip[] = [];
  if (f.verdict !== 'all')
    out.push({ key: 'verdict', label: 'Result', value: verdictLabel(f.verdict), title: '' });
  if (f.clients.length)
    out.push({
      key: 'clients',
      label: 'Device',
      value: f.clients.length === 1 ? f.clients[0] : `${f.clients[0]} +${f.clients.length - 1}`,
      title: f.clients.join(', '),
    });
  if (f.domain)
    out.push({ key: 'domain', label: 'Domain', value: f.domain, title: 'this exact name only, not its subdomains' });
  if (f.qtype) out.push({ key: 'qtype', label: 'Type', value: f.qtype, title: '' });
  if (f.list) out.push({ key: 'list', label: 'List', value: f.list, title: 'queries this list matched' });
  if (f.upstream)
    out.push({ key: 'upstream', label: 'Resolver', value: f.upstream, title: 'answered by this upstream resolver' });
  if (f.dnssec)
    out.push({ key: 'dnssec', label: 'DNSSEC', value: DNSSEC_NAMES[f.dnssec] ?? f.dnssec, title: '' });
  for (const x of f.exclude)
    out.push({ key: `exclude:${x}`, label: 'Not', value: x, title: `leaving out names containing "${x}"` });
  return out;
}

export function removeChip(f: QueryFilter, key: string): QueryFilter {
  const g: QueryFilter = { ...f };
  if (key.startsWith('exclude:')) {
    const x = key.slice('exclude:'.length);
    g.exclude = f.exclude.filter((e) => e !== x);
    return g;
  }
  switch (key) {
    case 'verdict':
      g.verdict = 'all';
      break;
    case 'clients':
      g.clients = [];
      break;
    case 'domain':
    case 'qtype':
    case 'list':
    case 'upstream':
    case 'dnssec':
      g[key] = '';
      break;
  }
  return g;
}

// toParams is the filter as URL parameters — the same names the history
// and export endpoints take, plus `range` for the hash. `since` is added by
// the caller at request time (it moves with the clock).
export function toParams(f: QueryFilter): URLSearchParams {
  const sp = new URLSearchParams();
  if (f.q) sp.set('q', f.q);
  if (f.domain) sp.set('domain', f.domain);
  if (f.exclude.length) sp.set('exclude', f.exclude.join(','));
  if (f.clients.length) sp.set('client', f.clients.join(','));
  if (f.verdict === 'would_block') sp.set('would_block', 'true');
  else if (f.verdict !== 'all') sp.set('verdict', f.verdict);
  if (f.list) sp.set('list', f.list);
  if (f.qtype) sp.set('qtype', f.qtype);
  if (f.upstream) sp.set('upstream', f.upstream);
  if (f.dnssec) sp.set('dnssec', f.dnssec);
  if (f.range !== 'all') sp.set('range', f.range);
  return sp;
}

// fromParams reads a deep link. It accepts what toParams writes and the
// older dashboard links (`qname` for the substring, `verdict=would_block`).
export function fromParams(p: Record<string, string>): QueryFilter {
  const f = emptyFilter();
  f.q = (p.q ?? p.qname ?? '').trim();
  if (p.domain) f.domain = p.domain.toLowerCase().replace(/\.$/, '');
  if (p.exclude) f.exclude = p.exclude.split(',').map((s) => s.trim()).filter(Boolean).slice(0, MAX_EXCLUDE);
  if (p.client) f.clients = p.client.split(',').map((s) => s.trim()).filter(Boolean);
  if (p.verdict === 'blocked' || p.verdict === 'allowed' || p.verdict === 'would_block') f.verdict = p.verdict;
  if (p.would_block === 'true') f.verdict = 'would_block';
  if (p.list) f.list = p.list;
  if (p.qtype) f.qtype = p.qtype.toUpperCase();
  if (p.upstream) f.upstream = p.upstream;
  if (p.dnssec && DNSSEC.includes(p.dnssec)) f.dnssec = p.dnssec;
  if (RANGES.some((r) => r.key === p.range)) f.range = p.range as RangeKey;
  return f;
}

// matches applies the filter to one entry client-side: new live entries
// joining a results view, and the ring fallback in ephemeral mode. It must
// agree with the server's QueryHistory.
export function matches(f: QueryFilter, e: LogEntry, now = Date.now()): boolean {
  if (f.verdict === 'would_block') {
    if (!e.audit_list) return false;
  } else if (f.verdict !== 'all' && e.verdict !== f.verdict) return false;
  if (f.clients.length && !f.clients.includes(e.client)) return false;
  if (f.list && e.list !== f.list && e.audit_list !== f.list) return false;
  if (f.dnssec && e.dnssec !== f.dnssec) return false;
  if (f.domain && e.qname !== f.domain) return false;
  if (f.qtype && e.qtype !== f.qtype) return false;
  if (f.upstream && e.upstream !== f.upstream) return false;
  const name = e.qname.toLowerCase();
  if (f.exclude.some((x) => name.includes(x))) return false;
  const since = sinceMs(f, now);
  if (since !== undefined && new Date(e.time).getTime() < since) return false;
  if (f.q) {
    const q = f.q.toLowerCase();
    return name.includes(q) || e.client.toLowerCase().includes(q);
  }
  return true;
}
