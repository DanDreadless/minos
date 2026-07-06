<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, openStream, type LogEntry } from '../lib/api';
  import { copy } from '../lib/copy';
  import { notify, notifyError } from '../lib/toast';

  const MAX_ROWS = 500;

  let entries: LogEntry[] = [];
  let search = '';
  let verdictFilter: 'all' | 'blocked' | 'allowed' = 'all';
  let ws: WebSocket | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let destroyed = false;
  let connected = false;

  $: filtered = entries.filter((e) => {
    if (verdictFilter !== 'all' && e.verdict !== verdictFilter) return false;
    if (search) {
      const q = search.toLowerCase();
      return e.qname.includes(q) || e.client.includes(q);
    }
    return true;
  });

  function fmtTime(iso: string): string {
    return new Date(iso).toLocaleTimeString();
  }

  function connect(): void {
    if (destroyed) return;
    ws = openStream(
      (e) => {
        connected = true;
        entries = [e, ...entries].slice(0, MAX_ROWS);
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
    try {
      entries = await api.querylog(MAX_ROWS);
    } catch (e) {
      notifyError(e);
    }
    connect();
  });

  onDestroy(() => {
    destroyed = true;
    if (reconnectTimer) clearTimeout(reconnectTimer);
    ws?.close();
  });
</script>

<h1>
  {copy.docket.title} <small>{copy.docket.subtitle}</small>
  {#if connected}<span class="live" title="streaming new queries">{copy.docket.live}</span>{/if}
</h1>

<div class="filters">
  <input type="search" placeholder={copy.docket.searchPlaceholder} bind:value={search} />
  <select bind:value={verdictFilter}>
    <option value="all">{copy.docket.filterAll}</option>
    <option value="blocked">{copy.docket.filterBlocked}</option>
    <option value="allowed">{copy.docket.filterAllowed}</option>
  </select>
  <span class="count">{filtered.length} shown</span>
</div>

{#if filtered.length === 0}
  <p class="empty">{copy.docket.empty}</p>
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
        {#each filtered as e (e.time + e.qname + e.client + e.qtype)}
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
              <td><span class="badge allowed">{copy.docket.verdictAllowed}</span></td>
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

  .count {
    color: var(--text-dim);
    font-size: 0.8rem;
    margin-left: auto;
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
