<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, type Stats, type Status } from '../lib/api';
  import BarList from '../lib/components/BarList.svelte';
  import SetupCard from '../lib/components/SetupCard.svelte';
  import StatTile from '../lib/components/StatTile.svelte';
  import TimelineChart from '../lib/components/TimelineChart.svelte';
  import { copy } from '../lib/copy';
  import { docketHref } from '../lib/router';
  import { notifyError } from '../lib/toast';

  export let status: Status | null;
  export let onStatusChange: () => Promise<void>;

  let stats: Stats | null = null;
  let customPause = '';
  let timer: ReturnType<typeof setInterval> | null = null;

  // A fresh instance: nothing judged in the last 24 h. The setup checklist
  // shows until traffic arrives or the user dismisses it (SetupCard also
  // honours its own localStorage flag, so dismissal is permanent).
  $: fresh = stats !== null && stats.timeline.every((b) => b.total === 0);

  // The DNSSEC card is driven by two different clocks and must not blur
  // them: status.dnssec counters are process-lifetime, stats.dnssec is the
  // same 24 h window as the rest of the dashboard. Labelled separately.
  $: dnssec = status?.dnssec ?? null;
  $: dnssecJudged = dnssec
    ? dnssec.secure + dnssec.insecure + dnssec.bogus + dnssec.indeterminate
    : 0;
  // "Not checkable" dominating means the upstream isn't returning DNSSEC
  // records at all — validation is then near-inert, which looks identical
  // to "everything is fine" unless we say so. Unsigned zones are normal and
  // deliberately not part of this test.
  $: dnssecUpstreamBlind = dnssecJudged > 0 && dnssec!.indeterminate / dnssecJudged > 0.5;

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

{#if fresh}
  <SetupCard />
{/if}

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
      href={docketHref({ verdict: 'blocked' })}
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
    {#if status.cache_enabled}
      <StatTile
        value={fmtPercent(status.cache_hits + status.cache_misses, status.cache_hits)}
        label={copy.stats.cacheRate}
        hint={copy.stats.cacheRateHint}
      />
    {/if}
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

  {#if dnssec}
    <section class="card dnssec">
      <h2>
        {copy.dnssec.title}
        <small>{copy.dnssec.titleHint}</small>
        <span
          class="mode"
          class:enforce={dnssec.mode === 'enforce'}
          title={dnssec.mode === 'enforce'
            ? copy.dnssec.modeEnforceHint
            : copy.dnssec.modePermissiveHint}
        >
          {dnssec.mode === 'enforce' ? copy.dnssec.modeEnforce : copy.dnssec.modePermissive}
        </span>
      </h2>

      {#if dnssecUpstreamBlind}
        <p class="warn">{copy.dnssec.upstreamWarning}</p>
      {/if}

      {#if dnssec.mode === 'permissive'}
        {#if stats?.dnssec && stats.dnssec.would_block > 0}
          <p class="headline">
            <a href={docketHref({ verdict: 'would_block', list: 'dnssec' })}>
              {copy.dnssec.wouldBlock(stats.dnssec.would_block)}
            </a>
          </p>
          <p class="sub">{copy.dnssec.wouldBlockHint}</p>
          <BarList
            tone="blocked"
            empty={copy.dashboard.noData}
            items={stats.dnssec.top_domains.map((d) => ({
              label: d.qname,
              count: d.count,
              href: docketHref({ verdict: 'would_block', qname: d.qname }),
            }))}
          />
          <p class="sub">
            <a href={docketHref({ verdict: 'would_block', list: 'dnssec' })}>
              {copy.dnssec.seeDocket}
            </a>
          </p>
        {:else}
          <p class="headline quiet">{copy.dnssec.wouldBlockNone}</p>
          <p class="sub">{copy.dnssec.wouldBlockNoneHint}</p>
        {/if}
      {:else}
        <p class="sub">{copy.dnssec.enforceNote}</p>
      {/if}

      <h3>{copy.dnssec.countersTitle}</h3>
      <dl class="counters">
        <div>
          <dt title={copy.dnssec.secureHint}>{copy.dnssec.secure}</dt>
          <dd>{dnssec.secure.toLocaleString()}</dd>
        </div>
        <div>
          <dt title={copy.dnssec.insecureHint}>{copy.dnssec.insecure}</dt>
          <dd>{dnssec.insecure.toLocaleString()}</dd>
        </div>
        <div>
          <dt title={copy.dnssec.bogusHint}>{copy.dnssec.bogus}</dt>
          <dd class="bad">{dnssec.bogus.toLocaleString()}</dd>
        </div>
        <div>
          <dt title={copy.dnssec.indeterminateHint}>{copy.dnssec.indeterminate}</dt>
          <dd class:bad={dnssecUpstreamBlind}>{dnssec.indeterminate.toLocaleString()}</dd>
        </div>
      </dl>
    </section>
  {/if}

  <section class="columns">
    <div class="card">
      <h2>{copy.dashboard.topBlockedTitle} <small>{copy.dashboard.topBlockedHint}</small></h2>
      <BarList
        tone="blocked"
        empty={copy.dashboard.noData}
        items={stats.top_blocked.map((d) => ({
          label: d.qname,
          count: d.count,
          href: docketHref({ verdict: 'blocked', qname: d.qname }),
        }))}
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
          href: docketHref({ client: c.client }),
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

  .dnssec {
    margin-top: 1.25rem;
  }

  /* Amber, matching the would-block badge in the Docket: the same idea
     (attributed but not enforced) should read the same colour everywhere. */
  .mode {
    font-family: var(--font-body);
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    border: 1px solid var(--audit, #c9962e);
    color: var(--audit, #c9962e);
    border-radius: 3px;
    padding: 0.05rem 0.4rem;
    margin-left: 0.6rem;
    vertical-align: middle;
  }

  .mode.enforce {
    border-color: var(--blocked);
    color: var(--blocked);
  }

  .headline {
    font-size: 1.05rem;
    margin: 0.2rem 0 0.1rem;
  }

  .headline a {
    color: var(--audit, #c9962e);
    text-decoration: none;
  }

  .headline a:hover {
    text-decoration: underline;
  }

  .headline.quiet {
    color: var(--text-dim);
    font-size: 0.95rem;
  }

  .sub {
    color: var(--text-dim);
    font-size: 0.8rem;
    margin: 0 0 0.6rem;
  }

  .sub a {
    color: var(--text-dim);
  }

  .warn {
    border-left: 2px solid var(--audit, #c9962e);
    padding-left: 0.6rem;
    color: var(--text);
    font-size: 0.85rem;
    margin: 0.4rem 0 0.8rem;
  }

  h3 {
    font-size: 0.75rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text-dim);
    font-family: var(--font-body);
    font-weight: 600;
    margin: 1rem 0 0.4rem;
  }

  .counters {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(8rem, 1fr));
    gap: 0.75rem;
    margin: 0;
  }

  .counters dt {
    color: var(--text-dim);
    font-size: 0.75rem;
  }

  .counters dd {
    margin: 0.1rem 0 0;
    font-size: 1.15rem;
    font-family: var(--font-mono);
  }

  .counters dd.bad {
    color: var(--blocked);
  }
</style>
