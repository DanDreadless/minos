<script lang="ts">
  import { onMount } from 'svelte';
  import {
    api,
    exportConfig,
    setToken,
    type ConfigView,
    type Upstream,
  } from '../lib/api';
  import { copy } from '../lib/copy';
  import { notify, notifyError } from '../lib/toast';

  let cfg: ConfigView | null = null;

  // Editable working copies, initialised from the loaded config.
  let upstreams: Upstream[] = [];
  let mode: string = 'zero_ip';
  let blockTTL = 60;
  let refreshInterval = '24h';
  let retentionDays = 90;
  let ringSize = 10000;
  let newToken = '';
  let cacheEnabled = true;
  let cacheMaxEntries = 10000;
  let cacheMinTTL = 10;
  let cacheMaxTTL = 3600;

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
    blockTTL = c.dns.block_ttl;
    refreshInterval = c.lists.refresh_interval;
    retentionDays = c.querylog.retention_days;
    ringSize = c.querylog.ring_size;
    cacheEnabled = c.dns.cache.enabled;
    cacheMaxEntries = c.dns.cache.max_entries;
    cacheMinTTL = c.dns.cache.min_ttl;
    cacheMaxTTL = c.dns.cache.max_ttl;
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
  const saveBlocking = () => save({ blocking: { mode }, dns: { block_ttl: blockTTL } });
  const saveLists = () => save({ lists: { refresh_interval: refreshInterval } });
  const saveCache = () =>
    save({
      dns: {
        cache: {
          enabled: cacheEnabled,
          max_entries: cacheMaxEntries,
          min_ttl: cacheMinTTL,
          max_ttl: cacheMaxTTL,
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
    <h2>{copy.settings.backupTitle} <small>{copy.settings.backupHint}</small></h2>
    <button on:click={doExport}>{copy.settings.backupButton}</button>
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

  .note {
    color: var(--text-dim);
    font-size: 0.85rem;
  }

  .route-arrow {
    color: var(--text-dim);
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
