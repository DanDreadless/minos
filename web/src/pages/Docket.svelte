<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, openStream, type LogEntry } from '../lib/api';
  import { copy } from '../lib/copy';
  import { currentParams } from '../lib/router';
  import { notify, notifyError } from '../lib/toast';

  const MAX_ROWS = 500;
  const HISTORY_PAGE = 200;

  // live: the ring buffer + WebSocket stream (the live tail). history: the
  // persisted, server-filtered results shown while searching or drilled in —
  // so a drill-down spans the full retained log, not just the ring.
  let live: LogEntry[] = [];
  let history: LogEntry[] = [];
  let historyLoading = false;
  let historyDone = false;
  let search = '';
  // clientFilter holds a device's exact IP(s) when drilled in from Devices or
  // the Tribunal — whole-address matching, distinct from the substring search.
  let clientFilter: string[] = [];
  let verdictFilter: 'all' | 'blocked' | 'allowed' | 'would_block' = 'all';
  let ws: WebSocket | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let searchDebounce: ReturnType<typeof setTimeout> | null = null;
  let destroyed = false;
  let connected = false;

  $: hasFilter = search.trim() !== '' || verdictFilter !== 'all' || clientFilter.length > 0;

  function clientMatch(e: LogEntry): boolean {
    if (verdictFilter === 'would_block') {
      if (!e.audit_list) return false;
    } else if (verdictFilter !== 'all' && e.verdict !== verdictFilter) return false;
    if (clientFilter.length && !clientFilter.includes(e.client)) return false;
    if (search) {
      const q = search.toLowerCase();
      return e.qname.toLowerCase().includes(q) || e.client.toLowerCase().includes(q);
    }
    return true;
  }

  // Unfiltered → the live tail. Filtered → server history, falling back to
  // filtering the live ring when history is empty (ephemeral mode, where the
  // ring already backs the dashboard too, or simply no persisted matches).
  $: displayed = !hasFilter ? live : history.length ? history : live.filter(clientMatch);

  async function fetchHistory(reset: boolean): Promise<void> {
    if (historyLoading) return;
    historyLoading = true;
    try {
      const before =
        !reset && history.length ? new Date(history[history.length - 1].time).getTime() : undefined;
      const rows = await api.querylogHistory({
        q: search.trim(),
        client: clientFilter.join(','),
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
    if (hasFilter) void fetchHistory(true);
  }

  function clearClientFilter(): void {
    clientFilter = [];
    refreshHistory();
  }

  function onSearchInput(): void {
    if (searchDebounce) clearTimeout(searchDebounce);
    searchDebounce = setTimeout(refreshHistory, 250);
  }

  function fmtTime(iso: string): string {
    return new Date(iso).toLocaleTimeString();
  }

  function connect(): void {
    if (destroyed) return;
    ws = openStream(
      (e) => {
        connected = true;
        live = [e, ...live].slice(0, MAX_ROWS);
        // Keep a filtered/drilled-in view live: prepend new matches on top.
        if (hasFilter && history.length && clientMatch(e)) history = [e, ...history];
      },
      () => {
        ws = null;
        connected = false;
        if (!destroyed) reconnectTimer = setTimeout(connect, 2000);
      },
    );
    ws.onopen = () => (connected = true);
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
    // Honour a deep link from the dashboard, e.g.
    // #/querylog?verdict=blocked&client=192.168.1.5
    const params = currentParams();
    if (params.verdict === 'blocked' || params.verdict === 'allowed') {
      verdictFilter = params.verdict;
    }
    if (params.client) {
      clientFilter = params.client
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
    } else if (params.qname) {
      search = params.qname;
    }

    try {
      live = await api.querylog(MAX_ROWS);
    } catch (e) {
      notifyError(e);
    }
    // Arrived via a drill-down/search → load the persisted history for it.
    if (hasFilter) void fetchHistory(true);
    connect();
  });

  onDestroy(() => {
    destroyed = true;
    if (reconnectTimer) clearTimeout(reconnectTimer);
    if (searchDebounce) clearTimeout(searchDebounce);
    ws?.close();
  });
</script>

<h1>
  {copy.docket.title} <small>{copy.docket.subtitle}</small>
  {#if connected}<span class="live" title="streaming new queries">{copy.docket.live}</span>{/if}
</h1>

<div class="filters">
  <input
    type="search"
    placeholder={copy.docket.searchPlaceholder}
    bind:value={search}
    on:input={onSearchInput}
  />
  <select bind:value={verdictFilter} on:change={refreshHistory}>
    <option value="all">{copy.docket.filterAll}</option>
    <option value="blocked">{copy.docket.filterBlocked}</option>
    <option value="allowed">{copy.docket.filterAllowed}</option>
    <option value="would_block">{copy.docket.filterWouldBlock}</option>
  </select>
  {#if clientFilter.length}
    <span class="chip" title={clientFilter.join(', ')}>
      {copy.docket.deviceScope}
      {clientFilter.length === 1 ? clientFilter[0] : `${clientFilter[0]} +${clientFilter.length - 1}`}
      <button class="chip-x" on:click={clearClientFilter} aria-label="clear device filter">×</button>
    </span>
  {/if}
  <span class="count">
    {displayed.length} shown{#if hasFilter && history.length}
      <span class="scope">· searching history</span>{/if}
  </span>
</div>

{#if displayed.length === 0}
  <p class="empty">{historyLoading ? 'Searching…' : copy.docket.empty}</p>
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
        {#each displayed as e (e.time + e.qname + e.client + e.qtype)}
          <tr>
            <td>{fmtTime(e.time)}</td>
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
  {#if hasFilter && history.length > 0 && !historyDone}
    <div class="load-older">
      <button on:click={() => fetchHistory(false)} disabled={historyLoading}>
        {historyLoading ? 'Loading…' : 'Load older'}
      </button>
    </div>
  {/if}
{/if}

<style>
  h1 small {
    color: var(--text-dim);
    font-size: 0.85rem;
    margin-left: 0.5rem;
  }

  .live {
    font-size: 0.65rem;
    letter-spacing: 0.15em;
    text-transform: uppercase;
    color: var(--allowed);
    border: 1px solid var(--allowed);
    border-radius: 999px;
    padding: 0.05rem 0.5rem;
    margin-left: 0.6rem;
    vertical-align: middle;
  }

  .filters {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    margin-bottom: 1rem;
  }

  .filters input {
    flex: 1;
    max-width: 24rem;
  }

  .chip {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    font-size: 0.78rem;
    color: var(--text);
    background: var(--surface-2, rgba(127, 127, 127, 0.15));
    border: 1px solid var(--border, rgba(127, 127, 127, 0.3));
    border-radius: 999px;
    padding: 0.1rem 0.3rem 0.1rem 0.6rem;
    white-space: nowrap;
  }

  .chip-x {
    all: unset;
    cursor: pointer;
    line-height: 1;
    font-size: 1rem;
    padding: 0 0.25rem;
    color: var(--text-dim);
  }

  .chip-x:hover {
    color: var(--accent);
  }

  .count {
    color: var(--text-dim);
    font-size: 0.8rem;
    margin-left: auto;
  }

  .count .scope {
    color: var(--accent);
  }

  .load-older {
    text-align: center;
    padding: 0.6rem 0;
    flex: none;
  }

  .load-older button {
    font-size: 0.8rem;
  }

  .empty {
    color: var(--text-dim);
    font-style: italic;
  }

  /* Fill the height main.fill hands us and scroll the rows internally, so
     the page itself never grows as queries stream in. */
  .table-wrap {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }

  /* Keep the column headers visible while the body scrolls. th already
     carries an opaque background in app.css. */
  thead th {
    position: sticky;
    top: 0;
    z-index: 1;
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

  .row-action {
    padding: 0.1rem 0.6rem;
    font-size: 0.78rem;
  }

  .row-action.subtle {
    opacity: 0;
  }

  tr:hover .row-action.subtle {
    opacity: 1;
  }
</style>
