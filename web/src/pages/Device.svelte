<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, type ClientOverview, type Device, type Group, type LogEntry } from '../lib/api';
  import BarList from '../lib/components/BarList.svelte';
  import { copy } from '../lib/copy';
  import { fmtLogTime, fmtLogTimeFull } from '../lib/format';
  import { currentDeviceKey, docketHref, hrefFor } from '../lib/router';
  import { notify, notifyError } from '../lib/toast';

  const HISTORY_PAGE = 200;

  // The page key is the device's MAC when it has one (stable across DHCP
  // leases), else its IP — same addressing as the clients API.
  const key = currentDeviceKey();

  let device: Device | null = null;
  let groups: Group[] = [];
  let loaded = false;
  let nameDraft = '';
  let notesDraft = '';
  let timer: ReturnType<typeof setInterval> | null = null;

  function matches(d: Device): boolean {
    return (d.mac || d.ip) === key || d.ip === key || (d.ips ?? []).includes(key);
  }

  // Refresh identity/counters only — the history table paginates on its own
  // and must not be refetched by a timer.
  async function load(): Promise<void> {
    try {
      const [devices, gs] = await Promise.all([api.clients(), api.groups()]);
      groups = gs;
      const found = devices.find(matches) ?? null;
      if (found && (!device || device.notes !== found.notes)) notesDraft = found.notes ?? '';
      if (found && (!device || device.name !== found.name)) nameDraft = found.name ?? '';
      device = found;
    } catch (e) {
      notifyError(e);
    } finally {
      loaded = true;
    }
  }

  function deviceIPs(d: Device): string[] {
    // The history endpoint caps the exact-client filter at 32 addresses.
    return (d.ips ?? [d.ip]).slice(0, 32);
  }

  function deviceLabel(d: Device): string {
    return d.name || d.hostname || d.ip;
  }

  function fmtWhen(iso?: string): string {
    return iso ? new Date(iso).toLocaleString() : '—';
  }

  function blurOnEnter(e: KeyboardEvent): void {
    if (e.key === 'Enter') (e.target as HTMLInputElement).blur();
  }

  async function apply(upd: Parameters<typeof api.updateClient>[1]): Promise<void> {
    if (!device) return;
    try {
      const devices = await api.updateClient(device.mac || device.ip, { ...upd, ip: device.ip });
      device = devices.find(matches) ?? device;
    } catch (e) {
      notifyError(e);
      await load();
    }
  }

  async function saveName(): Promise<void> {
    const label = nameDraft.trim();
    if (!device || label === (device.name ?? '')) return;
    await apply({ name: label });
  }

  async function saveNotes(): Promise<void> {
    if (!device || notesDraft === (device.notes ?? '')) return;
    await apply({ notes: notesDraft });
    notify(copy.device.notesSaved);
  }

  async function setGroup(ev: Event): Promise<void> {
    await apply({ group: (ev.target as HTMLSelectElement).value });
  }

  async function toggleBlock(): Promise<void> {
    if (!device) return;
    const blocking = !device.blocked;
    await apply({ blocked: blocking });
    notify(
      blocking
        ? `DNS blocked for ${deviceLabel(device)}.`
        : `DNS restored for ${deviceLabel(device)}.`,
    );
  }

  // --- activity (top domains) ---

  let statsHours = 24;
  let overview: ClientOverview | null = null;
  const windows = [
    { hours: 24, label: '24h' },
    { hours: 168, label: '7d' },
    { hours: 720, label: '30d' },
    { hours: 2160, label: '90d' },
  ];

  async function loadStats(hours = statsHours): Promise<void> {
    if (!device) return;
    statsHours = hours;
    overview = null;
    try {
      overview = await api.clientStats(deviceIPs(device), hours);
    } catch (e) {
      notifyError(e);
    }
  }

  // --- query history (persisted log, all addresses) ---

  let history: LogEntry[] = [];
  let historyLoading = false;
  let historyDone = false;
  let verdictFilter: 'all' | 'blocked' | 'allowed' | 'would_block' = 'all';

  async function fetchHistory(reset: boolean): Promise<void> {
    if (!device || historyLoading) return;
    historyLoading = true;
    try {
      const before =
        !reset && history.length ? new Date(history[history.length - 1].time).getTime() : undefined;
      const rows = await api.querylogHistory({
        client: deviceIPs(device).join(','),
        verdict: verdictFilter === 'would_block' ? 'all' : verdictFilter,
        would_block: verdictFilter === 'would_block',
        before,
        limit: HISTORY_PAGE,
      });
      history = reset ? rows : [...history, ...rows];
      historyDone = rows.length < HISTORY_PAGE;
    } catch (e) {
      notifyError(e);
    } finally {
      historyLoading = false;
    }
  }

  function refreshHistory(): void {
    history = [];
    historyDone = false;
    void fetchHistory(true);
  }


  async function pardon(domain: string): Promise<void> {
    try {
      await api.pardon(domain);
      notify(copy.pardon.done(domain));
    } catch (e) {
      notifyError(e);
    }
  }

  async function sentence(domain: string): Promise<void> {
    try {
      await api.sentence(domain);
      notify(copy.sentence.done(domain));
    } catch (e) {
      notifyError(e);
    }
  }

  onMount(async () => {
    await load();
    if (device) {
      void loadStats();
      void fetchHistory(true);
    }
    timer = setInterval(load, 30000);
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
  });
