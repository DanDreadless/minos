// Minimal hash router: '#/docket' → 'docket', with an optional query string
// ('#/querylog?client=1.2.3.4&verdict=blocked') for deep links. No dependency.
import { readable } from 'svelte/store';

export type Route = 'dashboard' | 'querylog' | 'devices' | 'lists' | 'domains' | 'settings';

// The hash without its query string, e.g. '#/querylog?client=x' → '#/querylog'.
function hashPath(): string {
  const h = location.hash || '#/';
  const q = h.indexOf('?');
  return q === -1 ? h : h.slice(0, q);
}

function parse(): Route {
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
// exact client (or a device's set of IPs), and/or a domain substring — the
// target of the dashboard and devices drill-downs. `client`/`clients` match
// whole addresses; `qname` is a substring.
export function docketHref(params: {
  verdict?: string;
  client?: string;
  clients?: string[];
  qname?: string;
}): string {
  const sp = new URLSearchParams();
  if (params.verdict) sp.set('verdict', params.verdict);
  const ips = params.clients ?? (params.client ? [params.client] : []);
  if (ips.length) sp.set('client', ips.join(','));
  if (params.qname) sp.set('qname', params.qname);
  const qs = sp.toString();
  return qs ? `#/querylog?${qs}` : '#/querylog';
}

export const hrefFor: Record<Route, string> = {
  dashboard: '#/',
  querylog: '#/querylog',
  devices: '#/devices',
  lists: '#/lists',
  domains: '#/domains',
  settings: '#/settings',
};
