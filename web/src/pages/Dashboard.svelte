<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, type Stats, type Status } from '../lib/api';
  import BarList from '../lib/components/BarList.svelte';
  import StatTile from '../lib/components/StatTile.svelte';
  import TimelineChart from '../lib/components/TimelineChart.svelte';
  import { copy } from '../lib/copy';
  import { notifyError } from '../lib/toast';

  export let status: Status | null;
  export let onStatusChange: () => Promise<void>;

  let stats: Stats | null = null;
  let customPause = '';
  let timer: ReturnType<typeof setInterval> | null = null;

  function fmtPercent(total: number, blocked: number): string {
    if (total === 0) return '—';
    return ((blocked / total) * 100).toFixed(1) + '%';
  }

  async function loadStats(): Promise<void> {
    try {
      stats = await api.stats(24);
    } catch (e) {
      notifyError(e);
    }
  }

  async function recess(duration: string): Promise<void> {
    try {
      await api.pause(duration);
      await onStatusChange();
    } catch (e) {
      notifyError(e);
    }
  }

  async function recessCustom(): Promise<void> {
    const d = customPause.trim();
    if (!d) return;
    await recess(d);
    customPause = '';
  }

  async function resume(): Promise<void> {
    try {
      await api.resume();
      await onStatusChange();
    } catch (e) {
      notifyError(e);
    }
  }

  onMount(() => {
    void loadStats();
    timer = setInterval(loadStats, 60000);
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
  });
</script>

{#if status}
  <section class="controls card">
    {#if status.paused}
      <span class="paused-banner">
        {status.paused_until
          ? copy.recess.active(new Date(status.paused_until).toLocaleTimeString())
          : copy.recess.activeIndefinite}
      </span>
      <button class="primary" on:click={resume}>{copy.recess.resume}</button>
    {:else}
      <span class="control-label">
        {copy.recess.action} <small>({copy.recess.actionHint})</small>
      </span>
      <button on:click={() => recess('5m')}>5 min</button>
      <button on:click={() => recess('30m')}>30 min</button>
      <button on:click={() => recess('')}>Until resumed</button>
      <form class="custom" on:submit|preventDefault={recessCustom}>
        <input placeholder="2h, 45m…" bind:value={customPause} size="6" />
        <button type="submit" disabled={!customPause.trim()}>Go</button>
      </form>
    {/if}
  </section>

  <section class="stats">
    <StatTile
      value={status.queries_total.toLocaleString()}
      label={copy.stats.judged}
      hint={copy.stats.judgedHint}
    />
    <StatTile
      value={status.queries_blocked.toLocaleString()}
      label={copy.stats.condemned}
      hint={copy.stats.condemnedHint}
      tone="blocked"
    />
    <StatTile
      value={fmtPercent(status.queries_total, status.queries_blocked)}
      label={copy.stats.blockRate}
      hint={copy.stats.blockRateHint}
    />
    <StatTile
      value={status.rules.toLocaleString()}
      label={copy.stats.rules}
      hint={copy.stats.rulesHint}
    />
  </section>
{/if}

{#if stats}
  <section class="card">
    <h2>{copy.dashboard.timelineTitle}</h2>
    {#if stats.timeline.some((b) => b.total > 0)}
      <TimelineChart data={stats.timeline} />
      <p class="legend">
        <span class="swatch total"></span> queries
        <span class="swatch blocked"></span> blocked
      </p>
    {:else}
      <p class="empty">{copy.dashboard.noData}</p>
    {/if}
  </section>

  <section class="columns">
    <div class="card">
      <h2>{copy.dashboard.topBlockedTitle} <small>{copy.dashboard.topBlockedHint}</small></h2>
      <BarList
        tone="blocked"
        empty={copy.dashboard.noData}
        items={stats.top_blocked.map((d) => ({ label: d.qname, count: d.count }))}
      />
    </div>
    <div class="card">
      <h2>{copy.dashboard.topClientsTitle} <small>{copy.dashboard.topClientsHint}</small></h2>
      <BarList
        tone="accent"
        empty={copy.dashboard.noData}
        items={stats.top_clients.map((c) => ({
          label: c.client,
          count: c.total,
          sub: `${c.blocked} blocked`,
        }))}
      />
    </div>
  </section>
{/if}

<style>
  .controls {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.6rem;
    margin-bottom: 1.25rem;
  }

  .control-label small {
    color: var(--text-dim);
  }

  .custom {
    display: flex;
    gap: 0.4rem;
    align-items: center;
  }

  .paused-banner {
    color: var(--accent);
  }

  .stats {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
    gap: 1rem;
    margin-bottom: 1.25rem;
  }

  .columns {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(20rem, 1fr));
    gap: 1.25rem;
    margin-top: 1.25rem;
  }

  h2 small {
    color: var(--text-dim);
    font-size: 0.75rem;
    margin-left: 0.5rem;
    letter-spacing: 0;
  }

  .legend {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.75rem;
    color: var(--text-dim);
    margin: 0.4rem 0 0;
  }

  .swatch {
    display: inline-block;
    width: 10px;
    height: 10px;
    border-radius: 2px;
  }

  .swatch.total {
    background: var(--chart-total);
  }

  .swatch.blocked {
    background: var(--blocked);
    margin-left: 0.8rem;
  }

  .empty {
    color: var(--text-dim);
    font-style: italic;
  }
</style>
