// The user guide: one section per page, what it is for, how to use it, and
// every setting with its default, effect, config key and whether it applies
// live. This file is the single source — the in-app Help page renders it,
// and `npm run build` generates docs/user-guide.md from it, so the two can
// never drift. internal/config's TestEverySettingIsDocumented fails when a
// config key is missing here (or documented here but gone), so adding a
// setting means documenting it.
//
// Plain words throughout, matching the labels in copy.ts.

// live: changed from the UI or API, applies at once with no restart.
// file: set in minos.yaml only; restart Minos after editing it.
export type Applies = 'live' | 'file';

export interface Field {
  key: string;
  effect: string;
}

export interface Setting {
  label: string;
  key: string;
  default?: string;
  effect: string;
  applies: Applies;
  where: string; // where to change it
  fields?: Field[]; // sub-keys of a list or block setting
}

export interface Section {
  id: string;
  title: string;
  page?: string; // the page this documents
  summary: string;
  howto?: string[];
  tasks?: { q: string; a: string }[];
  settings?: Setting[];
}

export const docs: Section[] = [
  {
    id: 'start',
    title: 'Getting started',
    summary:
      'Minos is a DNS server for your home network that blocks ads, trackers and malware before they load. Every device that uses it as its DNS server has each lookup checked against your blocklists and your own rules: blocked names get an empty answer, everything else is looked up through your upstream resolvers.',
    howto: [
      "Point devices at Minos: set your router's DHCP DNS option to this machine's address, so every device follows, or set the DNS server on individual devices.",
      'Choose blocklists on Blocking → Blocklists — the catalog has curated picks; Balanced is the recommended start.',
      'Watch the Query Log for a day. If something you use breaks, find its blocked lookup and press Allow.',
      'Optional: set an API token (Settings → Access & security), notifications, and the update check.',
    ],
    tasks: [
      {
        q: 'What is at the bottom of the side menu?',
        a: 'Whether Minos is blocking right now ("Blocking · 73k rules", "Blocking paused until 14:20", or in red "Not blocking — no blocklist rules loaded"), Pause blocking, and Check a domain (also Ctrl+K / ⌘K on any page): paste a name or URL to see whether it is blocked and why, then allow or block it. On a phone, a paused or not-blocking state also shows in the top bar.',
      },
      {
        q: 'Something broke and I think Minos blocked it.',
        a: 'Open the Query Log, filter by the device or type the site\'s name, and look for Blocked rows around the time it broke. Press Allow on the row (or use Check a domain, Ctrl+K). Allowed domains include their subdomains.',
      },
      {
        q: 'How do I turn blocking off for a moment?',
        a: 'Pause blocking at the bottom of the side menu: 5 or 30 minutes, an hour, until you resume, or any duration such as 2h. Device-level blocks (Devices → Block DNS) still apply while paused.',
      },
    ],
  },
  {
    id: 'dashboard',
    title: 'Dashboard',
    page: '#/',
    summary:
      'What your network did over a window you choose: queries, blocked, block rate, a volume chart, the most-blocked domains and the busiest devices — every number for the same window.',
    howto: [
      'Pick the window at the top (24 hours, 7, 30 or 90 days). It is remembered in this browser.',
      'Click any number, domain or device to open exactly those queries in the Query Log.',
      'Needs attention appears only when something does: no rules loaded, a list failing or retired, a resolver not answering, no API token, a nearly full disk, DNSSEC unable to check answers, or new warnings in Logs. Each line links to its fix.',
      'The line under the tiles gives totals since Minos last started; the cache hit rate is also since the last start.',
      'The Host card (not shown in containers) reports the machine Minos runs on; the disk figure is the filesystem holding the query log.',
      'The DNSSEC card appears when validation is on: in permissive mode it lists answers that enforce mode would have refused.',
    ],
  },
  {
    id: 'querylog',
    title: 'Query Log',
    page: '#/querylog',
    summary:
      'Every DNS lookup your devices make — streaming live, and searchable across the whole kept history — with the list and rule behind every block.',
    howto: [
      'With no filter the page is Live: new lookups arrive at the top. Pause holds the rows still while you read; new ones wait, counted.',
      'Any filter switches to Results: matches from the full history, newest first, with Load older.',
      'Filter with the result buttons (All, Blocked, Allowed, Would block), the list menu and the time range — or click any device, domain, type, list or resolver in a row.',
      'The search box takes filters as you type: blocked, allowed, would-block, client:192.168.1.5, domain:example.com (that exact name), type:AAAA, list:StevenBlack (quote names with spaces), upstream:1.1.1.1:853, dnssec:bogus, and -cdn to leave out names containing "cdn". Anything else matches domains and device addresses. Press / to search, Enter to apply, Esc to clear, Backspace to remove the last filter.',
      'Row actions: Allow (on blocked rows) or Block (on allowed rows), Check (how the domain would be judged now) and Copy.',
      'Export CSV or JSON downloads every match of the current filter, up to 100,000 rows.',
      'The address bar keeps the filter, so a search can be bookmarked or shared.',
    ],
    settings: [
      {
        label: 'Keep history (days)',
        key: 'querylog.retention_days',
        default: '90',
        effect: 'How long lookups are kept on disk. Older rows are removed once a day.',
        applies: 'live',
        where: 'Settings → System',
      },
      {
        label: 'Recent queries kept in memory',
        key: 'querylog.ring_size',
        default: '10000',
        effect: 'How many of the newest lookups are held in memory for the live view. History on disk is unaffected.',
        applies: 'live',
        where: 'Settings → System',
      },
      {
        label: 'Query log memory limit (MB)',
        key: 'querylog.memory_mb',
        default: '32',
        effect:
          'The most RAM the query-log database uses for caching and for working out searches and statistics (8–4096). Bigger jobs spill to a temporary file instead of using more memory.',
        applies: 'live',
        where: 'Settings → System',
      },
      {
        label: 'Query log file',
        key: 'querylog.db_path',
        default: 'minos.db (installers put it in /var/lib/minos)',
        effect: 'Where history is stored.',
        applies: 'file',
        where: 'minos.yaml',
      },
      {
        label: 'Memory-only mode',
        key: 'querylog.ephemeral',
        default: 'false',
        effect:
          'Keeps no history on disk at all (and keeps the blocklist cache in memory): the Query Log and Dashboard only cover what is in memory since the last start.',
        applies: 'file',
        where: 'minos.yaml',
      },
    ],
  },
  {
    id: 'devices',
    title: 'Devices',
    page: '#/devices',
    summary:
      'Every device that has used Minos, named where Minos could find out (router names, mDNS, NetBIOS, UPnP, DHCP), with groups that give devices different rules and schedules.',
    howto: [
      'Give a device a label, put it in a group, or Block DNS to refuse all of its lookups (this applies even while blocking is paused).',
      'Open a device for its page: every address it has used, what it looked up most, its full query history and notes.',
      'A device is followed by its MAC address when Minos can see one, so its rules survive a new IP address from DHCP.',
      'Groups: filter (your normal rules plus the group\'s own allowed/blocked domains and services), bypass (no filtering) or block (no DNS). A group can have a schedule — it applies only during those hours, and devices follow the normal rules outside them.',
    ],
    settings: [
      {
        label: 'Devices',
        key: 'clients',
        effect: 'Saved labels, groups, blocks and notes for devices.',
        applies: 'live',
        where: 'Devices',
        fields: [
          { key: 'clients.ip', effect: 'The device\'s address (its last known one when a MAC is set).' },
          { key: 'clients.mac', effect: 'When set, the device is recognised by MAC on any address.' },
          { key: 'clients.name', effect: 'Your label for it.' },
          { key: 'clients.group', effect: 'The group it belongs to (empty: the normal rules).' },
          { key: 'clients.blocked', effect: 'Refuse all of its DNS lookups.' },
          { key: 'clients.notes', effect: 'Free text, up to 4096 characters.' },
        ],
      },
      {
        label: 'Groups',
        key: 'groups',
        effect: 'Named policies for sets of devices.',
        applies: 'live',
        where: 'Devices → Groups',
        fields: [
          { key: 'groups.name', effect: 'The group\'s name.' },
          { key: 'groups.mode', effect: 'filter, bypass or block.' },
          { key: 'groups.allowlist', effect: 'Extra allowed domains for members.' },
          { key: 'groups.denylist', effect: 'Extra blocked domains for members.' },
          { key: 'groups.services', effect: 'Services blocked for members.' },
          { key: 'groups.allowed_services', effect: 'Services allowed for members.' },
          { key: 'groups.custom_services', effect: 'Your custom services blocked for members.' },
          { key: 'groups.allowed_custom_services', effect: 'Your custom services allowed for members.' },
          { key: 'groups.safe_search', effect: 'Enforce Safe Search for members.' },
          { key: 'groups.schedule', effect: 'When the group applies; outside it, members follow the normal rules.' },
          { key: 'groups.schedule.days', effect: 'Days as mon, tue … sun (empty: every day).' },
          { key: 'groups.schedule.start', effect: 'Start time, HH:MM server time.' },
          { key: 'groups.schedule.end', effect: 'End time; before the start means it runs past midnight.' },
        ],
      },
    ],
  },
  {
    id: 'blocklists',
    title: 'Blocking → Blocklists',
    page: '#/lists',
    summary:
      'The lists Minos blocks from, downloaded and kept up to date automatically. If a list\'s server is down, Minos keeps blocking with the last good copy (also after a restart).',
    howto: [
      'Choose your blocklists: the catalog offers verified lists in tiers — Balanced (recommended), Strict (more blocking, more to allow), Security (malware and phishing) and Family (content you choose to keep off the network). One click subscribes.',
      'Each list shows a plain status — up to date, failing but still blocking with the copy from a given time, retired, and so on. Hover for the reason.',
      'Audit: tick it to try a list without enforcing it — its matches are recorded as "would block" in the Query Log. Untick to enforce.',
      'Allowlists (subscribed lists of always-allowed domains) can be added too; they beat every blocklist.',
      '"Replace with…" appears on a list its publisher has retired; "Add mirrors" on catalog lists subscribed before mirrors existed.',
    ],
    settings: [
      {
        label: 'Check for updates every',
        key: 'lists.refresh_interval',
        default: '24h',
        effect: 'How often every list is downloaded again (minimum 5m). A list that fails is retried sooner, backing off from a minute to an hour.',
        applies: 'live',
        where: 'Blocking → Blocklists',
      },
      {
        label: 'Blocklists',
        key: 'lists.sources',
        effect: 'Subscribed blocklists.',
        applies: 'live',
        where: 'Blocking → Blocklists',
        fields: [
          { key: 'lists.sources.name', effect: 'A unique name, shown in the Query Log.' },
          { key: 'lists.sources.url', effect: 'Where it is downloaded from.' },
          { key: 'lists.sources.format', effect: 'hosts, adblock or plain (one domain per line).' },
          { key: 'lists.sources.enabled', effect: 'Off keeps the subscription but stops using it.' },
          { key: 'lists.sources.audit', effect: 'Record matches as "would block" without blocking.' },
          { key: 'lists.sources.mirrors', effect: 'Up to 5 fallback addresses, used only when the main one is gone or the copy is getting old.' },
          {
            key: 'lists.sources.headers',
            effect: 'Extra request headers, e.g. an access key for a keyed feed. Config file only; never shown by the API and never sent to a mirror.',
          },
        ],
      },
      {
        label: 'Allowlists',
        key: 'lists.allow_sources',
        effect: 'Subscribed lists of always-allowed domains; same fields as blocklists.',
        applies: 'live',
        where: 'Blocking → Blocklists',
        fields: [
          { key: 'lists.allow_sources.name', effect: 'A unique name.' },
          { key: 'lists.allow_sources.url', effect: 'Where it is downloaded from.' },
          { key: 'lists.allow_sources.format', effect: 'hosts, adblock or plain.' },
          { key: 'lists.allow_sources.enabled', effect: 'Off stops using it.' },
          { key: 'lists.allow_sources.audit', effect: 'Not allowed on allowlists (a validation error).' },
          { key: 'lists.allow_sources.mirrors', effect: 'Fallback addresses.' },
          { key: 'lists.allow_sources.headers', effect: 'Extra request headers (config file only).' },
        ],
      },
    ],
  },
  {
    id: 'allowblock',
    title: 'Blocking → Allowed and Blocked',
    page: '#/allowed',
    summary:
      'Your own rules, which beat the blocklists: domains you always allow or always block, and whole services (every domain an app uses) in one move.',
    howto: [
      'Allowed and Blocked are separate tabs; each has your domains (as chips — × removes one) and the services for everyone or for one group.',
      'Allowed and blocked domains include their subdomains. An allowed domain beats any blocked domain or blocklist.',
      'Services: block or allow a whole service — for everyone, or for one group (choose it under "Services for"). Allowing a streaming service also allows the playback and sign-in hosts it needs.',
      'Your custom services: define your own bundle of domains, then block or allow it like any service.',
    ],
    settings: [
      {
        label: 'Allowed domains',
        key: 'blocking.allowlist',
        effect:
          'Always allowed, with subdomains. An entry written /pattern/ is a regex rule (case-insensitive, matched anywhere in the name): an allow regex beats any block.',
        applies: 'live',
        where: 'Blocking → Allowed',
      },
      { label: 'Blocked domains', key: 'blocking.denylist', effect: 'Always blocked, with subdomains. An entry written /pattern/ is a regex rule, e.g. /^ad[0-9]*\\./ — checked after the exact rules, so an allowed domain still wins. Regexes use RE2 (no backreferences or lookaround); up to 1000 per list.', applies: 'live', where: 'Blocking → Blocked' },
      { label: 'Blocked services', key: 'blocking.services', effect: 'Services blocked for everyone.', applies: 'live', where: 'Blocking → Allowed / Blocked → Services' },
      { label: 'Allowed services', key: 'blocking.allowed_services', effect: 'Services allowed for everyone; allowing beats blocking.', applies: 'live', where: 'Blocking → Allowed / Blocked → Services' },
      {
        label: 'Your custom services',
        key: 'blocking.custom_services',
        effect: 'Your own service bundles.',
        applies: 'live',
        where: 'Blocking → Allowed / Blocked → Your custom services',
        fields: [
          { key: 'blocking.custom_services.name', effect: 'Its ID (derived from the label if not given).' },
          { key: 'blocking.custom_services.label', effect: 'Its display name.' },
          { key: 'blocking.custom_services.domains', effect: 'The domains it covers.' },
          { key: 'blocking.custom_services.allow_extra', effect: 'Extra hosts allowed only while the service is allowed.' },
          { key: 'blocking.custom_services.blocked', effect: 'Blocked for everyone.' },
          { key: 'blocking.custom_services.allowed', effect: 'Allowed for everyone.' },
        ],
      },
    ],
  },
  {
    id: 'localdns',
    title: 'Local DNS',
    page: '#/local',
    summary:
      'Names on your own network: records Minos answers itself, and domains it sends to another resolver — usually your router, so device names from DHCP keep working.',
    howto: [
      'Local records: A (IPv4), AAAA (IPv6) or CNAME (alias). A leading *. matches all subdomains. Address records also answer reverse (PTR) lookups. Local records beat the blocklists and never leave your network.',
      'Conditional forwarding: send domains such as lan, home.arpa or your reverse zone (1.168.192.in-addr.arpa) to the router. A route is final: if its resolver fails, the lookup fails rather than going upstream.',
      'Lookups for private address ranges that no route covers are answered "not found" locally rather than leaking upstream.',
    ],
    settings: [
      {
        label: 'Local records',
        key: 'dns.local_records',
        effect: 'Names Minos answers itself.',
        applies: 'live',
        where: 'Local DNS',
        fields: [
          { key: 'dns.local_records.name', effect: 'The name, e.g. nas.home.lab or *.home.lab.' },
          { key: 'dns.local_records.a', effect: 'IPv4 addresses.' },
          { key: 'dns.local_records.aaaa', effect: 'IPv6 addresses.' },
          { key: 'dns.local_records.cname', effect: 'An alias target (instead of addresses).' },
        ],
      },
      {
        label: 'Local record TTL',
        key: 'dns.local_ttl',
        default: '300',
        effect: 'How long devices cache a local answer (seconds).',
        applies: 'live',
        where: 'minos.yaml or the API',
      },
      {
        label: 'Conditional forwarding',
        key: 'dns.routes',
        effect: 'Domains sent to a different resolver.',
        applies: 'live',
        where: 'Local DNS',
        fields: [
          { key: 'dns.routes.domains', effect: 'Domains it covers, with their subdomains.' },
          { key: 'dns.routes.upstream', effect: 'The resolver to use.' },
          { key: 'dns.routes.upstream.address', effect: 'host:port, or a URL for DoH.' },
          { key: 'dns.routes.upstream.protocol', effect: 'udp, tcp, dot, doh, doh3 or doq.' },
        ],
      },
      {
        label: 'Forward private reverse lookups',
        key: 'dns.forward_private_reverse',
        default: 'false',
        effect: 'Send reverse lookups for private addresses upstream instead of answering them locally.',
        applies: 'file',
        where: 'minos.yaml',
      },
    ],
  },
  {
    id: 'logs',
    title: 'Logs',
    page: '#/logs',
    summary:
      "Minos's own activity — list downloads, resolver trouble, certificate renewals, errors — live. A red dot on Logs in the menu means a warning or error you haven't seen.",
    howto: [
      'Filter by level and search messages and their details; Pause holds the view; Copy copies a line; Download saves everything held as a text file for a bug report.',
      'The page holds the newest 2,000 messages and starts afresh when Minos restarts; the full history is in the system journal (journalctl -u minos) or docker logs.',
      'Tokens, keys and passwords never appear — not here and not in the journal.',
      'From a shell: minos logs -f -level warn.',
    ],
    settings: [
      {
        label: 'Application log level',
        key: 'log_level',
        default: 'info (or the -log-level flag)',
        effect: 'Which messages Minos records, here and in the system journal: debug, info, warn or error. Debug records every query — use it briefly.',
        applies: 'live',
        where: 'Settings → System, or Record on the Logs page',
      },
    ],
  },
  {
    id: 'settings-dns',
    title: 'Settings → DNS',
    page: '#/settings?tab=dns',
    summary: 'Where Minos looks up allowed names, whether it checks DNSSEC, and its answer cache.',
    howto: [
      'Upstream resolvers are tried in order: the first is the primary. The light beside each is green when it is answering, red when failing, hollow when it has not been needed yet.',
      'Encrypted protocols (DoH, DoT) keep your lookups private from your internet provider; the presets are verified to work.',
      'Changes on every Settings tab are drafts until you press Save changes in the bar at the bottom.',
    ],
    settings: [
      {
        label: 'Upstream resolvers',
        key: 'dns.upstreams',
        default: 'Cloudflare: https://1.1.1.1/dns-query (DoH), then 1.0.0.1:853 (DoT)',
        effect: 'Where allowed lookups are sent, in order. A resolver that fails three times in a row is skipped for 30 seconds, then retried.',
        applies: 'live',
        where: 'Settings → DNS',
        fields: [
          { key: 'dns.upstreams.address', effect: 'host:port for udp, tcp, dot and doq; an https:// URL for doh and doh3.' },
          { key: 'dns.upstreams.protocol', effect: 'udp, tcp, dot (DNS over TLS), doh (DNS over HTTPS), doh3 (DNS over HTTPS on HTTP/3) or doq (DNS over QUIC).' },
        ],
      },
      {
        label: 'DNSSEC validation',
        key: 'dns.dnssec',
        default: 'off',
        effect:
          'off trusts the resolvers; permissive checks signed answers and records failures without refusing anything; enforce refuses answers that fail. Start with permissive.',
        applies: 'live',
        where: 'Settings → DNS',
      },
      {
        label: 'Response cache',
        key: 'dns.cache',
        effect: 'Answers repeat lookups from memory instead of asking upstream. Blocked answers are never cached.',
        applies: 'live',
        where: 'Settings → DNS',
        fields: [
          { key: 'dns.cache.enabled', effect: 'On by default.' },
          { key: 'dns.cache.max_entries', effect: 'How many answers to keep (default 10000).' },
          { key: 'dns.cache.min_ttl', effect: 'Keep short-lived answers at least this long, in seconds (default 10).' },
          { key: 'dns.cache.max_ttl', effect: 'Never serve from cache longer than this, in seconds (default 3600).' },
          { key: 'dns.cache.serve_stale', effect: 'If upstream is slow or down, answer from an expired entry (up to 6 hours old) while fetching a fresh one (default on).' },
        ],
      },
      {
        label: 'DNS listen address',
        key: 'dns.listen',
        default: ':53',
        effect: 'Where Minos accepts DNS lookups.',
        applies: 'file',
        where: 'minos.yaml',
      },
      {
        label: 'Encrypted DNS for your devices (DoT/DoH)',
        key: 'dns.tls',
        effect:
          'Lets devices reach Minos itself over DNS over TLS and DNS over HTTPS. Needs a certificate: files, or one issued automatically (ACME, DNS-01). With either on, Minos also advertises them over DDR, so Windows 11, iOS and macOS can switch to encrypted DNS by themselves — when the certificate covers the address they use for Minos.',
        applies: 'file',
        where: 'minos.yaml',
        fields: [
          { key: 'dns.tls.dot_listen', effect: 'Address for DNS over TLS, usually :853 (empty: off).' },
          { key: 'dns.tls.doh_listen', effect: 'Address for DNS over HTTPS at /dns-query, usually :443 (empty: off). HTTP/3 is served on the same port over UDP and advertised, so clients that speak it upgrade.' },
          { key: 'dns.tls.doq_listen', effect: 'Address for DNS over QUIC (RFC 9250), usually :853 — UDP, so it can share the number with DoT (empty: off).' },
          { key: 'dns.tls.cert_file', effect: 'Certificate file (instead of acme).' },
          { key: 'dns.tls.key_file', effect: 'Its private key.' },
          { key: 'dns.tls.acme', effect: 'Issue and renew a certificate automatically.' },
          { key: 'dns.tls.acme.email', effect: 'Contact address for the certificate authority.' },
          { key: 'dns.tls.acme.domain', effect: 'The name the certificate is for.' },
          { key: 'dns.tls.acme.provider', effect: 'cloudflare, desec, duckdns or rfc2136 — who publishes the DNS-01 record.' },
          { key: 'dns.tls.acme.api_token', effect: 'API token for cloudflare, desec or duckdns.' },
          { key: 'dns.tls.acme.server', effect: 'RFC 2136 server, host:port.' },
          { key: 'dns.tls.acme.tsig_name', effect: 'RFC 2136 TSIG key name.' },
          { key: 'dns.tls.acme.tsig_secret', effect: 'RFC 2136 TSIG secret.' },
          { key: 'dns.tls.acme.tsig_algorithm', effect: 'RFC 2136 TSIG algorithm (default hmac-sha256).' },
          { key: 'dns.tls.acme.directory_url', effect: 'ACME directory (default Let\'s Encrypt).' },
          { key: 'dns.tls.acme.cache_dir', effect: 'Where issued certificates are kept.' },
        ],
      },
    ],
  },
  {
    id: 'settings-filtering',
    title: 'Settings → Filtering',
    page: '#/settings?tab=filtering',
    summary: 'How blocked lookups are answered, and the protections against devices working around Minos.',
    settings: [
      {
        label: 'Block response',
        key: 'blocking.mode',
        default: 'zero_ip',
        effect: 'zero_ip answers 0.0.0.0 / :: (fails fast and quietly — recommended); nxdomain says the domain does not exist.',
        applies: 'live',
        where: 'Settings → Filtering',
      },
      {
        label: 'Remember blocks for (seconds)',
        key: 'dns.block_ttl',
        default: '60',
        effect: 'How long a device caches a blocked answer before asking again. Lower makes an Allow take effect sooner.',
        applies: 'live',
        where: 'Settings → Filtering',
      },
      {
        label: 'Enforce Safe Search for everyone',
        key: 'blocking.safe_search',
        default: 'off',
        effect: 'Google, Bing, DuckDuckGo and YouTube (moderate) answer with their safe versions. Bypass groups are exempt.',
        applies: 'live',
        where: 'Settings → Filtering',
      },
      {
        label: 'Keep Firefox on this resolver',
        key: 'dns.allow_firefox_doh',
        default: 'false (Firefox is kept on Minos)',
        effect: 'When false, Minos tells Firefox this network filters DNS, so Firefox does not switch itself to its own encrypted DNS. Untick "Keep Firefox on this resolver" to set it true.',
        applies: 'live',
        where: 'Settings → Filtering',
      },
      {
        label: 'Block trackers hidden behind CNAMEs',
        key: 'blocking.cname_inspection',
        default: 'on',
        effect:
          'Some trackers hide behind a name that looks first-party (metrics.shop.example) but is an alias (CNAME) for a tracker. Minos checks every alias in an answer against your rules and blocks the answer when one is blocked; the Query Log shows "via CNAME <tracker>". Names you have allowed, and bypass devices, are not checked.',
        applies: 'live',
        where: 'Settings → Filtering',
      },
      {
        label: 'Protect against DNS rebinding',
        key: 'dns.rebind_protection',
        default: 'off',
        effect:
          'Refuses answers that point a public name at an address on your own network (192.168.x.x, 10.x.x.x, fd00::/8, loopback) — the trick a malicious web page uses to reach your router or NAS. Allowed domains (e.g. plex.direct for Plex), local records, conditional forwarding and names like .lan or .home.arpa are exempt. Refusals show in the Query Log under the list "rebinding".',
        applies: 'live',
        where: 'Settings → Filtering',
      },
      {
        label: 'Block iCloud Private Relay',
        key: 'blocking.block_icloud_private_relay',
        default: 'off',
        effect: 'Apple devices fall back to normal DNS so Minos can filter them. A real privacy trade-off.',
        applies: 'live',
        where: 'Settings → Filtering',
      },
    ],
  },
  {
    id: 'settings-notifications',
    title: 'Settings → Notifications',
    page: '#/settings?tab=notifications',
    summary:
      'Messages to your phone (ntfy) or any webhook when something on your network changes. Each says what happened and what to do, and tapping it opens the right page in Minos.',
    howto: [
      'Set an ntfy topic URL (e.g. https://ntfy.sh/my-minos-topic) or a webhook, save, then Send test notification.',
      '"DNS is down" — every resolver failing — is always sent, at urgent priority, even in quiet hours.',
    ],
    settings: [
      { label: 'Webhook URL', key: 'notifications.webhook_url', effect: 'Each event is POSTed here as JSON.', applies: 'live', where: 'Settings → Notifications' },
      { label: 'ntfy topic URL', key: 'notifications.ntfy_url', effect: 'An ntfy topic — ntfy.sh or self-hosted.', applies: 'live', where: 'Settings → Notifications' },
      { label: 'ntfy access token', key: 'notifications.ntfy_token', effect: 'Only for protected topics. Never shown again once saved.', applies: 'live', where: 'Settings → Notifications' },
      {
        label: 'Link address',
        key: 'notifications.ui_url',
        default: "this machine's address and the API port",
        effect: 'Where tapping a notification takes you. Set it if you reach Minos by another name.',
        applies: 'live',
        where: 'Settings → Notifications',
      },
      { label: 'What to send', key: 'notifications.mute', effect: 'Event types switched off.', applies: 'live', where: 'Settings → Notifications' },
      {
        label: 'Quiet hours',
        key: 'notifications.quiet_hours',
        default: 'none',
        effect: 'e.g. 22:00-07:00: notifications arrive silently during these hours.',
        applies: 'live',
        where: 'Settings → Notifications',
      },
      { label: 'Traffic digest', key: 'notifications.digest', default: 'off', effect: 'A daily or weekly summary of what your network did.', applies: 'live', where: 'Settings → Notifications' },
      { label: 'Digest time', key: 'notifications.digest_time', default: '09:00', effect: 'When the digest is sent (server time).', applies: 'live', where: 'Settings → Notifications' },
      { label: 'Digest day', key: 'notifications.digest_day', default: 'monday', effect: 'Which day the weekly digest is sent.', applies: 'live', where: 'Settings → Notifications' },
    ],
  },
  {
    id: 'settings-discovery',
    title: 'Settings → Device discovery',
    page: '#/settings?tab=discovery',
    summary: 'How Minos learns the names of the devices on your network. Everything stays on your network.',
    settings: [
      {
        label: 'Ask UPnP devices to introduce themselves (SSDP)',
        key: 'discovery.ssdp',
        default: 'on',
        effect: 'Names smart TVs, consoles and other gadgets by their own name, maker and model.',
        applies: 'live',
        where: 'Settings → Device discovery',
      },
      {
        label: 'Listen for DHCP introductions (Linux)',
        key: 'discovery.dhcp_listen',
        default: 'on',
        effect: 'Devices announce their own name when they join the network. Minos only listens — it never hands out addresses.',
        applies: 'live',
        where: 'Settings → Device discovery',
      },
    ],
  },
  {
    id: 'settings-access',
    title: 'Settings → Access & security',
    page: '#/settings?tab=access',
    summary: 'Who can use this interface and the API.',
    settings: [
      {
        label: 'Password',
        key: 'api.password_hash',
        default: 'none',
        effect:
          'When set, opening the interface asks for it, and a sign-in lasts 30 days on that browser. Five wrong tries from one address wait a minute. Stored only as a one-way hash (argon2id) — set or change it in Settings, never in the file. Forgot it? Delete password_hash from minos.yaml and restart.',
        applies: 'live',
        where: 'Settings → Access & security',
      },
      {
        label: 'Session signing key',
        key: 'api.session_key',
        default: 'Made with the first password',
        effect: 'Signs sign-ins. "Sign out everywhere" replaces it, which ends every session at once. The minos command on the same machine uses it too, so it keeps working with only a password set.',
        applies: 'live',
        where: 'Settings → Access & security',
      },
      {
        label: 'Two-step sign-in (authenticator app)',
        key: 'api.totp_secret',
        default: 'off',
        effect: 'After the password, ask for the 6-digit code from an authenticator app (Google Authenticator, 1Password, Aegis …). Turned on by scanning the code shown in Settings and typing one code back.',
        applies: 'live',
        where: 'Settings → Access & security',
      },
      {
        label: 'API token',
        key: 'api.token',
        default: 'none',
        effect:
          'For scripts and automation (Home Assistant, curl): sent as X-Api-Token or Authorization: Bearer. With a password set too, either works. With neither, anyone on your network can change Minos.',
        applies: 'live',
        where: 'Settings → Access & security',
      },
      {
        label: 'Networks Minos answers',
        key: 'dns.allowed_networks',
        default: 'Private and local networks',
        effect:
          'Which devices may use Minos, as networks (192.168.1.0/24) or single addresses. Blank answers private networks (192.168.x.x, 10.x.x.x, 172.16–31.x.x, 100.64.x.x, fd00::/8, link-local, loopback) and the networks this machine is connected to — enough for any home network, IPv6 included — and refuses everyone else, so an accidentally exposed port never makes Minos an "open resolver" for the internet. Refusals are counted in the Logs, not the Query Log.',
        applies: 'live',
        where: 'Settings → Access & security',
      },
      {
        label: 'Queries a minute per device',
        key: 'dns.rate_limit',
        default: '1000',
        effect:
          'The most lookups one device may make in a minute; beyond that it is refused until the minute is up. Stops a runaway app or an infected device from swamping Minos. The first refusal each minute appears in the Query Log under "rate-limit". 0 turns the limit off.',
        applies: 'live',
        where: 'Settings → Access & security',
      },
      {
        label: 'Interface listen address',
        key: 'api.listen',
        default: '0.0.0.0:8080',
        effect: 'Where this web interface and the API listen.',
        applies: 'file',
        where: 'minos.yaml',
      },
    ],
  },
  {
    id: 'settings-system',
    title: 'Settings → System',
    page: '#/settings?tab=system',
    summary: 'Query-log storage, the application log level, and updates.',
    settings: [
      {
        label: 'Check for new releases once a day',
        key: 'update_check',
        default: 'off',
        effect:
          'Asks GitHub for the latest release — nothing about you or your network is sent. A banner shows how to upgrade. On Linux installs made with the install script, and once a password or API token is set, it has an Update now button: Minos downloads the release, checks it is signed by the Minos release key and is newer, installs it and restarts. If the new version does not answer within 30 seconds, the previous one is put back and you are told.',
        applies: 'live',
        where: 'Settings → System',
      },
      {
        label: 'Install method',
        key: 'update_install_method',
        default: 'detected',
        effect: 'binary, docker or source — forces the upgrade instructions when detection cannot tell.',
        applies: 'file',
        where: 'minos.yaml',
      },
    ],
  },
  {
    id: 'settings-backup',
    title: 'Settings → Backup & import',
    page: '#/settings?tab=backup',
    summary:
      'Download the whole configuration (minos.yaml) as a backup, restore one, or bring your setup across from Pi-hole or AdGuard Home. Imports only add; nothing already configured is removed.',
  },
  {
    id: 'cli',
    title: 'Command line',
    summary: 'The minos command talks to the running server through its API (using -config to find the address and token).',
    howto: [
      'minos status — counters, rules and pause state.',
      'minos pause 30m / minos resume — pause and resume blocking.',
      'minos logs -f -level warn — follow the application log.',
      'minos import pihole /etc/pihole or minos import adguard AdGuardHome.yaml — migrate settings.',
      'minos version.',
    ],
  },
];

// Config keys that are containers of the documented keys above, not
// settings in their own right. The drift test treats them as covered.
export const structuralKeys = ['dns', 'blocking', 'lists', 'querylog', 'api', 'notifications', 'discovery'];
