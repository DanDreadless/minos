<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import {
    api,
    exportConfig,
    setToken,
    type ConfigView,
    type ImportReport,
    type Upstream,
    type UpstreamHealth,
  } from '../lib/api';
  import { copy } from '../lib/copy';
  import { fmtBytes, fmtLogTime } from '../lib/format';
  import { resolverPresets, matchPreset } from '../lib/resolvers';
  import { notify, notifyError } from '../lib/toast';

  let cfg: ConfigView | null = null;

  // Editable working copies, initialised from the loaded config.
  let upstreams: Upstream[] = [];
  let dnssec: 'off' | 'permissive' | 'enforce' = 'off';
  let mode: string = 'zero_ip';
  let blockTTL = 60;
  let safeSearch = false;
  let firefoxCanary = true;
  let blockPrivateRelay = false;
  let refreshInterval = '24h';
  let retentionDays = 90;
  let ringSize = 10000;
  let memoryMB = 32;
  // Minos's own resident memory, so the cap can be judged against reality.
  let procRSS: number | undefined;
  let newToken = '';
  let cacheEnabled = true;
  let cacheMaxEntries = 10000;
  let cacheMinTTL = 10;
  let cacheMaxTTL = 3600;
  let cacheServeStale = true;
  let updateCheck = false;
  let webhookURL = '';
  let ntfyURL = '';
  let ntfyToken = '';
  let ntfyTokenSet = false;
  let digest: 'off' | 'daily' | 'weekly' = 'off';
  let digestTime = '09:00';
  let digestDay = 'monday';
  let uiURL = '';
  let uiURLDefault = '';
  let quiet = false;
  let quietFrom = '22:00';
  let quietTo = '07:00';
  let mute: string[] = [];
  let testing = false;
  let testResult = '';
  let ssdp = true;
  let dhcpListen = true;

  interface RouteRow {
    domains: string;
    address: string;
    protocol: Upstream['protocol'];
  }
  let routeRows: RouteRow[] = [];

  function initFrom(c: ConfigView): void {
    cfg = c;
    upstreams = c.dns.upstreams.map((u) => ({ ...u }));
    savedUpstreams = new Set(c.dns.upstreams.map((u) => `${u.protocol} ${u.address}`));
    dnssec = c.dns.dnssec;
    mode = c.blocking.mode;
    safeSearch = c.blocking.safe_search;
    firefoxCanary = !c.dns.allow_firefox_doh;
    blockPrivateRelay = c.blocking.block_icloud_private_relay;
    blockTTL = c.dns.block_ttl;
    refreshInterval = c.lists.refresh_interval;
    retentionDays = c.querylog.retention_days;
    ringSize = c.querylog.ring_size;
    memoryMB = c.querylog.memory_mb;
    cacheEnabled = c.dns.cache.enabled;
    cacheMaxEntries = c.dns.cache.max_entries;
    cacheMinTTL = c.dns.cache.min_ttl;
    cacheMaxTTL = c.dns.cache.max_ttl;
    cacheServeStale = c.dns.cache.serve_stale;
    updateCheck = c.update_check;
    webhookURL = c.notifications.webhook_url;
    ntfyURL = c.notifications.ntfy_url;
    ntfyTokenSet = c.notifications.ntfy_token_set;
    ntfyToken = '';
    digest = c.notifications.digest;
    digestTime = c.notifications.digest_time;
    digestDay = c.notifications.digest_day;
    uiURL = c.notifications.ui_url;
    uiURLDefault = c.notifications.ui_url_default;
    quiet = c.notifications.quiet_hours !== '';
    if (quiet) [quietFrom, quietTo] = c.notifications.quiet_hours.split('-');
    mute = [...c.notifications.mute];
    ssdp = c.discovery.ssdp;
    dhcpListen = c.discovery.dhcp_listen;
    routeRows = c.dns.routes.map((r) => ({
      domains: r.domains.join(', '),
      address: r.upstream.address,
      protocol: r.upstream.protocol,
    }));
  }

  async function load(): Promise<void> {
    try {
      initFrom(await api.getConfig());
    } catch (e) {
      notifyError(e);
    }
  }

  // An event group is on unless all its types are muted. Both take the mute
  // list as a parameter so the checkboxes re-render when it changes.
  function eventOn(types: string[], muted: string[]): boolean {
    return !types.every((t) => muted.includes(t));
  }

  function setEvent(muted: string[], types: string[], on: boolean): string[] {
    const rest = muted.filter((t) => !types.includes(t));
    return on ? rest : [...rest, ...types];
  }

  async function sendTest(): Promise<void> {
    testing = true;
    testResult = '';
    try {
      testResult = copy.settings.testResult(await api.testNotification());
    } catch (e) {
      notifyError(e);
    } finally {
      testing = false;
    }
  }

  async function save(upd: Parameters<typeof api.updateConfig>[0]): Promise<void> {
    try {
      initFrom(await api.updateConfig(upd));
      notify(copy.settings.saved);
    } catch (e) {
      notifyError(e);
    }
  }

  const saveUpstreams = () => save({ dns: { upstreams, dnssec } });
  const saveBlocking = () =>
    save({
      blocking: {
        mode,
        safe_search: safeSearch,
        block_icloud_private_relay: blockPrivateRelay,
      },
      dns: { block_ttl: blockTTL, allow_firefox_doh: !firefoxCanary },
    });
  const saveLists = () => save({ lists: { refresh_interval: refreshInterval } });
  const saveCache = () =>
    save({
      dns: {
        cache: {
          enabled: cacheEnabled,
          max_entries: cacheMaxEntries,
          min_ttl: cacheMinTTL,
          max_ttl: cacheMaxTTL,
          serve_stale: cacheServeStale,
        },
      },
    });
  const saveQuerylog = () =>
    save({ querylog: { retention_days: retentionDays, ring_size: ringSize, memory_mb: memoryMB } });

  function addUpstream(): void {
    upstreams = [...upstreams, { address: '', protocol: 'doh' }];
  }

  // applyPreset fills a row from a chosen known resolver; picking "Custom"
  // leaves the row untouched so it can be typed by hand.
  function applyPreset(i: number, ev: Event): void {
    const id = (ev.target as HTMLSelectElement).value;
    const preset = resolverPresets.find((p) => p.id === id);
    if (!preset) return;
    const next = [...upstreams];
    next[i] = { ...preset.upstream };
    upstreams = next;
  }

  // A pasted https:// URL can only be DoH; switch the protocol to match
  // rather than let the row save as DoT and fail every exchange (the server
  // now rejects that combination, but catching it here saves a round trip).
  function matchProtocol<T extends { address: string; protocol: Upstream['protocol'] }>(row: T): void {
    if (/^https:\/\//i.test(row.address.trim()) && row.protocol !== 'doh') {
      row.protocol = 'doh';
      upstreams = upstreams;
      routeRows = routeRows;
    }
  }

  // The first two rows are the primary and secondary resolvers; the rest are
  // ordered fallbacks.
  function orderLabel(i: number): string {
    if (i === 0) return 'Primary';
    if (i === 1) return 'Secondary';
    return `${i + 1}.`;
  }

  const saveRoutes = () =>
    save({
      dns: {
        routes: routeRows.map((r) => ({
          domains: r.domains
            .split(/[\s,]+/)
            .map((s) => s.trim())
            .filter(Boolean),
          upstream: { address: r.address.trim(), protocol: r.protocol },
        })),
      },
    });

  function addRoute(): void {
    routeRows = [...routeRows, { domains: '', address: '', protocol: 'udp' }];
  }

  function removeRoute(i: number): void {
    routeRows = routeRows.filter((_, idx) => idx !== i);
  }

  function removeUpstream(i: number): void {
    upstreams = upstreams.filter((_, idx) => idx !== i);
  }

  function moveUpstream(i: number, delta: number): void {
    const j = i + delta;
    if (j < 0 || j >= upstreams.length) return;
    const next = [...upstreams];
    [next[i], next[j]] = [next[j], next[i]];
    upstreams = next;
  }

  async function saveToken(): Promise<void> {
    const t = newToken.trim();
    if (!t) return;
    try {
      const updated = await api.updateConfig({ api: { token: t } });
      // Adopt the new token locally before the next request needs it.
      setToken(t);
      initFrom(updated);
      newToken = '';
      notify(copy.settings.saved);
    } catch (e) {
      notifyError(e);
    }
  }

  async function clearToken(): Promise<void> {
    if (!window.confirm(copy.settings.tokenConfirmClear)) return;
    try {
      const updated = await api.updateConfig({ api: { token: '' } });
      setToken('');
      initFrom(updated);
      notify(copy.settings.saved);
    } catch (e) {
      notifyError(e);
    }
  }

  async function doExport(): Promise<void> {
    try {
      await exportConfig();
    } catch (e) {
      notifyError(e);
    }
  }

  // --- migration & restore uploads ---
  let piholeGravity: FileList | null = null;
  let piholeCustom: FileList | null = null;
  let adguardFile: FileList | null = null;
  let restoreFile: FileList | null = null;
  let importReport: ImportReport | null = null;
  let importing = false;

  function describeReport(r: ImportReport): string {
    return `Imported ${r.lists} blocklists, ${r.allow} allowed, ${r.deny} blocked, ${r.local_records} local records, ${r.services} services.`;
  }

  async function doImportPihole(): Promise<void> {
    if (!piholeGravity?.[0]) return;
    importing = true;
    importReport = null;
    try {
      importReport = await api.importPihole(piholeGravity[0], piholeCustom?.[0]);
      notify(describeReport(importReport));
      await load();
    } catch (e) {
      notifyError(e);
    } finally {
      importing = false;
    }
  }

  async function doImportAdGuard(): Promise<void> {
    if (!adguardFile?.[0]) return;
    importing = true;
    importReport = null;
    try {
      importReport = await api.importAdGuard(adguardFile[0]);
      notify(describeReport(importReport));
      await load();
    } catch (e) {
      notifyError(e);
    } finally {
      importing = false;
    }
  }

  async function doRestore(): Promise<void> {
    if (!restoreFile?.[0]) return;
    if (!window.confirm(copy.settings.restoreConfirm)) return;
    importing = true;
    try {
      initFrom(await api.importConfig(restoreFile[0]));
      restoreFile = null;
      notify(copy.settings.restoreDone);
    } catch (e) {
      notifyError(e);
    } finally {
      importing = false;
    }
  }

  // --- upstream health lights ---
  // Live breaker state, polled while the page is open. Keyed by address —
  // the backend's counters use the configured address as their name.
  let upstreamHealth: Record<string, UpstreamHealth> = {};
  let healthTimer: ReturnType<typeof setInterval> | null = null;

  async function loadUpstreamHealth(): Promise<void> {
    try {
      const list = await api.upstreams();
      upstreamHealth = Object.fromEntries(list.map((h) => [h.address, h]));
    } catch {
      // Decorative: the settings form works without the lights.
    }
  }

  // The light shows the server's verdict, never a guess. Green only for a
  // resolver that has actually answered: the old two-state light painted an
  // idle backup green, which is how one that had never answered a query sat
  // green for 49 days. An unused backup gets a hollow ring — untested, not
  // healthy — and a row typed but not saved has no light at all.
  //
  // The health map and the saved set are parameters, not globals read inside
  // the functions, on purpose: Svelte re-evaluates a template expression only
  // when a variable *named in it* changes. Read from closure, the lights
  // froze on their first render — before the health data had arrived — so
  // every row showed "not used yet" (field bug, v0.22.0; the old two-state
  // light defaulted to green, which is what hid it).
  type Light = 'healthy' | 'failing' | 'unused' | 'unsaved';
  let savedUpstreams = new Set<string>();

  function healthState(
    u: Upstream,
    health: Record<string, UpstreamHealth>,
    saved: Set<string>,
  ): Light {
    if (!saved.has(`${u.protocol} ${u.address}`)) return 'unsaved';
    return health[u.address]?.state ?? 'unused';
  }

  function healthTitle(
    u: Upstream,
    health: Record<string, UpstreamHealth>,
    saved: Set<string>,
  ): string {
    const h = health[u.address];
    switch (healthState(u, health, saved)) {
      case 'unsaved':
        return copy.settings.upstreamUnsaved;
      case 'failing':
        return copy.settings.upstreamFailing(
          h?.last_success ? fmtLogTime(h.last_success) : '',
          h?.last_error ?? '',
        );
      case 'healthy':
        return copy.settings.upstreamHealthy(h!.requests, h!.failures, h!.avg_ms);
      default:
        return copy.settings.upstreamUnused;
    }
  }

  onMount(() => {
    void load();
    void loadUpstreamHealth();
    void api
      .host()
      .then((h) => (procRSS = h.sample?.proc_rss))
      .catch(() => {});
    healthTimer = setInterval(loadUpstreamHealth, 10000);
  });

  onDestroy(() => {
    if (healthTimer) clearInterval(healthTimer);
  });
