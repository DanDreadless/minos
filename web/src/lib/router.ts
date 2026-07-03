// Minimal hash router: '#/docket' → 'docket'. No dependency needed.
import { readable } from 'svelte/store';

export type Route = 'dashboard' | 'querylog' | 'lists' | 'domains' | 'settings';

function parse(): Route {
  switch (location.hash) {
    case '#/querylog':
      return 'querylog';
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

export const hrefFor: Record<Route, string> = {
  dashboard: '#/',
  querylog: '#/querylog',
  lists: '#/lists',
  domains: '#/domains',
  settings: '#/settings',
};
