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
  import { confirmDialog } from '../lib/confirm';
  import HelpLink from '../lib/components/HelpLink.svelte';
  import { copy } from '../lib/copy';
  import { fmtBytes, fmtLogTime } from '../lib/format';
  import { resolverPresets, matchPreset } from '../lib/resolvers';
  import { currentParams, setLeaveGuard } from '../lib/router';
  import { notify, notifyError } from '../lib/toast';

  let cfg: ConfigView | null = null;

  // Editable working copies, initialised from the loaded config.
  let upstreams: Upstream[] = [];
  let dnssec: 'off' | 'permissive' | 'enforce' = 'off';
  let mode: string = 'zero_ip';
  let blockTTL = 60;
  let safeSearch = false;
  let firefoxCanary = true;
  let rebindProtect = false;
  let blockPrivateRelay = false;
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
  let logLevel = 'info';

  // Tabs keep Settings to one subject at a time; the tab lives in the URL
  // (#/settings?tab=notifications) so links and reloads land on it.
  const tabs = copy.settings.tabs;
  type TabKey = (typeof tabs)[number]['key'];
  let tab: TabKey = initialTab();

  function initialTab(): TabKey {
    const t = currentParams().tab;
    return tabs.some((x) => x.key === t) ? (t as TabKey) : 'dns';
  }

  function selectTab(t: TabKey): void {
    tab = t;
    window.history.replaceState(null, '', `#/settings?tab=${t}`);
  }

  // A link to another tab while Settings is already open.
  function onHashChange(): void {
    if (location.hash.startsWith('#/settings')) tab = initialTab();
  }
  let dhcpListen = true;

  function initFrom(c: ConfigView): void {
    cfg = c;
    upstreams = c.dns.upstreams.map((u) => ({ ...u }));
    savedUpstreams = new Set(c.dns.upstreams.map((u) => `${u.protocol} ${u.address}`));
    dnssec = c.dns.dnssec;
    mode = c.blocking.mode;
    safeSearch = c.blocking.safe_search;
    firefoxCanary = !c.dns.allow_firefox_doh;
    rebindProtect = c.dns.rebind_protection;
    blockPrivateRelay = c.blocking.block_icloud_private_relay;
    blockTTL = c.dns.block_ttl;
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
    logLevel = c.log_level || 'info';
    ssdp = c.discovery.ssdp;
    dhcpListen = c.discovery.dhcp_listen;
    snapshot();
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

  // One save model for every tab: fields (toggles included) are drafts
  // until "Save changes" in the bar at the bottom, which saves every tab
  // with changes in one request. Actions that are not settings — setting
  // the API token, restoring a backup — stay immediate buttons.
  type Upd = Parameters<typeof api.updateConfig>[0];

  function tabUpdate(t: TabKey): Upd | null {
    switch (t) {
      case 'dns':
        return {
          dns: {
            upstreams,
            dnssec,
            cache: {
              enabled: cacheEnabled,
              max_entries: cacheMaxEntries,
              min_ttl: cacheMinTTL,
              max_ttl: cacheMaxTTL,
              serve_stale: cacheServeStale,
            },
          },
        };
      case 'filtering':
        return {
          blocking: { mode, safe_search: safeSearch, block_icloud_private_relay: blockPrivateRelay },
          dns: { block_ttl: blockTTL, allow_firefox_doh: !firefoxCanary, rebind_protection: rebindProtect },
        };
      case 'notifications':
        return {
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
        };
      case 'discovery':
        return { discovery: { ssdp, dhcp_listen: dhcpListen } };
      case 'system':
        return {
          querylog: { retention_days: retentionDays, ring_size: ringSize, memory_mb: memoryMB },
          log_level: logLevel,
          update_check: updateCheck,
        };
    }
    return null;
  }

  let baseline: Record<string, string> = {};
  function snapshot(): void {
    baseline = Object.fromEntries(tabs.map((t) => [t.key, JSON.stringify(tabUpdate(t.key))]));
  }

  // Every draft is named here so the comparison re-runs when any changes
  // (Svelte tracks only what a reactive statement names).
  $: draftKey = JSON.stringify([
    upstreams, dnssec, mode, blockTTL, safeSearch, firefoxCanary, rebindProtect, blockPrivateRelay, retentionDays,
    ringSize, memoryMB, cacheEnabled, cacheMaxEntries, cacheMinTTL, cacheMaxTTL, cacheServeStale,
    updateCheck, webhookURL, ntfyURL, ntfyToken, digest, digestTime, digestDay, uiURL, quiet,
    quietFrom, quietTo, mute, ssdp, dhcpListen, logLevel,
  ]);
  $: dirtyTabs = changedTabs(draftKey, baseline);
  $: setLeaveGuard(dirtyTabs.length > 0);

  function changedTabs(_key: string, base: Record<string, string>): TabKey[] {
    if (!cfg) return [];
    return tabs.map((t) => t.key).filter((k) => JSON.stringify(tabUpdate(k)) !== base[k]);
  }

  // Deep-merges tab updates (two tabs both write under dns); arrays replace.
  function merge(a: Record<string, unknown>, b: Record<string, unknown>): Record<string, unknown> {
    const out: Record<string, unknown> = { ...a };
    for (const [k, v] of Object.entries(b)) {
      const cur = out[k];
      out[k] =
        v && typeof v === 'object' && !Array.isArray(v) && cur && typeof cur === 'object' && !Array.isArray(cur)
          ? merge(cur as Record<string, unknown>, v as Record<string, unknown>)
          : v;
    }
    return out;
  }

  let saving = false;
  async function saveAll(): Promise<void> {
    let upd: Record<string, unknown> = {};
    for (const t of dirtyTabs) upd = merge(upd, (tabUpdate(t) ?? {}) as Record<string, unknown>);
    saving = true;
    await save(upd as Upd);
    saving = false;
  }

  function discard(): void {
    if (cfg) initFrom(cfg);
  }

  function tabLabel(k: TabKey): string {
    return tabs.find((t) => t.key === k)?.label ?? k;
  }

  // Reloading or closing the page with unsaved changes asks first.
  function onBeforeUnload(ev: BeforeUnloadEvent): void {
    if (dirtyTabs.length) ev.preventDefault();
  }

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
    }
  }

  // The first two rows are the primary and secondary resolvers; the rest are
  // ordered fallbacks.
  function orderLabel(i: number): string {
    if (i === 0) return 'Primary';
    if (i === 1) return 'Secondary';
    return `${i + 1}.`;
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
      cfg = updated; // only the token changed: other tabs' drafts survive
      newToken = '';
      notify(copy.settings.saved);
    } catch (e) {
      notifyError(e);
    }
  }

  async function clearToken(): Promise<void> {
    if (!(await confirmDialog({ message: copy.settings.tokenConfirmClear, confirm: copy.settings.tokenClear, danger: true })))
      return;
    try {
      const updated = await api.updateConfig({ api: { token: '' } });
      setToken('');
      cfg = updated;
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
    if (!(await confirmDialog({ message: copy.settings.restoreConfirm, confirm: copy.settings.restoreButton, danger: true })))
      return;
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
    window.addEventListener('hashchange', onHashChange);
    window.addEventListener('beforeunload', onBeforeUnload);
  });

  onDestroy(() => {
    if (healthTimer) clearInterval(healthTimer);
    window.removeEventListener('hashchange', onHashChange);
    window.removeEventListener('beforeunload', onBeforeUnload);
    setLeaveGuard(false);
  });
