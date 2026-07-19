// Minimal hash router: '#/docket' → 'docket', with an optional query string
// ('#/querylog?client=1.2.3.4&verdict=blocked') for deep links. No dependency.
import { readable } from 'svelte/store';

export type Route =
  | 'dashboard'
  | 'querylog'
  | 'devices'
  | 'device'
  | 'lists'
  | 'domains'
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
    case '#/domains':
      return 'domains';
    case '#/settings':
      return 'settings';
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

// docketHref builds a deep link into the Docket pre-filtered by verdict, an
// exact client (or a device's set of IPs), a list name, and/or a domain
// substring — the target of the dashboard and devices drill-downs.
// `client`/`clients` and `list` match whole values; `qname` is a substring.
export function docketHref(params: {
  verdict?: string;
  client?: string;
  clients?: string[];
  qname?: string;
  list?: string;
}): string {
  const sp = new URLSearchParams();
  if (params.verdict) sp.set('verdict', params.verdict);
  const ips = params.clients ?? (params.client ? [params.client] : []);
  if (ips.length) sp.set('client', ips.join(','));
  if (params.qname) sp.set('qname', params.qname);
  if (params.list) sp.set('list', params.list);
  const qs = sp.toString();
  return qs ? `#/querylog?${qs}` : '#/querylog';
}

export const hrefFor: Record<Route, string> = {
  dashboard: '#/',
  querylog: '#/querylog',
  devices: '#/devices',
  device: '#/devices', // detail pages aren't nav destinations; fall back to the list
  lists: '#/lists',
  domains: '#/domains',
  settings: '#/settings',
};

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