</script>

<h1>{copy.settings.title}</h1>

{#if cfg}
  <section class="card">
    <h2>{copy.settings.upstreamsTitle} <small>{copy.settings.upstreamsHint}</small></h2>
    {#each upstreams as u, i (i)}
      <div class="upstream-row">
        <span
          class="health-dot {healthState(u, upstreamHealth, savedUpstreams)}"
          title={healthTitle(u, upstreamHealth, savedUpstreams)}
          role="img"
          aria-label={healthTitle(u, upstreamHealth, savedUpstreams)}
        ></span>
        <span class="order">{orderLabel(i)}</span>
        <select
          class="preset"
          title="pick a known resolver"
          value={matchPreset(u)}
          on:change={(e) => applyPreset(i, e)}
        >
          <option value="">{copy.settings.upstreamCustom}</option>
          {#each resolverPresets as p (p.id)}
            <option value={p.id}>{p.label}</option>
          {/each}
        </select>
        <input
          class="grow"
          placeholder={u.protocol === 'doh' ? 'https://resolver/dns-query' : 'host:port'}
          bind:value={u.address}
          on:input={() => matchProtocol(u)}
        />
        <select bind:value={u.protocol} title="protocol">
          <option value="doh">DoH (encrypted, HTTPS)</option>
          <option value="dot">DoT (encrypted, TLS)</option>
          <option value="udp">UDP (plaintext)</option>
          <option value="tcp">TCP (plaintext)</option>
        </select>
        <button title="move up" disabled={i === 0} on:click={() => moveUpstream(i, -1)}>↑</button>
        <button title="move down" disabled={i === upstreams.length - 1} on:click={() => moveUpstream(i, 1)}>
          ↓
        </button>
        <button class="danger" title="remove" disabled={upstreams.length === 1} on:click={() => removeUpstream(i)}>
          ✕
        </button>
      </div>
    {/each}
    <label class="field">
      <span>{copy.settings.dnssecLabel}</span>
      <select bind:value={dnssec}>
        <option value="off">{copy.settings.dnssecOff}</option>
        <option value="permissive">{copy.settings.dnssecPermissive}</option>
        <option value="enforce">{copy.settings.dnssecEnforce}</option>
      </select>
    </label>
    <p class="note">{copy.settings.dnssecNote}</p>
    <div class="section-actions">
      <button on:click={addUpstream}>Add upstream</button>
      <button class="primary" on:click={saveUpstreams}>{copy.settings.save}</button>
    </div>
  </section>

  <section class="card">
    <h2>{copy.settings.blockingTitle}</h2>
    <label class="radio">
      <input type="radio" bind:group={mode} value="zero_ip" />
      {copy.settings.blockingModeZeroIp}
    </label>
    <label class="radio">
      <input type="radio" bind:group={mode} value="nxdomain" />
      {copy.settings.blockingModeNxdomain}
    </label>
    <label class="field">
      <span>{copy.settings.blockTTL} <small>{copy.settings.blockTTLHint}</small></span>
      <input type="number" min="0" max="86400" bind:value={blockTTL} />
    </label>
    <label class="radio safe-search">
      <input type="checkbox" bind:checked={safeSearch} />
      {copy.settings.safeSearch}
    </label>
    <p class="note">{copy.settings.safeSearchHint}</p>
    <label class="radio safe-search">
      <input type="checkbox" bind:checked={firefoxCanary} />
      {copy.settings.firefoxCanary}
    </label>
    <p class="note">{copy.settings.firefoxCanaryHint}</p>
    <label class="radio safe-search">
      <input type="checkbox" bind:checked={blockPrivateRelay} />
      {copy.settings.privateRelay}
    </label>
    <p class="note">{copy.settings.privateRelayHint}</p>
    <div class="section-actions">
      <button class="primary" on:click={saveBlocking}>{copy.settings.save}</button>
    </div>
  </section>

  <section class="card">
    <h2>{copy.settings.routesTitle} <small>{copy.settings.routesHint}</small></h2>
    {#if routeRows.length === 0}
      <p class="note">{copy.settings.routesEmpty}</p>
    {/if}
    {#each routeRows as r, i (i)}
      <div class="upstream-row">
        <input
          class="grow"
          placeholder={copy.settings.routesDomainsPlaceholder}
          title={copy.settings.routesDomains}
          bind:value={r.domains}
        />
        <span class="route-arrow" aria-hidden="true">→</span>
        <input
          placeholder={r.protocol === 'doh' ? 'https://resolver/dns-query' : 'host:port'}
          bind:value={r.address}
          on:input={() => matchProtocol(r)}
        />
        <select bind:value={r.protocol} title="protocol">
          <option value="udp">UDP</option>
          <option value="tcp">TCP</option>
          <option value="dot">DoT</option>
          <option value="doh">DoH</option>
        </select>
        <button class="danger" title="remove route" on:click={() => removeRoute(i)}>✕</button>
      </div>
    {/each}
    <p class="note">{copy.settings.routesNote}</p>
    <div class="section-actions">
      <button on:click={addRoute}>{copy.settings.routesAdd}</button>
      <button class="primary" on:click={saveRoutes}>{copy.settings.save}</button>
    </div>
  </section>

  <section class="card">
    <h2>{copy.settings.cacheTitle} <small>{copy.settings.cacheHint}</small></h2>
    <label class="radio">
      <input type="checkbox" bind:checked={cacheEnabled} />
      {copy.settings.cacheEnabled}
    </label>
    {#if cacheEnabled}
      <label class="field">
        <span>{copy.settings.cacheMaxEntries}</span>
        <input type="number" min="100" max="1000000" step="100" bind:value={cacheMaxEntries} />
      </label>
      <label class="field">
        <span>{copy.settings.cacheMinTTL} <small>{copy.settings.cacheMinTTLHint}</small></span>
        <input type="number" min="0" max="86400" bind:value={cacheMinTTL} />
      </label>
      <label class="field">
        <span>{copy.settings.cacheMaxTTL} <small>{copy.settings.cacheMaxTTLHint}</small></span>
        <input type="number" min="1" max="604800" bind:value={cacheMaxTTL} />
      </label>
      <label class="radio">
        <input type="checkbox" bind:checked={cacheServeStale} />
        {copy.settings.cacheServeStale}
      </label>
      <p class="note">{copy.settings.cacheServeStaleHint}</p>
    {/if}
    <div class="section-actions">
      <button class="primary" on:click={saveCache}>{copy.settings.save}</button>
    </div>
  </section>

  <section class="card">
    <h2>{copy.settings.listsTitle}</h2>
    <label class="field">
      <span>{copy.settings.refreshInterval} <small>{copy.settings.refreshIntervalHint}</small></span>
      <input bind:value={refreshInterval} size="8" />
    </label>
    <div class="section-actions">
      <button class="primary" on:click={saveLists}>{copy.settings.save}</button>
    </div>
  </section>

  <section class="card">
    <h2>{copy.settings.querylogTitle}</h2>
    {#if cfg.querylog.ephemeral}
      <p class="note">{copy.settings.ephemeralNote}</p>
    {:else}
      <p class="note">{copy.settings.dbPathNote(cfg.querylog.db_path)}</p>
    {/if}
    <label class="field">
      <span>{copy.settings.retention}</span>
      <input type="number" min="1" max="3650" bind:value={retentionDays} />
    </label>
    <label class="field">
      <span>{copy.settings.ringSize}</span>
      <input type="number" min="100" max="1000000" step="100" bind:value={ringSize} />
    </label>
    {#if !cfg.querylog.ephemeral}
      <label class="field">
        <span>{copy.settings.memoryMB}</span>
        <input type="number" min="8" max="4096" step="8" bind:value={memoryMB} />
        <small>{copy.settings.memoryMBHint}</small>
      </label>
      {#if procRSS !== undefined}
        <p class="note">{copy.settings.procRSS(fmtBytes(procRSS))}</p>
      {/if}
    {/if}
    <div class="section-actions">
      <button class="primary" on:click={saveQuerylog}>{copy.settings.save}</button>
    </div>
  </section>

  <section class="card">
    <h2>{copy.settings.apiTitle}</h2>
    <p class="note">{cfg.api.token_set ? copy.settings.tokenSet : copy.settings.tokenUnset}</p>
    <div class="token-row">
      <input
        type="password"
        placeholder={copy.settings.tokenPlaceholder}
        bind:value={newToken}
        autocomplete="new-password"
      />
      <button class="primary" disabled={!newToken.trim()} on:click={saveToken}>
        {copy.settings.tokenSave}
      </button>
      {#if cfg.api.token_set}
        <button class="danger" on:click={clearToken}>{copy.settings.tokenClear}</button>
      {/if}
    </div>
    <p class="note">{copy.settings.listenNote(cfg.dns.listen, cfg.api.listen)}</p>
  </section>

  <section class="card">
    <h2>{copy.settings.notificationsTitle} <small>{copy.settings.notificationsHint}</small></h2>
    <p class="note">{copy.settings.notificationsNote}</p>
    <label class="field wide">
      <span>{copy.settings.webhookURL} <small>{copy.settings.webhookHint}</small></span>
      <input placeholder="https://…" bind:value={webhookURL} />
    </label>
    <label class="field wide">
      <span>{copy.settings.ntfyURL} <small>{copy.settings.ntfyHint}</small></span>
      <input placeholder="https://ntfy.sh/my-topic" bind:value={ntfyURL} />
    </label>
    <label class="field wide">
      <span>{copy.settings.ntfyToken} <small>{copy.settings.ntfyTokenHint}</small></span>
      <input
        type="password"
        placeholder={ntfyTokenSet ? '(token set — type to replace)' : ''}
        bind:value={ntfyToken}
        autocomplete="new-password"
      />
    </label>
    <label class="field wide">
      <span>{copy.settings.uiURL} <small>{copy.settings.uiURLHint}</small></span>
      <input placeholder={uiURLDefault || 'http://…'} bind:value={uiURL} />
    </label>
    <p class="note">{copy.settings.uiURLNote(uiURLDefault)}</p>

    <div class="digest">
      <h3>{copy.settings.eventsTitle}</h3>
      {#each copy.settings.events as ev (ev.label)}
        <label class="radio">
          <input
            type="checkbox"
            checked={eventOn(ev.types, mute)}
            on:change={(e) => (mute = setEvent(mute, ev.types, e.currentTarget.checked))}
          />
          {ev.label} <small class="hint">{ev.hint}</small>
        </label>
      {/each}
      <p class="note">{copy.settings.dnsDownNote}</p>
    </div>

    <div class="digest">
      <h3>{copy.settings.quietTitle} <small>{copy.settings.quietHint}</small></h3>
      <div class="digest-row">
        <label class="radio">
          <input type="checkbox" bind:checked={quiet} />
          {copy.settings.quietFrom}
        </label>
        <input type="time" bind:value={quietFrom} disabled={!quiet} required />
        <span>{copy.settings.quietTo}</span>
        <input type="time" bind:value={quietTo} disabled={!quiet} required />
      </div>
    </div>

    <div class="digest">
      <h3>{copy.settings.digestTitle} <small>{copy.settings.digestHint}</small></h3>
      <div class="digest-row">
        <label class="field">
          <span>{copy.settings.digestCadence}</span>
          <select bind:value={digest}>
            <option value="off">off</option>
            <option value="daily">daily</option>
            <option value="weekly">weekly</option>
          </select>
        </label>
        {#if digest !== 'off'}
          {#if digest === 'weekly'}
            <label class="field">
              <span>{copy.settings.digestDay}</span>
              <select bind:value={digestDay}>
                {#each ['monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday'] as d (d)}
                  <option value={d}>{d}</option>
                {/each}
              </select>
            </label>
          {/if}
          <label class="field">
            <span>{copy.settings.digestTime} <small>{copy.settings.digestTimeHint}</small></span>
            <input type="time" bind:value={digestTime} required />
          </label>
        {/if}
      </div>
    </div>
    <div class="section-actions">
      <button
        class="primary"
        on:click={() =>
          void save({
            notifications: {
              webhook_url: webhookURL.trim(),
              ntfy_url: ntfyURL.trim(),
              digest,
              digest_time: digestTime,
              digest_day: digestDay,
              ui_url: uiURL.trim(),
              quiet_hours: quiet ? `${quietFrom}-${quietTo}` : '',
              mute,
              // Only send a typed token; an untouched field never clobbers
              // the stored one. Clearing ntfy_url makes any token inert.
              ...(ntfyToken.trim() ? { ntfy_token: ntfyToken.trim() } : {}),
            },
          })}
      >
        {copy.settings.save}
      </button>
      <button on:click={sendTest} disabled={testing} title={copy.settings.testSaveFirst}>
        {testing ? copy.settings.testSending : copy.settings.testAction}
      </button>
      {#if testResult}<span class="test-result">{testResult}</span>{/if}
    </div>
  </section>

  <section class="card">
    <h2>{copy.settings.discoveryTitle} <small>{copy.settings.discoveryHint}</small></h2>
    <label class="radio">
      <input
        type="checkbox"
        bind:checked={ssdp}
        on:change={() => void save({ discovery: { ssdp } })}
      />
      {copy.settings.discoverySSDP}
    </label>
    <p class="note">{copy.settings.discoverySSDPHint}</p>
    <label class="radio">
      <input
        type="checkbox"
        bind:checked={dhcpListen}
        on:change={() => void save({ discovery: { dhcp_listen: dhcpListen } })}
      />
      {copy.settings.discoveryDHCP}
    </label>
    <p class="note">{copy.settings.discoveryDHCPHint}</p>
  </section>

  <section class="card">
    <h2>{copy.settings.updatesTitle}</h2>
    <label class="radio">
      <input
        type="checkbox"
        bind:checked={updateCheck}
        on:change={() => void save({ update_check: updateCheck })}
      />
      {copy.settings.updateCheck}
    </label>
    <p class="note">{copy.settings.updateCheckHint}</p>
  </section>

  <section class="card">
    <h2>{copy.settings.backupTitle} <small>{copy.settings.backupHint}</small></h2>
    <div class="section-actions">
      <button on:click={doExport}>{copy.settings.backupButton}</button>
    </div>
    <label class="field wide">
      <span>{copy.settings.restoreLabel} <small>{copy.settings.restoreHint}</small></span>
      <input type="file" accept=".yaml,.yml" bind:files={restoreFile} />
    </label>
    <div class="section-actions">
      <button class="danger" disabled={importing || !restoreFile?.[0]} on:click={doRestore}>
        {copy.settings.restoreButton}
      </button>
    </div>
  </section>

  <section class="card">
    <h2>{copy.settings.importTitle} <small>{copy.settings.importHint}</small></h2>
    <div class="import-grid">
      <div class="import-source">
        <h3>Pi-hole</h3>
        <label class="field wide">
          <span>{copy.settings.importGravity}</span>
          <input type="file" accept=".db,.sqlite,.sqlite3" bind:files={piholeGravity} />
        </label>
        <label class="field wide">
          <span>{copy.settings.importCustomList} <small>{copy.settings.importOptional}</small></span>
          <input type="file" accept=".list,.txt" bind:files={piholeCustom} />
        </label>
        <button class="primary" disabled={importing || !piholeGravity?.[0]} on:click={doImportPihole}>
          {copy.settings.importButton}
        </button>
      </div>
      <div class="import-source">
        <h3>AdGuard Home</h3>
        <label class="field wide">
          <span>{copy.settings.importAdguard}</span>
          <input type="file" accept=".yaml,.yml" bind:files={adguardFile} />
        </label>
        <button class="primary" disabled={importing || !adguardFile?.[0]} on:click={doImportAdGuard}>
          {copy.settings.importButton}
        </button>
      </div>
    </div>
    <p class="note">{copy.settings.importNote}</p>
    {#if importReport}
      <div class="import-report">
        <p>{describeReport(importReport)}</p>
        {#if importReport.skipped.length}
          <details>
            <summary>{importReport.skipped.length} item(s) skipped</summary>
            <ul>
              {#each importReport.skipped as reason}
                <li>{reason}</li>
              {/each}
            </ul>
          </details>
        {/if}
      </div>
    {/if}
  </section>
{/if}

<style>
  h2 small {
    color: var(--text-dim);
    font-size: 0.75rem;
    margin-left: 0.5rem;
    letter-spacing: 0;
  }

  section {
    margin-bottom: 1.25rem;
  }

  /* Pi-hole on top, AdGuard underneath — each a single inline row with its
     file fields and the import button lined up along the input baseline, the
     button pushed to the far end so it never crowds the inputs. */
  .import-grid {
    display: flex;
    flex-direction: column;
    gap: 1.25rem;
  }

  .import-source {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: 0.75rem 1.25rem;
  }

  .import-source h3 {
    margin: 0;
    font-size: 0.95rem;
    flex: 0 0 6.5rem;
    align-self: flex-end;
    padding-bottom: 0.4rem;
  }

  .import-source .field {
    margin-top: 0;
  }

  .import-source .field.wide input {
    max-width: 18rem;
  }

  .import-source .primary {
    align-self: flex-end;
  }

  .import-report {
    margin-top: 1rem;
    padding-top: 0.8rem;
    border-top: 1px solid var(--border);
  }

  .import-report ul {
    margin: 0.4rem 0 0;
    padding-left: 1.2rem;
    font-size: 0.8rem;
    color: var(--text-dim);
  }

  .import-report summary {
    cursor: pointer;
    color: var(--text-dim);
    font-size: 0.82rem;
  }

  .upstream-row {
    display: flex;
    gap: 0.5rem;
    align-items: center;
    flex-wrap: wrap;
    margin-bottom: 0.5rem;
  }

  .health-dot {
    width: 0.62rem;
    height: 0.62rem;
    border-radius: 50%;
    flex: none;
    cursor: help;
  }

  .health-dot.healthy {
    background: var(--allowed);
    box-shadow: 0 0 4px var(--allowed);
  }

  .health-dot.failing {
    background: var(--blocked);
    box-shadow: 0 0 4px var(--blocked);
  }

  /* Untested is not healthy: a hollow ring, not a green dot. */
  .health-dot.unused {
    border: 2px solid var(--text-dim);
  }

  .health-dot.unsaved {
    border: 2px dashed var(--border);
  }

  .upstream-row .preset {
    max-width: 15rem;
  }

  .order {
    color: var(--text-dim);
    width: 4.5rem;
    text-align: right;
    font-variant-numeric: tabular-nums;
  }

  .grow {
    flex: 1;
  }

  .section-actions {
    display: flex;
    gap: 0.6rem;
    margin-top: 0.9rem;
  }

  .radio {
    display: block;
    margin-bottom: 0.4rem;
    cursor: pointer;
  }

  .field {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin-top: 0.75rem;
  }

  .field span small {
    color: var(--text-dim);
    margin-left: 0.3rem;
  }

  .field input {
    width: 8rem;
  }

  .field.wide {
    align-items: flex-start;
    flex-direction: column;
    gap: 0.3rem;
  }

  .field.wide input {
    width: 100%;
    max-width: 26rem;
  }

  .note {
    color: var(--text-dim);
    font-size: 0.85rem;
  }

  .route-arrow {
    color: var(--text-dim);
  }

  .safe-search {
    margin-top: 0.75rem;
  }

  .token-row {
    display: flex;
    gap: 0.6rem;
    margin: 0.6rem 0;
  }

  .token-row input {
    max-width: 18rem;
    flex: 1;
  }

  .digest {
    margin-top: 1rem;
    padding-top: 0.8rem;
    border-top: 1px solid var(--border, rgba(127, 127, 127, 0.3));
  }

  .digest h3 {
    margin: 0 0 0.5rem;
    font-size: 0.92rem;
  }

  .digest h3 small {
    color: var(--text-dim);
    font-weight: normal;
    font-size: 0.78rem;
    margin-left: 0.5rem;
  }

  .digest-row {
    display: flex;
    flex-wrap: wrap;
    gap: 1rem;
    align-items: flex-end;
  }

  .radio small.hint {
    color: var(--text-dim);
    margin-left: 0.3rem;
  }

  .test-result {
    align-self: center;
    font-size: 0.82rem;
    color: var(--text-dim);
  }
</style>
