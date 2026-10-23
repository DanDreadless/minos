// All UI copy lives here so it can be audited or localised in one place.
// Rules (see CLAUDE.md, "Product vocabulary"): labels, buttons and messages
// use plain words — Blocked/Allowed, Allow/Block, Pause blocking, Upstream
// resolvers. The judgment flavour survives only in the tagline and a few
// empty states, where it can't be misread. Errors are always plain.
export const copy = {
  appName: 'Minos',
  tagline: 'Every query gets judged.',

  update: {
    available: (v: string) => `Minos v${v} is available.`,
    whatsNew: "What's new",
    howTo: (method: string) => `To upgrade (${method}):`,
    copy: 'Copy command',
    copied: 'Upgrade command copied.',
  },

  nav: {
    openMenu: 'Open menu',
    closeMenu: 'Close menu',
    dashboard: { label: 'Dashboard', hint: 'what your network is doing' },
    querylog: { label: 'Query Log', hint: 'every lookup, live and searchable' },
    devices: { label: 'Devices', hint: 'devices, groups & schedules' },
    lists: { label: 'Blocklists', hint: 'the lists that decide what is blocked' },
    domains: { label: 'Allow & Block', hint: 'your own domains & services' },
    settings: { label: 'Settings', hint: 'resolvers, filtering, notifications…' },
    local: { label: 'Local DNS', hint: 'names on your own network' },
    logs: { label: 'Logs', hint: "Minos's own activity" },
    logsAlert: 'new warnings or errors',
  },

  logs: {
    title: 'Logs',
    subtitle: "what Minos itself is doing — list updates, resolver trouble, errors",
    searchPlaceholder: 'Search messages and details…',
    showLabel: 'Show',
    show: [
      { level: 'debug', label: 'Everything' },
      { level: 'info', label: 'Info and up' },
      { level: 'warn', label: 'Warnings and errors' },
      { level: 'error', label: 'Errors only' },
    ] as { level: 'debug' | 'info' | 'warn' | 'error'; label: string }[],
    recordLabel: 'Record',
    recordHint:
      'Which messages Minos keeps — in this page and in the system journal. Applies at once. Debug is very chatty (every query); use it briefly.',
    recordLevels: { debug: 'debug (very detailed)', info: 'info (normal)', warn: 'warnings and errors', error: 'errors only' } as Record<string, string>,
    live: 'Live',
    paused: (n: number) => (n ? `Paused · ${n} new` : 'Paused'),
    reconnecting: 'Reconnecting…',
    pause: 'Pause',
    resume: 'Resume',
    download: 'Download',
    downloadHint: 'everything held, as a text file — attach it to a bug report (secrets are already removed)',
    copyLine: 'Copy',
    copied: 'Copied the line.',
    count: (shown: number, held: number) => `${shown} of ${held} held`,
    heldNote:
      'Only the newest 2,000 messages are kept in memory and they are cleared when Minos restarts. The full history is in the system journal (journalctl -u minos) or docker logs.',
    empty: 'Nothing logged yet.',
    noMatches: 'No messages match.',
    belowLevel: (level: string) =>
      `Messages below "${level}" are not being recorded. Change "Record" to see them from now on.`,
    levelName: { debug: 'debug', info: 'info', warn: 'warning', error: 'error' } as Record<string, string>,
  },

  stats: {
    judged: 'Queries',
    condemned: 'Blocked',
    windowHint: (w: string) => `in the ${w} — click to see them in the Query Log`,
    sinceRestart: (total: number, blocked: number, uptime: number) => {
      const d = Math.floor(uptime / 86400);
      const h = Math.floor((uptime % 86400) / 3600);
      const m = Math.floor((uptime % 3600) / 60);
      const up = d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`;
      return `Since Minos started ${up} ago: ${total.toLocaleString()} queries, ${blocked.toLocaleString()} blocked.`;
    },
    blockRate: 'Block rate',
    blockRateHint: 'share of queries blocked',
    rules: 'Rules',
    rulesHint: 'blocking rules loaded from your lists and settings',
    cacheRate: 'Cache hits',
    cacheRateHint: 'answers served from memory, no upstream trip',
  },

  dashboard: {
    windowLabel: 'Showing',
    windows: [
      { hours: 24, label: '24 h', long: 'last 24 hours', range: '24h' },
      { hours: 168, label: '7 days', long: 'last 7 days', range: '7d' },
      { hours: 720, label: '30 days', long: 'last 30 days', range: '30d' },
      { hours: 2160, label: '90 days', long: 'last 90 days', range: '90d' },
    ] as const,
    timelineTitle: (w: string) => `Queries — ${w}`,
    windowHint: (w: string) => w,
    topBlockedTitle: 'Most blocked domains',
    topClientsTitle: 'Busiest devices',
    noData: 'No query data yet.',
  },

  // DNSSEC proves an answer really came from the domain it claims. The
  // states are RFC jargon, so every label here is the plain meaning and
  // the hint carries the nuance — a user must never have to know what
  // "indeterminate" means to read this card.
  dnssec: {
    title: 'DNSSEC',
    titleHint: 'answer authenticity',
    modePermissive: 'permissive',
    modePermissiveHint: 'checking and recording, never refusing — a dry run for enforce',
    modeEnforce: 'enforce',
    modeEnforceHint: 'answers that fail the check are refused',
    wouldBlock: (n: number) =>
      `${n.toLocaleString()} ${n === 1 ? 'answer' : 'answers'} would have been refused in enforce mode`,
    wouldBlockHint: 'in the window above — each of these would become a failed lookup if you switch',
    wouldBlockNone: 'Nothing failed the check in this window.',
    wouldBlockNoneHint: 'Permissive mode is watching. Give it time before deciding on enforce.',
    seeDocket: 'Review them in the Query Log',
    enforceNote: 'Refused answers show as blocked in the Query Log, under the list "dnssec".',
    countersTitle: 'Since restart',
    countersHint: 'click a number to see those queries — the Query Log covers all kept history, so its totals will differ',
    secure: 'Verified',
    secureHint: 'signed, and the signature checked out',
    insecure: 'Unsigned',
    insecureHint: 'provably not signed — normal for most of the internet',
    bogus: 'Failed',
    bogusHint: 'signed, but the signature did not verify',
    indeterminate: 'Not checkable',
    indeterminateHint: 'no DNSSEC records came back; these always pass, in either mode',
    upstreamWarning:
      'Most answers could not be checked at all — your upstream resolver is probably not returning DNSSEC records, so validation is doing very little here.',
  },

  // The host panel. Deliberately plain-spoken: this is the one card that
  // is not about DNS at all, and a themed name would earn nothing. Every
  // value can be absent, so each label pairs with an em dash rather than
  // a zero when the platform cannot measure it.
  host: {
    title: 'Host',
    titleHint: 'the machine Minos runs on',
    cpu: 'CPU',
    cpuHint: 'utilisation across all cores since the last reading',
    load: 'Load',
    loadHint: '1-minute run queue — roughly, how many things are waiting for a core',
    memory: 'Memory',
    memoryHint: 'in use, of the total available to Minos',
    disk: 'Disk',
    diskHint: 'on the filesystem holding the query log — the SD card, on a Pi',
    temperature: 'Temperature',
    temperatureHint: 'CPU package temperature',
    uptime: 'Uptime',
    uptimeHint: 'since the machine last booted, not since Minos started',
    process: 'Minos',
    processHint: 'memory resident for Minos itself',
    unavailable: '—',
    unavailableHint: 'this platform cannot report that',
    cgroupNote: 'Memory figures are the container limit set for Minos, not the whole machine.',
    diskLow: 'Disk is nearly full. The query log lives here — pruning retention or moving db_path will buy room.',
    tokenHint: 'This page is readable by anyone on your network. Set an API token to lock it down.',
    tokenAction: 'Settings',
  },

  querylog: {
    title: 'Query Log',
    subtitle: 'every DNS lookup, live and searchable',
    empty: 'No queries yet. Point a device at Minos and every lookup it makes is judged here.',
    verdictBlocked: 'blocked',
    verdictAllowed: 'allowed',
    searchPlaceholder: 'Filter by domain or client…',
    searchScopeHint:
      'The search box matches domain and client names. To filter by blocklist, use the list dropdown instead — it is much faster too.',
    filterAll: 'All',
    filterBlocked: 'Blocked',
    filterAllowed: 'Allowed',
    filterWouldBlock: 'Would block',
    filterListAll: 'All lists',
    filterListTitle: 'show only queries this list matched',
    wouldBlockBadge: 'would block',
    wouldBlockTitle: (list: string, rule: string) =>
      list === 'dnssec'
        ? `This answer failed its DNSSEC check: "${rule}". Enforce mode would have refused it; permissive mode let it through.`
        : `A list in audit mode would have blocked this: "${rule}" in ${list}. Audit lists only record, so it was allowed.`,
    dnssecScope: 'DNSSEC', // prefixes the drilled-in validation-outcome chip
    dnssecName: (s: string) =>
      ({
        secure: 'Verified',
        insecure: 'Unsigned',
        bogus: 'Failed',
        indeterminate: 'Not checkable',
      })[s] ?? s,
    dnssecScopeTitle: (s: string) =>
      ({
        secure: 'Signed, and the signature checked out.',
        insecure: 'Provably not signed — normal for most of the internet.',
        bogus: 'Signed, but the signature did not verify.',
        indeterminate: 'No DNSSEC records came back, so nothing could be checked.',
      })[s] ?? s,
    deviceScope: 'device', // prefixes the drilled-in device's IP(s) chip
    // What the shown rows are, beside the count — a partial view must never
    // pass for a complete one.
    scopeHistory: 'full history',
    scopeRecentSearching: 'recent queries only — searching full history…',
    scopeRecentFailed: 'recent queries only',
    scopeRecent: 'recent queries only',
    searching: 'Searching the full history…',
    noMatches: 'No queries match these filters in the retained history.',
    noRecentMatches: 'No recent queries match.',
    searchFailed: (error: string, showingRecent: boolean) =>
      `The full-history search did not finish: ${error}${showingRecent ? ' Showing only matches among recent queries.' : ''}`,
    live: 'live',
    searchBoxPlaceholder: 'Search domains or devices — try  blocked  type:AAAA  -cdn',
    searchBoxLabel: 'Search the query log',
    rangeLabel: 'Time range',
    // The two modes, said plainly in the status line.
    modeLive: 'Live',
    modeLiveDetail: (n: number) => `newest ${n} queries, updating as they arrive`,
    modePaused: (held: number) =>
      held ? `Paused · ${held} new ${held === 1 ? 'query' : 'queries'} waiting` : 'Paused',
    modeReconnecting: 'Reconnecting to the live feed…',
    modeResults: 'Results',
    resultsDetail: (n: number, more: boolean, range: string) =>
      `${n === 0 ? 'no' : more ? `newest ${n}` : n} ${n === 1 ? 'match' : 'matches'} · ${range.toLowerCase()}`,
    pause: 'Pause',
    pauseHint: 'Stop new queries moving the rows while you read',
    resume: 'Resume',
    clearAll: 'Clear filters',
    exportLabel: 'Export',
    exportHint: (fmt: string) =>
      `Download every match (up to 100,000) as ${fmt.toUpperCase()}`,
    exporting: 'Preparing…',
    loadOlder: 'Load older',
    loadingOlder: 'Loading…',
    filterHint: 'Show only queries with this value',
    removeFilter: (label: string) => `remove the ${label} filter`,
    columnSource: 'List or resolver',
    columnSourceHint:
      'For a blocked query, the list that matched (hover for the rule). For an allowed one, the resolver that answered.',
    copyAction: 'Copy',
    copyHint: 'copy the domain',
    copied: (d: string) => `Copied ${d}`,
    checkAction: 'Check',
    checkHint: 'would this domain be blocked now, with the current lists?',
    checkResult: (d: string, blocked: boolean, list: string, rule: string) =>
      blocked ? `${d} would be blocked now by ${list} ("${rule}").` : `${d} would be allowed now.`,
    syntaxTitle: 'Search syntax',
    syntax: [
      ['blocked · allowed · would-block', 'only that result'],
      ['client:192.168.1.5', 'one device (exact address; comma-separate several)'],
      ['domain:example.com', 'one exact name, not its subdomains'],
      ['type:AAAA', 'one record type'],
      ['list:StevenBlack · list:"My list"', 'queries a list matched (quote names with spaces)'],
      ['upstream:1.1.1.1:853', 'answered by one resolver'],
      ['dnssec:bogus', 'one DNSSEC outcome'],
      ['-cdn', 'leave out names containing "cdn"'],
      ['anything else', 'domain or device address contains it'],
    ] as [string, string][],
    syntaxKeys: 'Press / to search from anywhere on the page, Enter to apply, Esc to clear.',
  },

  allow: {
    action: 'Allow',
    actionHint: 'always allow this domain and its subdomains',
    done: (domain: string) => `${domain} is now always allowed.`,
  },

  block: {
    action: 'Block',
    actionHint: 'always block this domain and its subdomains',
    done: (domain: string) => `${domain} is now always blocked.`,
  },

  attention: {
    title: 'Needs attention',
    noRules: 'Nothing is being blocked: no blocklist rules are loaded.',
    list: (name: string, state: string) =>
      ({
        unavailable: `${name} has never downloaded, so it is not blocking anything.`,
        retired: `${name} has been retired by its publisher — replace it with a current list.`,
        unauthorized: `${name} now refuses downloads — it may need a key.`,
      })[state] ?? `${name} needs attention.`,
    listsFailing: (names: string[]) =>
      `${names.length === 1 ? `${names[0]} is` : `${names.length} lists are`} not updating — still blocking with the last good copy.`,
    dnsDown: 'No upstream resolver is answering — devices cannot look up websites.',
    upstreamsFailing: (names: string[]) =>
      `${names.length === 1 ? `Upstream resolver ${names[0]} is` : `${names.length} upstream resolvers are`} not answering; the others are covering.`,
    noToken: 'No API token is set, so anyone on your network can change Minos.',
    disk: 'The disk holding the query log is over 90% full.',
    dnssecBlind:
      'DNSSEC validation is on, but most answers cannot be checked — your upstream resolver is probably not returning DNSSEC records.',
    logs: 'New warnings or errors in the application log.',
    openLists: 'Open Blocklists',
    openResolvers: 'Open resolver settings',
    openAccess: 'Set a token',
    openSystem: 'Open System settings',
    openLogs: 'Open Logs',
  },

  header: {
    blocking: (rules: number) =>
      `Blocking · ${rules >= 10000 ? `${Math.round(rules / 1000).toLocaleString()}k` : rules.toLocaleString()} rules`,
    noRules: 'Not blocking — no blocklist rules loaded',
    noRulesHint: 'Every lookup is allowed until a blocklist loads. Open Blocklists to add or fix one.',
    pauseFor: [
      { duration: '5m', label: 'for 5 minutes' },
      { duration: '30m', label: 'for 30 minutes' },
      { duration: '1h', label: 'for 1 hour' },
      { duration: '', label: 'until I resume it' },
    ],
    pauseCustom: '2h, 45m…',
    pauseGo: 'Pause',
    searchPlaceholder: 'Check a domain…',
    searchLabel: 'Check whether a domain is blocked',
    openInLog: 'Show in Query Log',
  },

  pause: {
    action: 'Pause blocking',
    actionHint: 'every lookup is allowed until it resumes',
    resume: 'Resume blocking',
    active: (until: string) => `Blocking paused until ${until}.`,
    activeIndefinite: 'Blocking paused.',
    headerPill: 'Blocking paused',
  },

  setup: {
    // The one lore touch on this card; everything below it is plain.
    title: "Every query gets judged. Let's set up your first.",
    hint: 'three minutes from empty to blocking',
    dismiss: 'Dismiss',
    dismissTitle: "Hide this checklist for good — it won't come back.",
    step1: (addr: string) =>
      `Point a device at Minos: set its DNS server to this machine${addr ? ` (listening on ${addr})` : ''}, or set your router's DHCP DNS option so every device follows.`,
    step1Link: 'setup guide',
    step2: 'Choose your blocklists — the catalog has curated, verified picks with one-click subscribe.',
    step2Link: 'open Blocklists',
    step3: 'Check your upstream resolvers — the default, Cloudflare over encrypted DNS, is already a good choice.',
    step3Link: 'Settings',
    step4: 'Optional: turn on the update check and notifications.',
    step4Link: 'Settings',
  },

  lists: {
    title: 'Blocklists',
    subtitle: 'the lists Minos blocks from, kept up to date automatically',
    empty: 'No blocklists configured. Add one below.',
    refreshAll: 'Refresh all lists',
    refreshEvery: 'Check for updates every',
    refreshing: 'Refreshing…',
    addTitle: 'Add a list',
    allowBadge: 'allowlist',
    allowBadgeTitle:
      'Every entry on this list is always allowed, even if a blocklist has it.',
    actionBlock: 'blocklist — entries are blocked',
    actionAllow: 'allowlist — entries are always allowed',
    confirmDelete: (name: string) =>
      `Remove the list "${name}"? Its rules stop applying immediately.`,
    auditHeader: 'Audit',
    auditTitle:
      "Audit mode: this list's matches are recorded as 'would block' in the Query Log but not blocked — try a strict list safely, then untick to enforce it.",
    auditBadge: 'audit',
    auditBadgeTitle: 'Recorded as "would block", not blocked.',
    blocksHeader: 'Blocked (7d)',
    blocksLinkTitle: 'see these queries in the Query Log',
    noBlocks: 'no blocks this week',
    noBlocksTitle:
      'No queries matched this list in the last 7 days. It may still be a quiet safety net — security lists often are.',
    allowStatTitle: 'An allowlist only allows; it never blocks.',
    builtinStatsTitle: 'Blocked by your own rules this week:',
    statDenylist: 'your blocked domains',
    statClients: 'device blocks',
    statService: (label: string) => `service ${label}`,
    statGroup: (name: string) => `group ${name}`,
    statusHeader: 'Status',
    // One plain label per server-side list state. `age` is how old the copy
    // in force is ("3 hours ago"). Every label says whether the list is
    // still blocking, because that is what the user actually needs to know.
    state: (state: string, age: string): string =>
      ({
        pending: 'Not downloaded yet',
        ok: `Up to date · checked ${age}`,
        failing: `Update failing · still blocking with the copy from ${age}`,
        stale: `Out of date · still blocking with the copy from ${age}`,
        unavailable: 'Not loaded · not blocking anything',
        retired: `Retired by its publisher · still blocking with the copy from ${age}`,
        unauthorized: `Now needs credentials · still blocking with the copy from ${age}`,
        suspicious: `Last download refused · still blocking with the copy from ${age}`,
        disabled: 'Off',
      })[state] ?? state,
    replace: (label: string) => `Replace with ${label}`,
    replaceHint: (label: string) =>
      `This list has been retired. Subscribe to ${label} from the catalog instead — it is added first, then this one is removed, so blocking never lapses.`,
    replaced: (from: string, to: string) => `Replaced "${from}" with "${to}".`,
    addMirrors: 'Add mirrors',
    addMirrorsHint: (mirrors: string[]) =>
      `The catalog has verified fallback copies of this list. They are used only if the main address fails for long or disappears:\n${mirrors.join('\n')}`,
    mirrorsAdded: (name: string, n: number) =>
      `Added ${n} ${n === 1 ? 'mirror' : 'mirrors'} to "${name}".`,
    viaMirror: 'via mirror',
    viaMirrorHint: (mirror: string, primaryError: string) =>
      `The main address failed (${primaryError}), so this copy came from the mirror ${mirror}. Minos tries the main address first on every refresh.`,
    mirrorsHint: (n: number) =>
      `${n} fallback ${n === 1 ? 'mirror' : 'mirrors'}, used only if the main address is gone or the copy gets old.`,
    stateHint: (state: string, error: string): string =>
      ({
        pending: 'Minos will download this list shortly.',
        ok: 'The last check reached the publisher successfully.',
        failing: `The last update failed: ${error}. Minos retries on a short backoff and keeps using the last good copy.`,
        stale: `Updates have failed for more than two refresh intervals: ${error}. The copy in force is getting old.`,
        unavailable: `No copy of this list could be downloaded yet: ${error}. Minos keeps retrying.`,
        retired: `The publisher no longer serves this list (${error}). Replace it with another from the catalog.`,
        unauthorized: `The publisher now refuses the download (${error}). The feed may need an account or key.`,
        suspicious: `${error}.`,
        disabled: 'This list is switched off and not blocking.',
      })[state] ?? error,
    catalogTitle: 'Choose your blocklists',
    catalogHint: 'curated, verified lists — one click to subscribe',
    catalogAdd: 'Add',
    catalogAdded: 'Added ✓',
    tiers: {
      balanced: { label: 'Balanced', hint: 'recommended — blocks ads & tracking with very low breakage' },
      strict: { label: 'Strict', hint: 'wider nets; keep an eye on the Query Log and allow what breaks' },
      security: { label: 'Security', hint: 'malware & phishing feeds — protection, not ad blocking' },
      family: { label: 'Family', hint: 'content you choose to keep off the network, not threats' },
    },
    serviceShuttleAvailable: 'Available',
    serviceShuttleAllAdded: 'All added.',
    serviceShuttleMoveIn: (label: string) => `add ${label}`,
    serviceShuttleMoveOut: (label: string) => `remove ${label}`,
    serviceDomains: (n: number) => `${n} domain${n === 1 ? '' : 's'}`,
    customBadge: 'yours',
    customBadgeTitle: 'A custom service you defined — manage it under "Your custom services".',
    customManage: 'Your custom services',
    customManageHint: 'define your own bundle of domains, then block or allow it like any service',
    customAdd: 'Add a custom service',
    customLabelLabel: 'Name',
    customLabelPlaceholder: 'e.g. School portal',
    customNameLabel: 'ID (optional)',
    customNamePlaceholder: 'derived from the name',
    customDomainsLabel: 'Domains — one per line or comma-separated',
    customDomainsPlaceholder: 'mygame.example\ncdn.mygame.example',
    customAllowExtraLabel: 'Extra hosts allowed only while the service is allowed (optional)',
    customAllowExtraPlaceholder: 'sign-in or CDN hosts the service needs to work',
    customCreate: 'Create service',
    customSave: 'Save changes',
    customCancel: 'Cancel',
    customEdit: 'Edit',
    customEditing: (label: string) => `Editing "${label}"`,
    customSharedNote:
      'Custom services are shared: the same bundle can be blocked or allowed above, and used by any group.',
    customConfirmDelete: (label: string) =>
      `Remove the custom service "${label}"? Anything blocking or allowing it — above or in any group — stops applying immediately.`,
  },

  domains: {
    checkTitle: 'Check a domain',
    checkHint: 'see whether it is blocked, and which rule decides',
    checkPlaceholder: 'example.com',
    checkButton: 'Check',
    checkBlocked: (list: string, rule: string) => `Blocked — matched "${rule}" in ${list}.`,
    checkAllowedByRule: (rule: string) => `Allowed — by "${rule}".`,
    checkAllowed: 'Allowed — no rule matches; it is looked up upstream as normal.',
    pardonsTitle: 'Allowed domains',
    pardonsHint: 'always allowed, with their subdomains, whatever the blocklists say',
    pardonsEmpty: 'No allowed domains yet.',
    servicesTitle: 'Services',
    servicesHint: 'block or allow a whole service — every domain it uses — in one move',
    servicesScopeLabel: 'Services for',
    servicesScopeGlobal: 'Everyone (global)',
    servicesScopeNote:
      'Choose a group to set services for just those devices — a group can also carry a schedule, on the',
    servicesScopeNoteLink: 'Devices page.',
    sentencedServicesTitle: 'Blocked services',
    sentencedServicesHint: 'blocked for the devices selected above',
    sentencedServicesActive: 'Blocked',
    sentencedServicesEmpty: 'No services blocked. Add one from the left.',
    pardonedServicesTitle: 'Allowed services',
    pardonedServicesHint: 'always allowed for the devices selected above',
    pardonedServicesActive: 'Allowed',
    pardonedServicesEmpty: 'No services allowed. Add one from the left.',
    sentencesTitle: 'Blocked domains',
    sentencesHint: 'always blocked, with their subdomains',
    sentencesEmpty: 'No blocked domains yet.',
    addPlaceholder: 'domain to add…',
    localTitle: 'Local DNS',
    localHint: 'names answered here — never blocked, never sent upstream',
    localEmpty: 'No local records. Add one to name a device or service on your network.',
    localNamePlaceholder: 'nas.home.lab or *.home.lab',
    localValuePlaceholder: (t: string) =>
      t === 'CNAME' ? 'target.home.lab' : 'address (comma-separate several)',
    localAdd: 'Add record',
    localEdit: 'Edit',
    localEditTitle: 'change this record’s address',
    localEditValueLabel: (name: string) => `new address for ${name}`,
    localSave: 'Save',
    localCancel: 'Cancel',
    localWildcardNote: 'A leading *. matches all subdomains. Address records also answer reverse (PTR) lookups.',
  },

  devices: {
    title: 'Devices',
    subtitle: 'every client that queries this resolver',
    empty: 'No devices yet. Point something at this resolver and it appears here.',
    neverSeen: 'never',
    privateMAC: 'Private address',
    privateMACTitle:
      'This device uses a randomized (locally administered) MAC address — a privacy feature of iOS, Android, and Windows — so no manufacturer registry can name it.',
    hintTitle:
      'A guess from this device’s traffic patterns or DHCP fingerprint — not something the device reported. Any real identity source replaces it.',
    nameSource: (source: string): string => {
      const labels: Record<string, string> = {
        ptr: 'reverse DNS',
        netbios: 'NetBIOS',
        mdns: 'mDNS',
        ssdp: 'UPnP',
        dhcp: 'DHCP',
      };
      return `name discovered via ${labels[source] ?? source}`;
    },
    blockAction: 'Block DNS',
    blockHint: 'refuse all DNS from this device',
    unblockAction: 'Unblock',
    blockedBadge: 'DNS blocked',
    namePlaceholder: 'label…',
    groupDefault: 'default (full rules)',
    groupsTitle: 'Groups',
    groupsHint: 'device policies — assign devices above',
    groupsEmpty: 'No groups. Devices follow the default rules until you create one.',
    modeFilter: 'filter — default rules plus this group’s lists',
    modeBypass: 'bypass — no filtering at all',
    modeBlock: 'block — refuse all DNS',
    addTitle: 'Create a group',
    extraAllow: 'Extra allowed domains',
    extraDeny: 'Extra blocked domains',
    groupServicesSummary: (blocked: number, allowed: number) =>
      `Services — ${blocked} blocked, ${allowed} allowed.`,
    groupServicesManage: 'Manage on Allow & Block',
    groupSafeSearch: 'Enforce Safe Search for this group',
    scheduleSummary: 'Schedule',
    scheduleOn: (start: string, end: string) => `active ${start}–${end}`,
    scheduleNote:
      'The group applies only during these hours (server time); outside them its devices follow the default rules. An end before the start runs past midnight.',
    scheduleSet: 'Set schedule',
    scheduleClear: 'Remove schedule',
    listPlaceholder: 'comma-separated domains',
    confirmDelete: (name: string) =>
      `Delete the group "${name}"? Its devices fall back to the default rules.`,
    forget: 'Forget',
    forgetHint: 'remove the saved label/group for this device',
    viewInDocket: 'See this device’s queries in the Query Log',
    activityHint: 'show what this device queried',
    activityTotals: (total: number, blocked: number) =>
      `${total.toLocaleString()} queries, ${blocked.toLocaleString()} blocked`,
    activityAllowed: 'Top allowed domains',
    activityBlocked: 'Top blocked domains',
    activityEmpty: 'Nothing in this window.',
    activityLoading: 'Loading…',
    openDevice: 'Open this device’s page',
  },

  device: {
    notFound: 'No device with this address has been seen.',
    backToList: 'All devices',
    identityTitle: 'Identity',
    ipsLabel: 'Addresses',
    primaryTag: 'current',
    macLabel: 'MAC',
    vendorLabel: 'Vendor',
    modelLabel: 'Model',
    hostnameLabel: 'Hostname',
    hintLabel: 'Guess',
    firstSeenLabel: 'First seen',
    lastSeenLabel: 'Last seen',
    queriesLabel: 'Queries',
    notesTitle: 'Notes',
    notesPlaceholder: 'Anything worth remembering about this device — owner, room, model…',
    notesSaved: 'Notes saved.',
    windowStart: (iso: string) =>
      `· counted in whole days, since ${new Date(iso).toLocaleDateString(undefined, { day: 'numeric', month: 'short' })}`,
    activityWindow: (hours: number): string => {
      if (hours >= 2160) return 'activity, last 90 days';
      if (hours >= 720) return 'activity, last 30 days';
      if (hours >= 168) return 'activity, last 7 days';
      return `activity, last ${hours} hours`;
    },
    historyTitle: 'Query history',
    historyHint: 'the full retained log for this device, every address included',
    historyEmpty: 'No persisted queries for this device.',
    loadOlder: 'Load older',
    loading: 'Loading…',
  },

  settings: {
    title: 'Settings',
    tabs: [
      { key: 'dns', label: 'DNS' },
      { key: 'filtering', label: 'Filtering' },
      { key: 'notifications', label: 'Notifications' },
      { key: 'discovery', label: 'Device discovery' },
      { key: 'access', label: 'Access & security' },
      { key: 'system', label: 'System' },
      { key: 'backup', label: 'Backup & import' },
    ] as const,
    logLevelTitle: 'Application log',
    logLevelHint: 'how much Minos records about its own activity',
    logLevelNote:
      'Applies at once, to the Logs page and the system journal. Debug records every query — use it briefly, when chasing a problem.',
    logLevelLink: 'Open Logs',
    save: 'Save changes',
    saved: 'Settings saved and applied — no restart needed.',
    upstreamsTitle: 'Upstream resolvers',
    upstreamsHint: 'where Minos looks up allowed names, tried in order — the first is the primary',
    upstreamPreset: 'Known resolver…',
    upstreamCustom: 'Custom',
    upstreamHealthy: (requests: number, failures: number, avgMs: number) =>
      `Healthy — ${requests.toLocaleString()} queries, ${failures.toLocaleString()} failed, ~${Math.round(avgMs)} ms average since the last restart.`,
    // Failing never claims the others are answering: they may be failing too.
    upstreamFailing: (lastSuccess: string, error: string) =>
      `Failing — ${lastSuccess ? `last answered ${lastSuccess}` : 'has not answered a single query since the last restart'}.` +
      `${error ? ` Last error: ${error}.` : ''} Minos skips it and retries every 30 seconds.`,
    upstreamUnused:
      'Not used yet — the resolvers above it have answered everything, so this one has not been tested. It is tried if they fail.',
    upstreamUnsaved: 'Not saved yet — save to start using this resolver.',
    dnssecLabel: 'DNSSEC validation',
    dnssecOff: 'Off — trust the upstream resolvers (default)',
    dnssecPermissive: 'Permissive — validate and count, never block',
    dnssecEnforce: 'Enforce — refuse answers that fail validation',
    dnssecNote:
      'Minos checks signed answers against the DNS root of trust itself, instead of taking the upstream’s word for it. Start with permissive and watch the counters; enforce answers "server failure" for forged responses. Answers that cannot be checked either way still pass, so a resolver without DNSSEC support degrades visibility, never resolution.',
    blockingTitle: 'Blocking',
    blockingModeZeroIp: 'Answer with 0.0.0.0 (recommended: fails fast, quiet)',
    blockingModeNxdomain: 'Answer NXDOMAIN (domain does not exist)',
    blockTTL: 'Remember blocks for (seconds)',
    blockTTLHint: 'how long a device caches a blocked answer before asking again (the TTL)',
    safeSearch: 'Enforce Safe Search for everyone',
    safeSearchHint:
      'Google, Bing, DuckDuckGo, and YouTube (moderate) answer with their enforced-safe variants. Devices in bypass groups are exempt.',
    firefoxCanary: 'Keep Firefox on this resolver',
    firefoxCanaryHint:
      "Answers Firefox's use-application-dns.net check with NXDOMAIN — Mozilla's documented signal that this network filters DNS — so Firefox keeps using Minos instead of switching itself to its built-in DNS over HTTPS. Untick to let Firefox decide.",
    privateRelay: 'Block iCloud Private Relay',
    privateRelayHint:
      'Denies the mask.icloud.com hostnames Apple documents for exactly this. Apple devices show "Private Relay is unavailable on this network" and use normal DNS, so Minos can filter their queries. A real privacy trade — off by default.',
    cacheTitle: 'Response cache',
    cacheHint: 'answers repeat queries from memory instead of asking upstream',
    cacheEnabled: 'Enable the response cache',
    cacheMaxEntries: 'Max cached answers',
    cacheMinTTL: 'Minimum TTL (seconds)',
    cacheMinTTLHint: 'short-lived answers are kept at least this long',
    cacheMaxTTL: 'Maximum TTL (seconds)',
    cacheMaxTTLHint: 'no answer is served from cache longer than this',
    cacheServeStale: 'Serve stale answers while refreshing',
    cacheServeStaleHint:
      'if an upstream is slow or down, answer from the expired entry (up to 6 hours old) and fetch a fresh one in the background',
    routesTitle: 'Conditional forwarding',
    routesHint: 'send certain domains to a different resolver (e.g. your router)',
    routesEmpty: 'No routes. All queries use the upstreams above.',
    routesDomains: 'domains (comma-separated)',
    routesDomainsPlaceholder: 'lan, home.arpa, 1.168.192.in-addr.arpa',
    routesNote:
      'Matches the domains and all their subdomains. A route is authoritative: if its resolver fails, the query fails rather than falling back.',
    routesAdd: 'Add route',
    listsTitle: 'Blocklist refresh',
    refreshInterval: 'Refresh interval',
    refreshIntervalHint: 'e.g. 24h, 12h, 6h — minimum 5m',
    querylogTitle: 'Query log',
    retention: 'Keep history (days)',
    ringSize: 'Recent queries kept in memory',
    memoryMB: 'Query log memory limit (MB)',
    memoryMBHint:
      'The most RAM the query log database keeps for caching and for working out searches and statistics. Work that needs more spills to a temporary file on disk — slower, but it never takes more memory. History itself always lives on disk. Applies immediately.',
    procRSS: (rss: string) => `Minos is using ${rss} of memory in total right now.`,
    ephemeralNote: 'Disk logging is off (ephemeral mode) — set in the config file.',
    dbPathNote: (p: string) => `History is stored in ${p}.`,
    apiTitle: 'API access',
    tokenSet: 'An API token is set. The UI and CLI must present it.',
    tokenUnset: 'No API token — anyone on the LAN can use this interface.',
    tokenPlaceholder: 'new API token',
    tokenSave: 'Set token',
    tokenClear: 'Remove token',
    tokenConfirmClear: 'Remove the API token? Anyone on the LAN will be able to manage Minos.',
    listenNote: (dns: string, api: string) =>
      `DNS listens on ${dns}, this interface on ${api}. Listen addresses are set in the config file and need a restart.`,
    notificationsTitle: 'Notifications',
    notificationsHint: 'get told when something on your network changes',
    notificationsNote:
      'Low-volume events only, and nothing is sent unless a destination is set. Each one says what happened and what to do, and tapping it opens the right page in Minos.',
    uiURL: 'Link address',
    uiURLHint: 'where tapping a notification takes you — the address you open Minos at',
    uiURLNote: (def: string) =>
      def
        ? `Leave blank to use ${def}. Set it if you reach Minos by another name, such as http://minos.lan:8080.`
        : 'Minos could not work out its own address; set this so notifications can link back to it.',
    eventsTitle: 'What to send',
    events: [
      { types: ['device_new'], label: 'New devices', hint: 'a device makes its first DNS query (named once discovery finds it; a burst arrives as one message)' },
      { types: ['list_failing'], label: 'Blocklist problems', hint: 'a list is retired, refuses downloads, never loaded, or has stopped updating' },
      { types: ['upstream_sick'], label: 'Resolver problems', hint: 'an upstream resolver stops answering (repeat failures within 30 minutes stay quiet)' },
      { types: ['list_recovered', 'upstream_recovered'], label: 'Recoveries', hint: 'a list or resolver that was reported failing works again' },
      { types: ['update_available'], label: 'Minos updates', hint: 'a new release is out (needs the update check on)' },
      { types: ['acme_renewal_failed'], label: 'Certificate problems', hint: 'the DoT/DoH certificate cannot be renewed' },
    ] as { types: string[]; label: string; hint: string }[],
    dnsDownNote: '"DNS is down" — every resolver failing — is always sent, at urgent priority.',
    quietTitle: 'Quiet hours',
    quietHint: 'notifications still arrive, silently — except "DNS is down"',
    quietFrom: 'From',
    quietTo: 'to',
    testAction: 'Send test notification',
    testSending: 'Sending…',
    testResult: (res: Record<string, string>) =>
      Object.entries(res)
        .map(([sink, r]) => `${sink === 'ntfy' ? 'ntfy' : 'Webhook'}: ${r === 'ok' ? 'delivered' : r}`)
        .join(' · '),
    testSaveFirst: 'Save first — the test uses the saved settings.',
    webhookURL: 'Webhook URL',
    webhookHint: 'each event is POSTed as JSON',
    ntfyURL: 'ntfy topic URL',
    ntfyHint: 'e.g. https://ntfy.sh/my-minos-topic, or your self-hosted ntfy',
    ntfyToken: 'ntfy access token',
    ntfyTokenHint: 'only for protected topics; leave blank otherwise',
    digestTitle: 'Traffic digest',
    digestHint:
      'a summary of what your network did — totals, top blocked, busiest client — sent to the destinations above',
    digestCadence: 'Send',
    digestDay: 'On',
    digestTime: 'At',
    digestTimeHint: 'server time',
    discoveryTitle: 'Device discovery',
    discoveryHint: 'how Minos names the devices on your network',
    discoverySSDP: 'Ask UPnP devices to introduce themselves (SSDP)',
    discoverySSDPHint:
      'One small multicast search every few minutes, plus a description fetch from each responding device — names smart TVs, consoles, and IoT gear by friendly name, manufacturer, and model. Local network only; nothing leaves the LAN.',
    discoveryDHCP: 'Listen for DHCP introductions (Linux)',
    discoveryDHCPHint:
      'Devices announce their own name when they join the network or renew a lease. Minos only listens — it never answers, never hands out addresses, and steps aside if this machine runs a DHCP server.',
    updatesTitle: 'Updates',
    updateCheck: 'Check for new releases once a day',
    updateCheckHint:
      'Asks GitHub for the latest release tag — nothing about you or your network is sent, only the request itself. Off unless you turn it on.',
    updateAvailable: (v: string) => `v${v} available`,
    backupTitle: 'Backup & restore',
    backupHint: 'download or restore the full configuration',
    backupButton: 'Export configuration',
    restoreLabel: 'Restore from a backup',
    restoreHint: 'upload a minos.yaml exported earlier',
    restoreButton: 'Restore configuration',
    restoreConfirm:
      'Replace the entire running configuration with this backup? Listen addresses and query-log storage are kept from the current setup; everything else is overwritten.',
    restoreDone: 'Configuration restored and applied.',
    importTitle: 'Import from Pi-hole or AdGuard Home',
    importHint: 'bring your existing setup across',
    importGravity: 'Pi-hole gravity.db',
    importCustomList: 'custom.list (Local DNS Records)',
    importAdguard: 'AdGuardHome.yaml',
    importOptional: 'optional',
    importButton: 'Import',
    importNote:
      'Imports only add — nothing already configured is removed, and duplicates are dropped. Regex rules and other unsupported entries are listed as skipped. Find gravity.db in /etc/pihole; AdGuardHome.yaml alongside the AdGuardHome binary.',
  },

  token: {
    prompt: 'This instance requires an API token.',
    placeholder: 'API token',
    submit: 'Unlock',
  },
};
