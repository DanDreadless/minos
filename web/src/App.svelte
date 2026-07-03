<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, ApiError, openStream, setToken, type LogEntry, type Status } from './lib/api';
  import { copy } from './lib/copy';

  const MAX_ROWS = 200;

  let status: Status | null = null;
  let entries: LogEntry[] = [];
  let needsToken = false;
  let tokenInput = '';
  let notice = '';
  let error = '';
  let ws: WebSocket | null = null;
  let pollTimer: ReturnType<typeof setInterval> | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let destroyed = false;

  function fmtTime(iso: string): string {
    return new Date(iso).toLocaleTimeString();
  }

  function fmtPercent(total: number, blocked: number): string {
    if (total === 0) return '—';
    return ((blocked / total) * 100).toFixed(1) + '%';
  }

  async function refresh(): Promise<void> {
    try {
      status = await api.status();
      needsToken = false;
      error = '';
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        needsToken = true;
      } else {
        error = e instanceof Error ? e.message : String(e);
      }
    }
  }

  function connect(): void {
    if (destroyed || needsToken) return;
    ws = openStream(
      (e) => {
        entries = [e, ...entries].slice(0, MAX_ROWS);
      },
      () => {
        ws = null;
        if (!destroyed) reconnectTimer = setTimeout(connect, 2000);
      },
    );
  }

  async function start(): Promise<void> {
    await refresh();
    if (needsToken) return;
    try {
      entries = await api.querylog(MAX_ROWS);
    } catch {
      // log endpoint failing is not fatal for the dashboard
    }
    connect();
    pollTimer = setInterval(refresh, 5000);
  }

  async function submitToken(): Promise<void> {
    setToken(tokenInput.trim());
    tokenInput = '';
    await start();
  }

  async function pardon(domain: string): Promise<void> {
    try {
      await api.pardon(domain);
      notice = copy.pardon.done(domain);
      setTimeout(() => (notice = ''), 5000);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  async function recess(duration: string): Promise<void> {
    try {
      await api.pause(duration);
      await refresh();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  async function resume(): Promise<void> {
    try {
      await api.resume();
      await refresh();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  onMount(() => {
    void start();
  });

  onDestroy(() => {
    destroyed = true;
    if (pollTimer) clearInterval(pollTimer);
    if (reconnectTimer) clearTimeout(reconnectTimer);
    ws?.close();
  });
</script>

<header>
  <div class="meander" aria-hidden="true"></div>
  <div class="masthead">
    <h1>{copy.appName}</h1>
    <span class="tagline">{copy.tagline}</span>
    {#if status}
      <span class="version">v{status.version}</span>
    {/if}
  </div>
</header>

{#if needsToken}
  <section class="token-gate">
    <p>{copy.token.prompt}</p>
    <form on:submit|preventDefault={submitToken}>
      <input
        type="password"
        placeholder={copy.token.placeholder}
        bind:value={tokenInput}
        autocomplete="off"
      />
      <button type="submit" class="primary">{copy.token.submit}</button>
    </form>
  </section>
{:else}
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}
  {#if notice}
    <p class="notice" role="status">{notice}</p>
  {/if}

  {#if status}
    <section class="controls">
      {#if status.paused}
        <span class="paused-banner">
          {status.paused_until
            ? copy.recess.active(new Date(status.paused_until).toLocaleTimeString())
            : copy.recess.activeIndefinite}
        </span>
        <button class="primary" on:click={resume}>{copy.recess.resume}</button>
      {:else}
        <span class="control-label" title={copy.recess.actionHint}>
          {copy.recess.action} <small>({copy.recess.actionHint})</small>
        </span>
        <button on:click={() => recess('5m')}>5 min</button>
        <button on:click={() => recess('30m')}>30 min</button>
        <button on:click={() => recess('')}>Until resumed</button>
      {/if}
    </section>

    <section class="stats">
      <div class="stat">
        <span class="stat-value">{status.queries_total.toLocaleString()}</span>
        <span class="stat-label" title={copy.stats.judgedHint}>{copy.stats.judged}</span>
        <span class="stat-hint">{copy.stats.judgedHint}</span>
      </div>
      <div class="stat">
        <span class="stat-value blocked">{status.queries_blocked.toLocaleString()}</span>
        <span class="stat-label" title={copy.stats.condemnedHint}>{copy.stats.condemned}</span>
        <span class="stat-hint">{copy.stats.condemnedHint}</span>
      </div>
      <div class="stat">
        <span class="stat-value">{fmtPercent(status.queries_total, status.queries_blocked)}</span>
        <span class="stat-label">{copy.stats.blockRate}</span>
        <span class="stat-hint">{copy.stats.blockRateHint}</span>
      </div>
      <div class="stat">
        <span class="stat-value">{status.rules.toLocaleString()}</span>
        <span class="stat-label">{copy.stats.rules}</span>
        <span class="stat-hint">{copy.stats.rulesHint}</span>
      </div>
    </section>
  {/if}

  <section class="docket">
    <h2>{copy.docket.title} <small>{copy.docket.subtitle}</small></h2>
    {#if entries.length === 0}
      <p class="empty">{copy.docket.empty}</p>
    {:else}
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
          {#each entries as e (e.time + e.qname + e.client)}
            <tr>
              <td>{fmtTime(e.time)}</td>
              <td>{e.client}</td>
              <td title={e.qname}>{e.qname}</td>
              <td>{e.qtype}</td>
              {#if e.verdict === 'blocked'}
                <td class="verdict-blocked">{copy.docket.verdictBlocked}</td>
                <td title="rule: {e.rule}">{e.list}</td>
                <td>
                  <button
                    class="pardon"
                    title={copy.pardon.actionHint}
                    on:click={() => pardon(e.rule ?? e.qname)}
                  >
                    {copy.pardon.action}
                  </button>
                </td>
              {:else}
                <td class="verdict-allowed">{copy.docket.verdictAllowed}</td>
                <td>{e.upstream ?? ''}</td>
                <td></td>
              {/if}
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>
{/if}

<style>
  header {
    margin-bottom: 2rem;
  }

  .meander {
    height: 6px;
    margin: 0 -1.5rem;
    background: repeating-linear-gradient(
      90deg,
      var(--accent) 0 12px,
      transparent 12px 24px
    );
    opacity: 0.5;
  }

  .masthead {
    display: flex;
    align-items: baseline;
    gap: 1rem;
    padding-top: 1.5rem;
  }

  h1 {
    margin: 0;
    font-size: 1.9rem;
    letter-spacing: 0.22em;
    text-transform: uppercase;
  }

  .tagline {
    color: var(--text-dim);
    font-style: italic;
  }

  .version {
    margin-left: auto;
    color: var(--text-dim);
    font-size: 0.8rem;
  }

  .token-gate {
    max-width: 24rem;
    margin: 4rem auto;
    text-align: center;
  }

  .token-gate form {
    display: flex;
    gap: 0.5rem;
    justify-content: center;
  }

  .error {
    color: var(--blocked);
  }

  .notice {
    color: var(--accent);
  }

  .controls {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    margin-bottom: 1.5rem;
  }

  .control-label small {
    color: var(--text-dim);
  }

  .paused-banner {
    color: var(--accent);
  }

  .stats {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
    gap: 1rem;
    margin-bottom: 2.5rem;
  }

  .stat {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 1rem 1.25rem;
    display: flex;
    flex-direction: column;
  }

  .stat-value {
    font-size: 1.7rem;
    font-variant-numeric: tabular-nums;
  }

  .stat-value.blocked {
    color: var(--blocked);
  }

  .stat-label {
    letter-spacing: 0.08em;
    margin-top: 0.25rem;
  }

  .stat-hint {
    color: var(--text-dim);
    font-size: 0.78rem;
  }

  .docket h2 {
    font-size: 1.2rem;
  }

  .docket h2 small {
    color: var(--text-dim);
    font-size: 0.8rem;
    margin-left: 0.5rem;
  }

  .empty {
    color: var(--text-dim);
    font-style: italic;
  }

  .verdict-blocked {
    color: var(--blocked);
  }

  .verdict-allowed {
    color: var(--allowed);
  }

  button.pardon {
    padding: 0.1rem 0.6rem;
    font-size: 0.8rem;
  }
</style>