</script>

<h1>{copy.settings.title}<HelpLink section={`settings-${tab}`} /></h1>

{#if cfg}
  <div class="tabs" role="tablist">
    {#each tabs as t (t.key)}
      <button role="tab" class:active={tab === t.key} aria-selected={tab === t.key} on:click={() => selectTab(t.key)}
        >{t.label}{#if dirtyTabs.includes(t.key)}<span class="dirty" title={copy.settings.unsavedTab}></span>{/if}</button
      >
    {/each}
  </div>

  {#if tab === 'dns'}
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
          title="Pick a known resolver"
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
        <select bind:value={u.protocol} title="Protocol">
          <option value="doh">DoH (encrypted, HTTPS)</option>
          <option value="dot">DoT (encrypted, TLS)</option>
          <option value="udp">UDP (plaintext)</option>
          <option value="tcp">TCP (plaintext)</option>
        </select>
        <button title="Move up" disabled={i === 0} on:click={() => moveUpstream(i, -1)}>↑</button>
        <button title="Move down" disabled={i === upstreams.length - 1} on:click={() => moveUpstream(i, 1)}>
          ↓
        </button>
        <button class="danger" title="Remove" disabled={upstreams.length === 1} on:click={() => removeUpstream(i)}>
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
  </section>
  {/if}

  {#if tab === 'filtering'}
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
      <input type="checkbox" bind:checked={rebindProtect} />
      {copy.settings.rebind}
    </label>
    <p class="note">{copy.settings.rebindHint}</p>
    <label class="radio safe-search">
      <input type="checkbox" bind:checked={blockPrivateRelay} />
      {copy.settings.privateRelay}
    </label>
    <p class="note">{copy.settings.privateRelayHint}</p>
  </section>
  {/if}

  {#if tab === 'notifications'}
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
            <option value="off">Off</option>
            <option value="daily">Daily</option>
            <option value="weekly">Weekly</option>
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
        on:click={sendTest}
        disabled={testing || dirtyTabs.includes('notifications')}
        title={copy.settings.testSaveFirst}
      >
        {testing ? copy.settings.testSending : copy.settings.testAction}
      </button>
      {#if testResult}<span class="test-result">{testResult}</span>{/if}
    </div>
  </section>
  {/if}

  {#if tab === 'discovery'}
  <section class="card">
    <h2>{copy.settings.discoveryTitle} <small>{copy.settings.discoveryHint}</small></h2>
    <label class="radio">
      <input
        type="checkbox"
        bind:checked={ssdp}
      />
      {copy.settings.discoverySSDP}
    </label>
    <p class="note">{copy.settings.discoverySSDPHint}</p>
    <label class="radio">
      <input
        type="checkbox"
        bind:checked={dhcpListen}
      />
      {copy.settings.discoveryDHCP}
    </label>
    <p class="note">{copy.settings.discoveryDHCPHint}</p>
  </section>
  {/if}

  {#if tab === 'access'}
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
  {/if}

  {#if tab === 'system'}
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
  </section>
  <section class="card">
    <h2>{copy.settings.logLevelTitle} <small>{copy.settings.logLevelHint}</small></h2>
    <label class="field">
      <span>{copy.logs.recordLabel}</span>
      <select bind:value={logLevel}>
        {#each ['debug', 'info', 'warn', 'error'] as l (l)}
          <option value={l}>{copy.logs.recordLevels[l]}</option>
        {/each}
      </select>
    </label>
    <p class="note">{copy.settings.logLevelNote} <a href="#/logs">{copy.settings.logLevelLink}</a></p>
  </section>
  <section class="card">
    <h2>{copy.settings.updatesTitle}</h2>
    <label class="radio">
      <input
        type="checkbox"
        bind:checked={updateCheck}
      />
      {copy.settings.updateCheck}
    </label>
    <p class="note">{copy.settings.updateCheckHint}</p>
  </section>
  {/if}

  {#if tab === 'backup'}
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
  {#if dirtyTabs.length}
    <div class="savebar" role="status">
      <span>{copy.settings.unsaved(dirtyTabs.map(tabLabel))}</span>
      <button on:click={discard} disabled={saving}>{copy.settings.discard}</button>
      <button class="primary" on:click={saveAll} disabled={saving}>{copy.settings.save}</button>
    </div>
  {/if}
{/if}

<style>
  .savebar {
    position: sticky;
    bottom: 0;
    z-index: 5;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.6rem;
    margin-top: 1.25rem;
    padding: 0.7rem 1rem;
    background: var(--bg-raised);
    border: 1px solid var(--accent);
    border-radius: 6px;
    box-shadow: 0 -6px 20px rgba(0, 0, 0, 0.35);
  }

  .savebar span {
    flex: 1;
    font-size: 0.88rem;
  }

  .tabs .dirty {
    display: inline-block;
    width: 0.4rem;
    height: 0.4rem;
    margin-left: 0.35rem;
    border-radius: 50%;
    background: var(--accent);
    vertical-align: middle;
  }

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

  .tabs {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
    margin-bottom: 1.1rem;
    border-bottom: 1px solid var(--border);
  }

  .tabs button {
    border: none;
    border-bottom: 2px solid transparent;
    border-radius: 0;
    padding: 0.45rem 0.8rem;
    color: var(--text-dim);
  }

  .tabs button.active {
    color: var(--accent);
    border-bottom-color: var(--accent);
  }
</style>
