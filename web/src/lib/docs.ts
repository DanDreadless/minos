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
  // examples are copyable snippets (YAML for Home Assistant, say).
  examples?: { title: string; code: string }[];
  settings?: Setting[];
}

export const docs: Section[] = [
  {
    id: 'how-it-works',
    title: 'How Minos works, in plain words',
    summary:
      "Every time a phone, laptop, TV or smart speaker goes online, it first has to turn a name like www.example.com into the address of the computer that serves it. That step is called a DNS lookup, and it works like looking someone up in a phone book. Minos becomes your home network's phone book: every lookup comes to Minos first, and it refuses to give out the numbers of advertisers, trackers and dangerous sites — so those never load, on any device, without installing anything on them.",
    howto: [
      'A device asks Minos for a name. If the name is on one of your blocklists (lists of advertising, tracking and malware names that other people maintain), Minos answers "no such place" and the advert, tracker or malicious page simply does not load.',
      'If the name is fine, Minos asks a public DNS service on the internet (called an upstream resolver — Cloudflare, Quad9 and Google run well-known ones) and passes the answer back. It remembers answers for a while, so repeat visits are faster.',
      'Your own rules win over the lists: you can always allow a name a list blocks, or block one no list does.',
      'Because it works at the network level, Minos also covers devices you cannot install an ad blocker on — smart TVs, games consoles, voice assistants, apps.',
      'Minos keeps a log of every lookup (the Query Log), so you can see what each device is doing, and why anything was blocked.',
      'Minos cannot block adverts that come from the same place as the content (YouTube video adverts, for example, come from YouTube itself), and it cannot see what you do on a site — only which names your devices look up.',
      'If the machine Minos runs on is switched off or crashes, devices that rely on it cannot look anything up and the internet will seem to be down. Getting started explains how to undo the change in a minute if that ever happens.',
    ],
  },
  {
    id: 'start',
    title: 'Getting started',
    summary:
      'From installed to protecting your whole home in a few steps. You only need to change one setting on your router; nothing has to be installed on your devices.',
    howto: [
      "1 — Find Minos's address. It is the address in your browser's address bar right now, without the :8080 at the end — for example 192.168.1.10. This must not change: on your router, give this machine a fixed address (often called a DHCP reservation or static lease), or ask whoever set it up.",
      "2 — Tell your router to hand out Minos as the DNS server. Open your router's settings (its address is usually on a sticker on the router, such as 192.168.1.1, or in its phone app) and look under LAN, DHCP or Network for a setting called DNS server, primary DNS or similar. Enter Minos's address there and save. Leave the second DNS box empty or put Minos's address in it again — a public DNS service there would let devices go around Minos at random. Some mesh systems call this Advanced → DNS.",
      '3 — Let devices pick up the change. Each device learns the new DNS server when it next joins the network: turn Wi-Fi off and on, restart it, or simply wait — most do within a day.',
      '4 — Check it is working. The Dashboard starts counting lookups, and the Devices page fills with your devices. If a device does not appear after reconnecting, see the questions below.',
      '5 — Choose your blocklists on Blocking → Blocklists. Balanced is the recommended start: it blocks most adverts and tracking and rarely breaks anything.',
      '6 — Set a password (Settings → Access & security), so only you can change Minos.',
      '7 — Use your devices normally for a day. If something stops working, open the Query Log, find the blocked lookup at that time and press Allow. The dashboard also points out when a device keeps retrying something that blocking has broken.',
      'Prefer not to change the router? You can instead set the DNS server on individual devices (in their Wi-Fi or network settings) to Minos\'s address. Only those devices are protected.',
    ],
    tasks: [
      {
        q: 'The internet stopped working after I changed the router. How do I undo it?',
        a: "Go back into your router's DNS setting and set it back to automatic (or to what it was before), then reconnect your devices. Everything works as it did, without Minos. The usual cause is that the machine running Minos was off, or its address changed — check it is on and has the address you entered.",
      },
      {
        q: 'A device does not show up on the Devices page.',
        a: 'It is still using another DNS server. Reconnect it to Wi-Fi or restart it. If it still does not appear, it may have a DNS server set by hand in its own network settings, or use a VPN or "private DNS" feature (Android calls it Private DNS, iPhones iCloud Private Relay) — switch that off, or set its DNS server to Minos\'s address.',
      },
      {
        q: 'My router does not let me change the DNS server.',
        a: "Some internet providers' routers lock it. You can still point each device at Minos by hand (see above): in its Wi-Fi or network settings, change DNS from automatic to manual and enter Minos's address. Ask whoever supports your network if you are unsure.",
      },
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
    id: 'words',
    title: 'Words used in Minos',
    summary:
      'The handful of technical words you will meet in Minos, each in plain terms. You do not need to know any of them to use Minos day to day.',
    tasks: [
      {
        q: 'DNS, lookup, query',
        a: 'DNS is the internet\'s phone book: it turns names (www.example.com) into the addresses computers actually connect to. A lookup — also called a query — is one device asking for one name. A busy home makes tens of thousands a day.',
      },
      {
        q: 'Domain, subdomain',
        a: 'A domain is a name such as example.com. A subdomain sits in front of it: ads.example.com and www.example.com are subdomains of example.com. Allowing or blocking a domain in Minos covers all of its subdomains too.',
      },
      {
        q: 'Blocklist, allowlist',
        a: 'A blocklist is a list of names to block — advertising, tracking or malware — kept up to date by people who specialise in it. Minos downloads the ones you choose and checks them again every day. An allowlist is the opposite: names that are always let through.',
      },
      {
        q: 'Upstream resolver',
        a: 'The public DNS service Minos asks for names it does not block — the next phone book along. Cloudflare (1.1.1.1), Quad9 (9.9.9.9) and Google (8.8.8.8) run well-known ones. You can list several; Minos uses the first that answers.',
      },
      {
        q: 'Encrypted DNS (DoH, DoT, DoQ)',
        a: 'Ordinary DNS travels unencrypted, so your internet provider can see every name your home looks up. DNS over HTTPS (DoH), over TLS (DoT) and over QUIC (DoQ) are three ways of encrypting it. Minos uses an encrypted one to talk to its upstream resolver by default.',
      },
      {
        q: 'Router, DHCP',
        a: 'Your router is the box that connects your home to the internet. One of its jobs (called DHCP) is to give each device an address on your network when it joins — and tell it which DNS server to use. That is why changing the DNS setting on the router points every device at Minos at once.',
      },
      {
        q: 'IP address, MAC address',
        a: 'An IP address (like 192.168.1.23) is a device\'s address on your network; it can change from day to day. A MAC address (like a4:83:e7:12:9b:01) is fixed into the device\'s network hardware, so Minos uses it, when it can see it, to recognise a device even after its IP address changes. Some phones invent a new "private" MAC address per network, which Minos shows as a private address.',
      },
      {
        q: 'Cache',
        a: 'Minos remembers recent answers for a while, so when a device asks for the same name again the answer comes back at once instead of being fetched again.',
      },
      {
        q: 'Query Log',
        a: 'Minos\'s record of every lookup: which device asked for which name, when, and whether it was blocked (and by which list and rule) or allowed.',
      },
      {
        q: 'Tracker, telemetry',
        a: 'A tracker is a service that follows what you do across websites and apps, usually to sell advertising. Telemetry is a device or app reporting how it is used back to its maker. Both are what most blocklists target.',
      },
      {
        q: 'DNSSEC',
        a: 'A way for a website\'s owner to sign their DNS answers, so a forged answer can be spotted. Minos can check those signatures. Most sites do not sign their answers yet, so most lookups show as unsigned, which is normal.',
      },
      {
        q: 'Local records, conditional forwarding',
        a: 'Local records are names Minos answers itself for things on your own network — nas.home pointing at your storage box, say. Conditional forwarding sends lookups for certain names to another DNS server, usually your router, which knows the names of your devices.',
      },
      {
        q: 'CNAME',
        a: 'An alias: a DNS answer that says "this name is really that other name". Some trackers hide behind aliases on a site\'s own name, which Minos checks for (CNAME inspection).',
      },
      {
        q: 'mDNS, NetBIOS, UPnP, DHCP names',
        a: 'Ways devices announce their own names on a home network — printers, speakers, Windows PCs and TVs each tend to use one. Minos listens for them to name your devices; nothing leaves your network.',
      },
      {
        q: 'API, API token',
        a: 'The API is how other programs (scripts, Home Assistant) talk to Minos instead of a person using the web pages. An API token is the password those programs use.',
      },
      {
        q: 'Audit (would block)',
        a: 'A way to try a blocklist without it blocking anything: Minos only notes what it would have blocked, so you can see whether it would break something before switching it on for real.',
      },
      {
        q: 'MQTT',
        a: 'A messaging system smart-home software uses to talk to devices. Home Assistant uses it, and Minos can use it to appear in Home Assistant.',
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
      "This week (under Needs attention) always covers the last 7 days, whatever window is chosen: lookups and the share blocked against the week before, devices seen for the first time, what your security blocklists caught and from which devices (or a nudge if none is switched on), how many tracking companies your devices talked to and which got through most, the busiest device, and settings changes with the latest. Every line links to where you can look into it.",
      'Pick the window at the top (24 hours, 7, 30 or 90 days). It is remembered in this browser.',
      'Click any number, domain or device to open exactly those queries in the Query Log.',
      'Possible threats (in red): a device behaving like malware — many random-looking domains in a few minutes, long random names under one domain (data leaving through DNS), or lookups of known malicious domains. See lookups shows the evidence; This is fine hides it; Never flag adds a domain to the ignore list (Settings → Filtering).',
      'Something may be broken: when a blocklist starts blocking a name a device used to reach (answered normally at least 3 times in the week before) and the device keeps asking for it — 15 times in a couple of minutes, the way an app retries when it has stopped working — Needs attention names the device (a link to its page) and the domain, says how often it used to work, and offers Allow, Allow 1 h and Dismiss (quiet for a week). Names blocked all along, such as page tags and ad hosts, never trigger it however often they are asked for, and nor do blocks you chose yourself (your blocked domains, blocked services, group rules, a blocked device). It can also notify you; the notification opens the device. Lookups let through while blocking was paused show "paused" in the Query Log and do not count as having worked.',
      'Needs attention appears only when something does: no rules loaded, a list failing or retired, a resolver not answering, no password or API token, a nearly full disk, DNSSEC unable to check answers, or new warnings in Logs. Each line links to its fix.',
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
      'The search box takes filters as you type: blocked, allowed, would-block, client:192.168.1.5, domain:example.com (that exact name), type:AAAA, list:StevenBlack (quote names with spaces), upstream:1.1.1.1:853, dnssec:bogus, and -cdn to leave out names containing "cdn". Anything else matches domains, device addresses and device names — kitchen finds every query from the Kitchen iPad. Press / to search, Enter to apply, Esc to clear, Backspace to remove the last filter.',
      'Row actions: Allow (on blocked rows) or Block (on allowed rows), 1 h (allow for an hour, then block again), Check (how the domain would be judged now) and Copy.',
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
      "Every device that has used Minos — phones, laptops, TVs, speakers — named where Minos could find out (it listens for the names devices give themselves, and asks your router). Groups give some devices different rules, for example stricter blocking and a bedtime for the children's tablets.",
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
      'Is each list worth it? (shown with two or more blocklists switched on) compares them: how much of each the others already cover, how many lookups in the last 7, 30 or 90 days only that list would have blocked (with the busiest of them), and roughly how much memory removing it would free. A list that caught nothing the others would not have, and is mostly covered by another, is a candidate to remove. Audit-only lists are left out.',
    ],
    tasks: [
      {
        q: 'Should I block newly registered domains?',
        a: "They are not in the catalog, on purpose. Hagezi's lists of domains registered in the last 7 days hold 2.9 million names (the high-entropy \"DGA\" cut, 900,000); on a Raspberry Pi 4 they add 370 MB and 120 MB of memory and make every rule change take 19 and 3.5 seconds. Replayed on a real home network's week — 1.2 million lookups — both matched three lookups, all of a genuine new website, and nothing malicious. Threat detection already flags devices looking up bursts of random new domains. If you want one anyway, add it as an audit-only list first (Add a list, then tick Audit) to see what it would block: https://raw.githubusercontent.com/hagezi/nrd/main/adblock/dga7.txt (AdBlock format).",
      },
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
      {
        label: 'Allowed for a limited time',
        key: 'blocking.temporary_allows',
        effect:
          'Domains allowed until a time, then blocked again by themselves — "Allow for 1 hour" on a Query Log row or in Check a domain. Shown in the Query Log as allowed by temporary-allow. Up to a week; allowing a domain for good replaces its temporary allow.',
        applies: 'live',
        where: 'Blocking → Allowed',
        fields: [
          { key: 'blocking.temporary_allows.domain', effect: 'The domain, with subdomains.' },
          { key: 'blocking.temporary_allows.until', effect: 'When the allow ends (a timestamp, e.g. 2026-10-26T15:00:00Z).' },
        ],
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
    id: 'privacy',
    title: 'Blocking → Privacy',
    page: '#/privacy',
    summary:
      'Which tracking companies your devices talked to — advertising, analytics and telemetry — what got through and what your blocklists stopped, for the whole network or one device (on its page).',
    howto: [
      'Each company shows how many of its tracking lookups got through and how many were blocked; the bar is the blocked share. Open it for its names, what each is for, and (for the whole network) which devices looked it up.',
      "A name counts as tracking only when it is one of the company's advertising, analytics or telemetry names that a curated DNS blocklist (Hagezi Pro) also blocks. A company's own sites and services — Google search, YouTube, Facebook itself, time servers — never count, and are listed separately as other companies.",
      "Block (company)'s tracking blocks that company's tracking names for every device, as the list \"company:<id>\" in the Query Log. Its own sites, apps and sign-in keep working. Stop blocking undoes it; it is also in Settings → History.",
      'Periods are whole days (UTC): today, the last 7 days or the last 30 days.',
      'Company data: AdGuard companiesdb (built from WhoTracks.me), CC BY-SA 4.0. Tracking names: Hagezi DNS blocklists, GPLv3. Both are built into Minos and updated with releases; nothing is sent anywhere.',
    ],
    settings: [
      {
        label: 'Companies whose tracking is blocked',
        key: 'blocking.companies',
        default: 'none',
        effect: 'Company IDs (from Blocking → Privacy) whose advertising, analytics and telemetry names are blocked for everyone.',
        applies: 'live',
        where: 'Blocking → Privacy',
      },
    ],
  },
  {
    id: 'localdns',
    title: 'Settings → DNS: names on your network',
    page: '#/settings?tab=dns',
    summary:
      'Names on your own network: records Minos answers itself, and domains it sends to another resolver — usually your router, so device names from DHCP keep working.',
    howto: [
      'Both live in Settings → DNS, below the upstream resolvers. Local records save the moment you add, edit or remove one; conditional forwarding is saved with the rest of the tab by Save changes.',
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
        where: 'Settings → DNS',
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
        where: 'Settings → DNS',
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
    id: 'home-assistant',
    title: 'Home Assistant',
    page: '#/settings?tab=homeassistant',
    summary:
      'Minos can appear in Home Assistant as a device of its own — a blocking switch, pause buttons, counters, problem sensors and a Block switch for each named device — with no YAML. It talks to Home Assistant through MQTT, the messaging system Home Assistant uses for devices like this.',
    howto: [
      "In Home Assistant, go to Settings → Add-ons, install the Mosquitto broker add-on and start it. Home Assistant then offers the MQTT integration under Settings → Devices & services; add it.",
      "Still in Home Assistant, make a user for Minos (Settings → People → Users → Add person, with a login) — for example \"minos\" with its own password. The Mosquitto add-on lets any Home Assistant user log in to the broker.",
      "In Minos, open Settings → Home Assistant. Enter the broker as mqtt://<Home Assistant's address>:1883 (for example mqtt://192.168.1.10:1883), the username and password you just made, tick Connect to Home Assistant and save. The status line turns green with \"Connected to the broker\".",
      'In Home Assistant, open Settings → Devices & services → MQTT → Devices: Minos is listed. Add its entities to a dashboard like any other device.',
      'Entities: switch.minos_blocking (off pauses blocking until turned back on); button.minos_pause_blocking_for_5_minutes and …_for_1_hour; sensor.minos_queries, sensor.minos_blocked_queries and sensor.minos_share_blocked (since Minos last started); sensor.minos_blocklist_rules; binary_sensor.minos_upstream_resolver_problem, binary_sensor.minos_threat_suspected and binary_sensor.minos_blocking_may_have_broken_something (on when there is a problem); update.minos_update (when Check for new releases is on); and switch.minos_block_<device> for each device you have named on the Devices page.',
      'State is sent when it changes and every 30 seconds. If Minos stops, its entities show as unavailable; if you untick Connect to Home Assistant, they are removed.',
      'Anything that can publish MQTT can control Minos too: minos/blocking/set (ON or OFF), minos/pause/set (minutes, e.g. 30) and minos/device/<id>/set (ON blocks, OFF unblocks). Do not send them retained — a retained command is ignored. Minos publishes minos/state (JSON), minos/availability (online/offline) and minos/device/<id>/state.',
      "No MQTT broker? Home Assistant's REST integrations work against the Minos API instead: set an API token (Settings → Access & security) and send it as the X-Api-Token header. docs/home-assistant.md has ready-made REST sensors, a switch and commands.",
    ],
    examples: [
      {
        title: 'Bedtime: the kids\' tablet goes offline at 21:00 and back at 07:00',
        code: `automation:
  - alias: Kids tablet off at bedtime
    triggers:
      - trigger: time
        at: "21:00:00"
    actions:
      - action: switch.turn_on
        target:
          entity_id: switch.minos_block_kids_tablet
  - alias: Kids tablet back in the morning
    triggers:
      - trigger: time
        at: "07:00:00"
    actions:
      - action: switch.turn_off
        target:
          entity_id: switch.minos_block_kids_tablet`,
      },
      {
        title: 'Tell me on my phone when Minos suspects a threat',
        code: `automation:
  - alias: Minos threat
    triggers:
      - trigger: state
        entity_id: binary_sensor.minos_threat_suspected
        to: "on"
    actions:
      - action: notify.mobile_app_your_phone
        data:
          title: Minos suspects a threat
          message: Open Minos to see which device.`,
      },
      {
        title: 'Pause blocking for 15 minutes from a script or another automation',
        code: `script:
  minos_pause_15:
    alias: Pause Minos for 15 minutes
    sequence:
      - action: mqtt.publish
        data:
          topic: minos/pause/set
          payload: "15"`,
      },
    ],
    tasks: [
      {
        q: 'The status says "not authorized" or "wrong username or password".',
        a: 'The broker refused the login. Check the Home Assistant user and its password (log in to Home Assistant with it once to be sure), then type the password again in Minos — it is never shown, so retyping is the way to be sure it is right.',
      },
      {
        q: 'The status says it cannot reach the broker.',
        a: 'Check the address and port (1883, or 8883 for mqtts://), that the Mosquitto add-on is running, and that Home Assistant\'s machine accepts connections on that port from the machine Minos runs on.',
      },
      {
        q: 'Minos says connected, but no Minos device appears in Home Assistant.',
        a: 'The MQTT integration must use the same broker, and discovery must be on with the prefix "homeassistant" (Settings → Devices & services → MQTT → Configure). If you changed the prefix there, set the same Discovery prefix in Minos.',
      },
      {
        q: 'A device has no Block switch.',
        a: 'Switches are made for devices with a name: give the device one on the Devices page (or its own page) and the switch appears within half a minute. Add a Block switch for each named device must also be ticked.',
      },
      {
        q: 'I run two Minos servers on one broker.',
        a: 'Give each its own Topic prefix (minos and minos2, say) in Settings → Home Assistant, or their entities collide.',
      },
      {
        q: 'How do I take Minos out of Home Assistant?',
        a: 'Untick Connect to Home Assistant and save; Minos removes its entities before it disconnects.',
      },
    ],
  },
  {
    id: 'settings-dns',
    title: 'Settings → DNS',
    page: '#/settings?tab=dns',
    summary:
      'Where Minos asks about names it does not block, and how it answers names on your own network. The defaults work for almost everyone: you only need this tab to choose a different DNS provider, or to give names to things at home.',
    howto: [
      'In short: upstream resolvers are the public DNS services Minos asks on your behalf. Local DNS records and conditional forwarding (further down) are for names on your own network. DNSSEC and the cache can be left as they are.',
      'Upstream resolvers are tried in order: the first is the primary. The light beside each is green when it is answering, red when failing, hollow when it has not been needed yet.',
      'Encrypted protocols (DoH, DoT) keep your lookups private from your internet provider; the presets are verified to work.',
      'Changes on every Settings tab are drafts until you press Save changes in the bar at the bottom.',
    ],
    settings: [
      {
        label: 'Upstream resolvers',
        key: 'dns.upstreams',
        default: 'Cloudflare: https://1.1.1.1/dns-query (DoH), then 1.0.0.1:853 (DoT)',
        effect:
          'Where allowed lookups are sent, in order. A resolver that fails three times in a row is skipped, and Minos checks it again every 30 seconds until it answers — the light turns green by itself. Backups are also checked every 30 minutes, so a dead one shows before the day it is needed. Test asks a resolver a question now, saved or not.',
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
    summary:
      'How blocked lookups are answered, and the protections that stop devices quietly going around Minos (some apps and browsers bring their own DNS). The defaults are sensible; change them only if you know you need to.',
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
      {
        label: 'Threat detection',
        key: 'threats.enabled',
        default: 'on',
        effect:
          'Watches for devices behaving like malware and says so under Needs attention (and in a notification): many random-looking domains in a few minutes (how malware finds its control server), long random names under one domain (data sent out through DNS), or a lookup blocked by a threat-intelligence list such as URLhaus or Hagezi TIF. "This is fine" hides a finding for a week (a day for known malicious domains). It reads the query log in the background, so lookups are never slowed. Tuned on 90 days of a real home network: no false alarms for the first two kinds.',
        applies: 'live',
        where: 'Settings → Filtering',
      },
      {
        label: 'Never flag these domains',
        key: 'threats.ignore',
        default: 'none',
        effect:
          'Domains (with subdomains) threat detection leaves alone — a service that legitimately uses long random names. Antivirus and spam-reputation lookups (Sophos, McAfee, ESET, Spamhaus…) are already left out. "Never flag" on a finding adds to it.',
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
    summary:
      'How Minos learns the names of the devices on your network, so the Devices page says "Living room TV" rather than a number. Devices announce their own names in several ways; each switch here lets Minos listen to one of them. Everything stays on your network, and leaving them all on is fine.',
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
    id: 'settings-homeassistant',
    title: 'Settings → Home Assistant',
    page: '#/settings?tab=homeassistant',
    summary:
      'Puts Minos in Home Assistant with no YAML. Minos announces itself over MQTT (Home Assistant\'s "MQTT discovery"), so a Minos device appears with everything below, kept up to date within seconds. The Home Assistant section of Help has the step-by-step setup, the entity names and example automations.',
    howto: [
      'In Home Assistant, install the Mosquitto broker add-on (Settings → Add-ons) and add the MQTT integration if it isn\'t there already.',
      'Make a Home Assistant user for Minos (Settings → People → Users), for example "minos" — the Mosquitto add-on accepts Home Assistant users.',
      'Here, enter the broker as mqtt://<Home Assistant\'s address>:1883 with that username and password, tick Connect to Home Assistant and save. The status line says when Minos is connected.',
      'In Home Assistant, open Settings → Devices & services → MQTT: the Minos device is listed with its entities.',
      'What you get: a Blocking switch (off pauses blocking until you turn it back on), Pause blocking for 5 minutes and for 1 hour buttons, Queries, Blocked queries and Share blocked sensors (counted since Minos started), Blocklist rules, problem sensors for an upstream resolver failing, a suspected threat and blocking that may have broken something, an Update entity when Check for new releases is on, and a Block switch for each device you have named.',
      'Changes made from Home Assistant act straight away; blocking a device is recorded in Settings → History as "Home Assistant (MQTT)".',
    ],
    tasks: [
      {
        q: 'The status says "wrong username or password" or "not authorized".',
        a: 'Check the user in Home Assistant (or the broker\'s password file). With the Mosquitto add-on, a Home Assistant user works; an "owner" account works too but a separate user is safer.',
      },
      {
        q: 'Minos is connected but nothing appears in Home Assistant.',
        a: 'The MQTT integration in Home Assistant must be using the same broker, and its discovery prefix must match (homeassistant unless you changed it).',
      },
      {
        q: 'How do I remove Minos from Home Assistant?',
        a: 'Untick Connect to Home Assistant and save: Minos removes its entities before it disconnects.',
      },
    ],
    settings: [
      {
        label: 'Connect to Home Assistant',
        key: 'home_assistant.enabled',
        default: 'off',
        effect: 'Connects to the MQTT broker and announces Minos to Home Assistant. Turning it off removes the Minos entities from Home Assistant.',
        applies: 'live',
        where: 'Settings → Home Assistant',
      },
      {
        label: 'MQTT broker',
        key: 'home_assistant.broker',
        default: 'none',
        effect: 'The broker Home Assistant uses: mqtt://host:1883, or mqtts://host:8883 for TLS (the certificate must be valid for the host name). If the broker goes away Minos keeps trying, waiting up to a minute between attempts.',
        applies: 'live',
        where: 'Settings → Home Assistant',
      },
      {
        label: 'Username',
        key: 'home_assistant.username',
        default: 'none',
        effect: 'The broker login. For the Mosquitto add-on, a Home Assistant user.',
        applies: 'live',
        where: 'Settings → Home Assistant',
      },
      {
        label: 'Password',
        key: 'home_assistant.password',
        default: 'none',
        effect: 'The broker password. Stored in the config file; never shown again, and hidden in the settings history.',
        applies: 'live',
        where: 'Settings → Home Assistant',
      },
      {
        label: 'Add a Block switch for each named device',
        key: 'home_assistant.device_switches',
        default: 'on',
        effect: 'One switch per device you have named on the Devices page. On blocks all of that device\'s DNS, exactly like Block device in Minos — handy for a bedtime automation.',
        applies: 'live',
        where: 'Settings → Home Assistant',
      },
      {
        label: 'Discovery prefix',
        key: 'home_assistant.discovery_prefix',
        default: 'homeassistant',
        effect: 'Where Home Assistant listens for announcements. Change it only if you changed it in Home Assistant\'s MQTT settings.',
        applies: 'live',
        where: 'Settings → Home Assistant',
      },
      {
        label: 'Topic prefix',
        key: 'home_assistant.topic_prefix',
        default: 'minos',
        effect: 'Where Minos publishes its state and listens for commands (minos/state, minos/blocking/set…). Give each Minos its own when two share a broker.',
        applies: 'live',
        where: 'Settings → Home Assistant',
      },
    ],
  },
  {
    id: 'settings-access',
    title: 'Settings → Access & security',
    page: '#/settings?tab=access',
    summary:
      'Who can use this interface and the API, and which devices may use Minos for DNS. At the very least, set a password: without one, anyone on your network can open these pages and change or switch off your blocking.',
    tasks: [
      {
        q: 'I forgot the password.',
        a: 'On the Minos machine, delete the password_hash line (and totp_secret, if you set up an authenticator app) from minos.yaml, then restart Minos. The interface is open again until you set a new password.',
      },
      {
        q: 'I lost the phone with my authenticator app.',
        a: 'Delete totp_secret from minos.yaml and restart Minos; signing in then asks only for the password. Set the app up again afterwards.',
      },
      {
        q: 'Home Assistant or a script stopped working after I set a password.',
        a: 'Scripts cannot sign in with a password. Set an API token here as well and send it as X-Api-Token (or Authorization: Bearer); both work side by side.',
      },
    ],
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
    summary:
      'Query-log storage, the application log level, and updates. Check now, in the Updates card, asks for the newest release straight away instead of waiting for the daily check.',
    settings: [
      {
        label: 'Check for new releases once a day',
        key: 'update_check',
        default: 'off',
        effect:
          'Asks GitHub for the latest release — nothing about you or your network is sent. A banner shows how to upgrade. Check now asks at once, whether or not this is on. On Linux installs made with the install script, and once a password or API token is set, it has an Update now button: Minos downloads the release, checks it is signed by the Minos release key and is newer, installs it and restarts. If the new version does not answer within 30 seconds, the previous one is put back and you are told.',
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
    id: 'settings-history',
    title: 'Settings → History',
    page: '#/settings?tab=history',
    summary:
      'Every settings change made here, by a script or with the minos command — when, who (password sign-in, API token, the minos command, with the address) and which settings — with a way back. The last 100 changes are kept, beside the config file.',
    howto: [
      'Show changes opens the change itself, line by line. Passwords, tokens and keys are always hidden.',
      'Undo last change restores the version before the latest change; Restore this version restores any earlier one.',
      'Restoring never touches the password, authenticator codes, API token, notification and certificate secrets, list request headers, or settings that need a restart (listen addresses, encrypted DNS, query-log storage) — so it cannot lock you out. A restore is recorded as a change of its own, so it can be undone too.',
      "Minos's own housekeeping (an hour's allow ending, say) is not recorded.",
    ],
  },
  {
    id: 'cli',
    title: 'Command line',
    summary:
      'For people comfortable typing commands on the Minos machine; everything here can also be done in the web pages. The minos command talks to the running server through its API, using -config to find the address and credentials — the API token, or with only a password set, a sign-in it makes from the config file itself.',
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
export const structuralKeys = ['dns', 'blocking', 'lists', 'querylog', 'api', 'notifications', 'discovery', 'threats', 'home_assistant'];
