// Minimal hash router: '#/docket' → 'docket', with an optional query string
// ('#/querylog?client=1.2.3.4&verdict=blocked') for deep links. No dependency.
import { readable } from 'svelte/store';

export type Route =
  | 'dashboard'
  | 'querylog'
  | 'devices'
  | 'device'
  | 'lists'
  | 'rules'
  | 'services'
  | 'privacy'
  | 'logs'
  | 'help'
  | 'settings';

// The hash without its query string, e.g. '#/querylog?client=x' → '#/querylog'.
function hashPath(): string {
  const h = location.hash || '#/';
  const q = h.indexOf('?');
  return q === -1 ? h : h.slice(0, q);
}

function parse(): Route {
  // Detail pages carry their key in the path: '#/device/<mac-or-ip>'.
  if (hashPath().startsWith('#/device/')) return 'device';
  switch (hashPath()) {
    case '#/querylog':
      return 'querylog';
    case '#/devices':
      return 'devices';
    case '#/lists':
      return 'lists';
    case '#/rules':
    case '#/allowed': // the old Allowed and Blocked tabs, and Allow & Block
    case '#/blocked':
    case '#/domains':
      return 'rules';
    case '#/services':
      return 'services';
    case '#/settings':
      return 'settings';
    case '#/logs':
      return 'logs';
    case '#/local': // Local DNS moved into Settings → DNS (the default tab)
      return 'settings';
    case '#/privacy':
      return 'privacy';
    case '#/help':
      return 'help';
    default:
      return 'dashboard';
  }
}

export const route = readable<Route>(parse(), (set) => {
  const onChange = () => set(parse());
  window.addEventListener('hashchange', onChange);
  return () => window.removeEventListener('hashchange', onChange);
});

// currentParams reads the hash query string live at call time — used by the
// Docket on mount to pick up a deep link. (A readable store would only track
// changes while it had a persistent subscriber, which the one-shot read on
// mount does not provide.)
export function currentParams(): Record<string, string> {
  const q = location.hash.indexOf('?');
  if (q === -1) return {};
  return Object.fromEntries(new URLSearchParams(location.hash.slice(q + 1)).entries());
}

// docketHref builds a deep link into the Query Log pre-filtered by verdict,
// an exact client (or a device's set of IPs), a list name, a domain, and/or
// a time range — the target of the dashboard and devices drill-downs.
// `client`/`clients`, `list` and `domain` match whole values; `qname` is a
// substring. `range` (a querysyntax RangeKey) should match the window the
// clicked count came from, so the rows shown add up to it.
export function docketHref(params: {
  verdict?: string;
  client?: string;
  clients?: string[];
  qname?: string;
  domain?: string;
  list?: string;
  dnssec?: string;
  range?: string;
}): string {
  const sp = new URLSearchParams();
  if (params.verdict) sp.set('verdict', params.verdict);
  const ips = params.clients ?? (params.client ? [params.client] : []);
  if (ips.length) sp.set('client', ips.join(','));
  if (params.qname) sp.set('qname', params.qname);
  if (params.domain) sp.set('domain', params.domain);
  if (params.list) sp.set('list', params.list);
  if (params.dnssec) sp.set('dnssec', params.dnssec);
  if (params.range) sp.set('range', params.range);
  const qs = sp.toString();
  return qs ? `#/querylog?${qs}` : '#/querylog';
}

export const hrefFor: Record<Route, string> = {
  dashboard: '#/',
  querylog: '#/querylog',
  devices: '#/devices',
  device: '#/devices', // detail pages aren't nav destinations; fall back to the list
  lists: '#/lists',
  rules: '#/rules',
  services: '#/services',
  settings: '#/settings',
  logs: '#/logs',
  privacy: '#/privacy',
  help: '#/help',
};

// rulesHref links to the Rules tab showing one scope's rules: "everyone",
// "group:<name>" or "device:<key>".
export function rulesHref(scope: string): string {
  return `#/rules?for=${encodeURIComponent(scope)}`;
}

// servicesHref links to the Services tab set to one scope.
export function servicesHref(scope: string): string {
  return `#/services?for=${encodeURIComponent(scope)}`;
}

// deviceHref links to a device's detail page. key is the device's MAC when
// it has one (stable across DHCP leases), else its IP.
export function deviceHref(key: string): string {
  return `#/device/${encodeURIComponent(key)}`;
}

// currentDeviceKey reads the '#/device/<key>' path segment at call time
// (one-shot on mount, like currentParams), or '' when absent.
export function currentDeviceKey(): string {
  const path = hashPath();
  if (!path.startsWith('#/device/')) return '';
  return decodeURIComponent(path.slice('#/device/'.length));
}

// A page with unsaved changes (Settings) sets the leave guard; App then asks
// before an in-app link navigates away. Reloads are covered by the page's
// own beforeunload handler.
let leaveGuard = false;
export function setLeaveGuard(on: boolean): void {
  leaveGuard = on;
}
export function leaveGuarded(): boolean {
  return leaveGuard;
}