</script>

{#if !loaded}
  <p class="empty">{copy.device.loading}</p>
{:else if !device}
  <h1>{copy.devices.title}</h1>
  <p class="empty">{copy.device.notFound}</p>
  <p><a href={hrefFor.devices}>← {copy.device.backToList}</a></p>
{:else}
  <p class="crumb"><a href={hrefFor.devices}>← {copy.device.backToList}</a></p>
  <h1>
    {deviceLabel(device)}
    {#if device.blocked}
      <span class="badge blocked">{copy.devices.blockedBadge}</span>
    {/if}
  </h1>

  <div class="controls">
    <input
      class="label-input"
      placeholder={copy.devices.namePlaceholder}
      bind:value={nameDraft}
      on:blur={saveName}
      on:keydown={blurOnEnter}
    />
    <select value={device.group} on:change={setGroup}>
      <option value="default">{copy.devices.groupDefault}</option>
      {#each groups as g (g.name)}
        <option value={g.name}>{g.name} ({g.mode})</option>
      {/each}
    </select>
    {#if device.blocked}
      <button class="row-action" on:click={toggleBlock}>{copy.devices.unblockAction}</button>
    {:else}
      <button class="row-action danger" title={copy.devices.blockHint} on:click={toggleBlock}>
        {copy.devices.blockAction}
      </button>
    {/if}
    <a class="row-action link" href={docketHref({ clients: deviceIPs(device) })}>
      {copy.devices.viewInDocket}
    </a>
  </div>

  <div class="grid">
    <section class="card identity">
      <h2>{copy.device.identityTitle}</h2>
      <dl>
        <dt>{copy.device.ipsLabel}</dt>
        <dd>
          {#each device.ips ?? [device.ip] as ip (ip)}
            <span class="ip">
              {ip}{#if ip === device.ip}
                <span class="tag">{copy.device.primaryTag}</span>{/if}
            </span>
          {/each}
        </dd>
        <dt>{copy.device.macLabel}</dt>
        <dd>
          {device.mac ?? '—'}
          {#if device.private_mac}
            <span class="dim" title={copy.devices.privateMACTitle}>({copy.devices.privateMAC})</span>
          {/if}
        </dd>
        {#if device.vendor}
          <dt>{copy.device.vendorLabel}</dt>
          <dd>{device.vendor}</dd>
        {/if}
        {#if device.model}
          <dt>{copy.device.modelLabel}</dt>
          <dd>{device.model}</dd>
        {/if}
        {#if device.hostname}
          <dt>{copy.device.hostnameLabel}</dt>
          <dd title={device.name_source ? copy.devices.nameSource(device.name_source) : undefined}>
            {device.hostname}
            {#if device.name_source}
              <span class="dim">via {device.name_source}</span>
            {/if}
          </dd>
        {/if}
        {#if device.hint && !device.vendor && !device.model}
          <dt>{copy.device.hintLabel}</dt>
          <dd class="dim" title={copy.devices.hintTitle}>{device.hint} (guessed)</dd>
        {/if}
        <dt>{copy.device.firstSeenLabel}</dt>
        <dd>{fmtWhen(device.first_seen)}</dd>
        <dt>{copy.device.lastSeenLabel}</dt>
        <dd>{fmtWhen(device.last_seen)}</dd>
        <dt>{copy.device.queriesLabel}</dt>
        <dd>
          {device.queries.toLocaleString()}
          <span class="dim">({device.queries_blocked.toLocaleString()} blocked)</span>
        </dd>
      </dl>
    </section>

    <section class="card notes">
      <h2>{copy.device.notesTitle}</h2>
      <textarea
        rows="6"
        maxlength="4096"
        placeholder={copy.device.notesPlaceholder}
        bind:value={notesDraft}
        on:blur={saveNotes}
      ></textarea>
    </section>
  </div>

  <section class="card activity">
    <div class="activity-head">
      <h2>{copy.device.activityWindow(statsHours)}</h2>
      <div class="activity-controls">
        {#each windows as w (w.hours)}
          <button
            class="row-action"
            class:active={statsHours === w.hours}
            on:click={() => loadStats(w.hours)}
          >
            {w.label}
          </button>
        {/each}
      </div>
    </div>
    {#if overview}
      <p class="activity-totals">
        {copy.devices.activityTotals(overview.total, overview.blocked)}
      </p>
      <div class="activity-grid">
        <div>
          <h3>{copy.devices.activityAllowed}</h3>
          <BarList
            empty={copy.devices.activityEmpty}
            items={overview.top_allowed.map((x) => ({
              label: x.qname,
              count: x.count,
              href: docketHref({ clients: device ? deviceIPs(device) : [], qname: x.qname }),
            }))}
          />
        </div>
        <div>
          <h3>{copy.devices.activityBlocked}</h3>
          <BarList
            tone="blocked"
            empty={copy.devices.activityEmpty}
            items={overview.top_blocked.map((x) => ({
              label: x.qname,
              count: x.count,
              href: docketHref({ clients: device ? deviceIPs(device) : [], qname: x.qname }),
            }))}
          />
        </div>
      </div>
    {:else}
      <p class="empty">{copy.devices.activityLoading}</p>
    {/if}
  </section>

  <section class="history">
    <div class="history-head">
      <h2>{copy.device.historyTitle} <small>{copy.device.historyHint}</small></h2>
      <select bind:value={verdictFilter} on:change={refreshHistory}>
        <option value="all">{copy.docket.filterAll}</option>
        <option value="blocked">{copy.docket.filterBlocked}</option>
        <option value="allowed">{copy.docket.filterAllowed}</option>
        <option value="would_block">{copy.docket.filterWouldBlock}</option>
      </select>
    </div>
    {#if history.length === 0}
      <p class="empty">{historyLoading ? copy.device.loading : copy.device.historyEmpty}</p>
    {:else}
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Time</th>
              <th>Client</th>
              <th>Domain</th>
              <th>Type</th>
              <th>Verdict</th>
              <th>Why</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <!-- Unkeyed on purpose — same-millisecond retries make these
                 fields non-unique, and duplicate keys crash the page. -->
            {#each history as e}
              <tr>
                <td class="when" title={fmtLogTimeFull(e.time)}>{fmtLogTime(e.time)}</td>
                <td>{e.client}</td>
                <td title={e.qname}>{e.qname}</td>
                <td>{e.qtype}</td>
                {#if e.verdict === 'blocked'}
                  <td><span class="badge blocked">{copy.docket.verdictBlocked}</span></td>
                  <td title="rule: {e.rule}">{e.list}</td>
                  <td>
                    <button
                      class="row-action"
                      title={copy.pardon.actionHint}
                      on:click={() => pardon(e.rule ?? e.qname)}
                    >
                      {copy.pardon.action}
                    </button>
                  </td>
                {:else}
                  <td>
                    <span class="badge allowed">{copy.docket.verdictAllowed}</span>
                    {#if e.audit_list}
                      <span
                        class="would-badge"
                        title={copy.docket.wouldBlockTitle(e.audit_list, e.audit_rule ?? '')}
                      >
                        {copy.docket.wouldBlockBadge}
                      </span>
                    {/if}
                  </td>
                  <td>{e.upstream ?? ''}</td>
                  <td>
                    <button
                      class="row-action subtle"
                      title={copy.sentence.actionHint}
                      on:click={() => sentence(e.qname)}
                    >
                      {copy.sentence.action}
                    </button>
                  </td>
                {/if}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if !historyDone}
        <div class="load-older">
          <button on:click={() => fetchHistory(false)} disabled={historyLoading}>
            {historyLoading ? copy.device.loading : copy.device.loadOlder}
          </button>
        </div>
      {/if}
    {/if}
  </section>
{/if}

<style>
  .crumb {
    margin: 0 0 0.4rem;
    font-size: 0.82rem;
  }

  .crumb a {
    color: var(--text-dim);
    text-decoration: none;
  }

  .crumb a:hover {
    color: var(--accent);
  }

  h1 .badge {
    vertical-align: middle;
    margin-left: 0.6rem;
  }

  h2 small {
    color: var(--text-dim);
    font-size: 0.78rem;
    margin-left: 0.5rem;
    letter-spacing: 0;
  }

  .empty {
    color: var(--text-dim);
    font-style: italic;
  }

  .controls {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-wrap: wrap;
    margin-bottom: 1.25rem;
  }

  .label-input {
    width: 12rem;
  }

  .row-action {
    padding: 0.1rem 0.6rem;
    font-size: 0.78rem;
  }

  .row-action.link {
    text-decoration: none;
  }

  .row-action.active {
    color: var(--accent);
    border-color: var(--accent);
  }

  .row-action.subtle {
    opacity: 0;
  }

  tr:hover .row-action.subtle {
    opacity: 1;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(20rem, 1fr));
    gap: 1.25rem;
    margin-bottom: 1.25rem;
  }

  .identity dl {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 0.35rem 1.2rem;
    margin: 0;
    font-size: 0.88rem;
  }

  .identity dt {
    color: var(--text-dim);
  }

  .identity dd {
    margin: 0;
  }

  .ip {
    display: inline-block;
    margin-right: 0.7rem;
    font-family: var(--font-mono);
    font-size: 0.85rem;
  }

  .tag {
    margin-left: 0.3rem;
    font-size: 0.65rem;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--accent);
    border: 1px solid var(--accent);
    border-radius: 999px;
    padding: 0 0.35rem;
    vertical-align: middle;
  }

  .dim {
    color: var(--text-dim);
    font-size: 0.8rem;
  }

  .notes textarea {
    width: 100%;
    resize: vertical;
    font-size: 0.85rem;
  }

  .activity {
    margin-bottom: 1.25rem;
  }

  .activity-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 1rem;
    flex-wrap: wrap;
  }

  .activity-controls {
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }

  .activity-totals {
    color: var(--text-dim);
    font-size: 0.82rem;
    margin: 0.3rem 0 1rem;
  }

  .activity-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(20rem, 1fr));
    gap: 1.5rem;
  }

  .activity-grid h3 {
    font-size: 0.82rem;
    color: var(--text-dim);
    margin: 0 0 0.6rem;
    font-weight: 600;
  }

  .history-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 1rem;
    flex-wrap: wrap;
    margin-bottom: 0.6rem;
  }

  .would-badge {
    display: inline-block;
    margin-left: 0.35rem;
    padding: 0 0.4rem;
    border: 1px solid var(--audit, #c9962e);
    border-radius: 0.6rem;
    color: var(--audit, #c9962e);
    font-size: 0.68rem;
    line-height: 1.4;
    white-space: nowrap;
    vertical-align: middle;
    cursor: help;
  }

  .load-older {
    text-align: center;
    padding: 0.6rem 0;
  }

  .load-older button {
    font-size: 0.8rem;
  }

  td.when {
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }
</style>
