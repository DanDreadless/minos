<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, type Host, type Stats, type Status } from '../lib/api';
  import BarList from '../lib/components/BarList.svelte';
  import SetupCard from '../lib/components/SetupCard.svelte';
  import StatTile from '../lib/components/StatTile.svelte';
  import TimelineChart from '../lib/components/TimelineChart.svelte';
  import { copy } from '../lib/copy';
  import {
    fmtBytes,
    fmtCelsius,
    fmtNumber,
    fmtPercentValue,
    fmtUptime,
  } from '../lib/format';
  import { docketHref } from '../lib/router';
  import { notifyError } from '../lib/toast';

  export let status: Status | null;
  export let onStatusChange: () => Promise<void>;

  let stats: Stats | null = null;
  let host: Host | null = null;
  // Whether an API token is configured. Read once: it drives a one-line
  // nudge on the host card, which is the first thing here to expose the
  // machine's identity rather than just DNS activity.
  let tokenSet = true;
  let customPause = '';
  let timer: ReturnType<typeof setInterval> | null = null;
  let hostTimer: ReturnType<typeof setInterval> | null = null;

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

  // The host card is supplementary: if it fails, the dashboard's real
  // job is unaffected, so it degrades to hidden rather than raising a
  // toast over the DNS numbers the user came for.
  async function loadHost(): Promise<void> {
    host = await api.host().catch(() => null);
  }

  $: hostSample = host?.sample;
  $: diskUsedPct =
    hostSample?.disk_total && hostSample?.disk_free !== undefined && hostSample.disk_total > 0
      ? ((hostSample.disk_total - hostSample.disk_free) / hostSample.disk_total) * 100
      : undefined;
  // The query log lives on this filesystem, so a full disk stops Minos
  // recording rather than merely inconveniencing the host.
  $: diskLow = diskUsedPct !== undefined && diskUsedPct >= 90;

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
    void loadHost();
    void api
      .getConfig()
      .then((cfg) => {
        tokenSet = cfg.api.token_set;
      })
      .catch(() => {});
    // The host sampler ticks every 10s server-side; polling it on the
    // stats cadence would leave the card a minute stale, so it gets its
    // own faster timer.
    timer = setInterval(() => {
      void loadStats();
      void loadHost();
    }, 60000);
    hostTimer = setInterval(loadHost, 15000);
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
    if (hostTimer) clearInterval(hostTimer);
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

{#if host?.supported}
  <section class="card host">
    <h2>
      {copy.host.title}
      <small>{copy.host.titleHint}</small>
      <span class="host-id">
        {host.hostname}{#if host.platform} · {host.platform}{/if}{#if host.cpus}
          · {host.cpus} CPU{host.cpus === 1 ? '' : 's'}{/if}
      </span>
    </h2>

    {#if diskLow}
      <p class="warn">{copy.host.diskLow}</p>
    {/if}

    <dl class="readings">
      <div>
        <dt title={copy.host.cpuHint}>{copy.host.cpu}</dt>
        <dd>{fmtPercentValue(hostSample?.cpu_percent)}</dd>
      </div>
      <div>
        <dt title={copy.host.loadHint}>{copy.host.load}</dt>
        <dd>{fmtNumber(hostSample?.load1)}</dd>
      </div>
      <div>
        <dt title={copy.host.memoryHint}>{copy.host.memory}</dt>
        <dd>
          {fmtBytes(hostSample?.mem_used)}<span class="of"
            >/ {fmtBytes(host.mem_total || undefined)}</span
          >
        </dd>
      </div>
      <div>
        <dt title={copy.host.diskHint}>{copy.host.disk}</dt>
        <dd class:bad={diskLow}>
          {fmtPercentValue(diskUsedPct)}<span class="of"
            >of {fmtBytes(hostSample?.disk_total)}</span
          >
        </dd>
      </div>
      <div>
        <dt title={copy.host.temperatureHint}>{copy.host.temperature}</dt>
        <dd>{fmtCelsius(hostSample?.temp_celsius)}</dd>
      </div>
      <div>
        <dt title={copy.host.uptimeHint}>{copy.host.uptime}</dt>
        <dd>{fmtUptime(hostSample?.uptime_seconds)}</dd>
      </div>
      <div>
        <dt title={copy.host.processHint}>{copy.host.process}</dt>
        <dd>{fmtBytes(hostSample?.proc_rss)}</dd>
      </div>
    </dl>

    {#if host.mem_source === 'cgroup'}
      <p class="sub">{copy.host.cgroupNote}</p>
    {/if}
    {#if !tokenSet}
      <p class="sub">
        {copy.host.tokenHint}
        <a href="#/settings">{copy.host.tokenAction}</a>
      </p>
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
  }

  .dnssec .sub {
    margin: 0 0 0.6rem;
  }

  .host .sub {
    margin: 0.8rem 0 0;
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

  /* A full disk is a failure, not a caution: it stops Minos recording. */
  .host .warn {
    border-left-color: var(--blocked);
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

  .host {
    margin-bottom: 1.25rem;
  }

  .host-id {
    font-family: var(--font-body);
    font-size: 0.75rem;
    font-weight: normal;
    color: var(--text-dim);
    letter-spacing: 0;
    margin-left: 0.6rem;
  }

  .readings {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(8rem, 1fr));
    gap: 0.75rem;
    margin: 0.5rem 0 0;
  }

  .readings dt {
    color: var(--text-dim);
    font-size: 0.75rem;
  }

  .readings dd {
    margin: 0.1rem 0 0;
    font-size: 1.15rem;
    font-family: var(--font-mono);
  }

  .readings dd.bad {
    color: var(--blocked);
  }

  .readings .of {
    font-size: 0.75rem;
    color: var(--text-dim);
    margin-left: 0.3rem;
  }
</style>
