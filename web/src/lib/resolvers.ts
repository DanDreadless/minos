// Curated catalog of known-good upstream resolvers for the Ferrymen picker.
//
// Every entry is DoH over an IP literal, never a hostname: a DNS server must
// not have to resolve its own resolver's name before it can forward anything
// (the same reasoning behind the default upstream). That only works when the
// provider's TLS certificate carries the IP as a SAN. Every address below
// except Quad9 was verified to complete a validated TLS handshake by IP;
// Quad9 is included on the maintainer's confirmation that it validates on
// their network (its IPs were unreachable from the build box, so it could
// not be checked there — a reachability block, not a cert failure).
// Providers whose certs don't cover their IPs (e.g. Mullvad →
// WRONG_PRINCIPAL) are deliberately omitted; a preset that fails validation
// would silently break resolution.
//
// To add a provider: confirm `curl https://<ip>/dns-query` validates its cert
// (curl exit 0, no SSL error) before listing it here.

import type { Upstream } from './api';

export interface ResolverPreset {
  id: string;
  label: string;
  upstream: Upstream;
}

export const resolverPresets: ResolverPreset[] = [
  { id: 'cloudflare', label: 'Cloudflare', upstream: { address: 'https://1.1.1.1/dns-query', protocol: 'doh' } },
  { id: 'cloudflare-security', label: 'Cloudflare — block malware', upstream: { address: 'https://1.1.1.2/dns-query', protocol: 'doh' } },
  { id: 'cloudflare-family', label: 'Cloudflare — block malware & adult', upstream: { address: 'https://1.1.1.3/dns-query', protocol: 'doh' } },
  { id: 'quad9', label: 'Quad9 — block malware', upstream: { address: 'https://9.9.9.9/dns-query', protocol: 'doh' } },
  { id: 'quad9-unfiltered', label: 'Quad9 — unfiltered', upstream: { address: 'https://9.9.9.10/dns-query', protocol: 'doh' } },
  { id: 'google', label: 'Google Public DNS', upstream: { address: 'https://8.8.8.8/dns-query', protocol: 'doh' } },
  { id: 'adguard', label: 'AdGuard DNS — block ads', upstream: { address: 'https://94.140.14.14/dns-query', protocol: 'doh' } },
  { id: 'adguard-unfiltered', label: 'AdGuard DNS — unfiltered', upstream: { address: 'https://94.140.14.140/dns-query', protocol: 'doh' } },
  { id: 'opendns', label: 'OpenDNS', upstream: { address: 'https://208.67.222.222/dns-query', protocol: 'doh' } },
];

// matchPreset returns the id of the preset that exactly matches u, or '' when
// the row is a custom/hand-entered resolver (including any DoT/plaintext one).
export function matchPreset(u: Upstream): string {
  const hit = resolverPresets.find(
    (p) => p.upstream.protocol === u.protocol && p.upstream.address === u.address,
  );
  return hit ? hit.id : '';
}
