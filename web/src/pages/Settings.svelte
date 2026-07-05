<script lang="ts">
  import { onMount } from 'svelte';
  import {
    api,
    exportConfig,
    setToken,
    type ConfigView,
    type ImportReport,
    type Upstream,
  } from '../lib/api';
  import { copy } from '../lib/copy';
  import { notify, notifyError } from '../lib/toast';

  let cfg: ConfigView | null = null;

  // Editable working copies, initialised from the loaded config.
  let upstreams: Upstream[] = [];
  let mode: string = 'zero_ip';
  let blockTTL = 60;
  let safeSearch = false;
  let refreshInterval = '24h';
  let retentionDays = 90;
  let ringSize = 10000;
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

  interface RouteRow {
    domains: string;
    address: string;
    protocol: Upstream['protocol'];
  }
  let routeRows: RouteRow[] = [];

  function initFrom(c: ConfigView): void {
    cfg = c;
    upstreams = c.dns.upstreams.map((u) => ({ ...u }));
    mode = c.blocking.mode;
    safeSearch = c.blocking.safe_search;
    blockTTL = c.dns.block_ttl;
    refreshInterval = c.lists.refresh_interval;
    retentionDays = c.querylog.retention_days;
    ringSize = c.querylog.ring_size;
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

  async function save(upd: Parameters<typeof api.updateConfig>[0]): Promise<void> {
    try {
      initFrom(await api.updateConfig(upd));
      notify(copy.settings.saved);
    } catch (e) {
      notifyError(e);
    }
  }

  const saveUpstreams = () => save({ dns: { upstreams } });
  const saveBlocking = () =>
    save({ blocking: { mode, safe_search: safeSearch }, dns: { block_ttl: blockTTL } });
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
    save({ querylog: { retention_days: retentionDays, ring_size: ringSize } });

  function addUpstream(): void {
    upstreams = [...upstreams, { address: '', protocol: 'doh' }];
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

  onMount(() => void load());
</script>

<h1>{copy.settings.title}</h1>

{#if cfg}
  <section class="card">
    <h2>{copy.settings.upstreamsTitle} <small>{copy.settings.upstreamsHint}</small></h2>
    {#each upstreams as u, i (i)}
      <div class="upstream-row">
        <span class="order">{i + 1}.</span>
        <input
          class="grow"
          placeholder={u.protocol === 'doh' ? 'https://resolver/dns-query' : 'host:port'}
          bind:value={u.address}
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
    <p class="note">{copy.settings.notificationsNote}</p>
    <div class="section-actions">
      <button
        class="primary"
        on:click={() =>
          void save({
            notifications: {
              webhook_url: webhookURL.trim(),
              ntfy_url: ntfyURL.trim(),
              // Only send a typed token; an untouched field never clobbers
              // the stored one. Clearing ntfy_url makes any token inert.
              ...(ntfyToken.trim() ? { ntfy_token: ntfyToken.trim() } : {}),
            },
          })}
      >
        {copy.settings.save}
      </button>
    </div>
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

  .import-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr));
    gap: 1.5rem;
  }

  .import-source h3 {
    margin: 0 0 0.6rem;
    font-size: 0.95rem;
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
    margin-bottom: 0.5rem;
  }

  .order {
    color: var(--text-dim);
    width: 1.2rem;
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
</style>
