<script lang="ts">
  // Dashboard → This week: the last seven days as a few plain facts, each
  // linking to where it can be looked into. Always the last 7 days,
  // whatever window the rest of the dashboard shows.
  import { onMount } from 'svelte';
  import { api, type SummaryDevice, type WeekSummary } from '../api';
  import { copy } from '../copy';
  import { fmtClock } from '../format';
  import { deviceHref, docketHref } from '../router';

  const c = copy.summary;
  let s: WeekSummary | null = null;
  let failed = false;

  onMount(async () => {
    try {
      s = await api.summary();
    } catch {
      failed = true; // The card is a nicety: say so quietly, no toast
    }
  });

  function label(d: SummaryDevice): string {
    return d.name || d.address;
  }

  // Change describes this week against the last, or nothing when there is
  // no last week to compare with (a new install).
  function change(now: number, before: number): string {
    if (before === 0) return '';
    const pct = Math.round((100 * (now - before)) / before);
    return pct === 0 ? c.same : pct > 0 ? c.more(pct) : c.fewer(-pct);
  }

  function share(part: number, whole: number): string {
    return whole ? `${((100 * part) / whole).toFixed(1)}%` : '0%';
  }
</script>

<section class="card week">
  <h2>{c.title} <small>{c.hint}</small></h2>
  {#if failed}
    <p class="note">{c.failed}</p>
  {:else if !s}
    <p class="note">{c.loading}</p>
  {:else}
    <ul>
      <li>
        <a href={docketHref({ range: '7d' })}>{c.lookups(s.lookups)}</a>, {c.blocked(share(s.blocked, s.lookups))}
        {#if change(s.lookups, s.prev_lookups)}<span class="dim"> — {change(s.lookups, s.prev_lookups)}</span>{/if}
      </li>
      <li>
        {#if s.new_device_count === 0}
          {c.noNewDevices}
        {:else}
          {c.newDevices(s.new_device_count)}
          {#each s.new_devices as d, i (i)}{i > 0 ? ', ' : ' '}<a href={deviceHref(d.key)}>{label(d)}</a>{/each}{s.new_device_count >
          s.new_devices.length
            ? c.andMore(s.new_device_count - s.new_devices.length)
            : ''}.
        {/if}
      </li>
      <li>
        {#if s.security_lists.length === 0}
          {c.noSecurityLists} <a href="#/lists">{c.addSecurityList}</a>
        {:else if s.security.blocked === 0}
          {c.securityQuiet}
        {:else}
          {c.securityBlocked(s.security.blocked, s.security.lists.join(', '))}
          {#each s.security.devices as d, i (i)}{i > 0 ? ', ' : ' '}<a href={deviceHref(d.key)}>{label(d)}</a>{/each}.
          <a href={docketHref({ list: s.security.lists[0], verdict: 'blocked', range: '7d' })}>{c.seeLookups}</a>
        {/if}
      </li>
      <li>
        {#if s.tracking.companies === 0}
          {c.noTracking}
        {:else}
          <a href="#/privacy">{c.trackingCompanies(s.tracking.companies)}</a>:
          {c.trackingCounts(s.tracking.allowed, s.tracking.blocked)}{#if s.tracking.top.length}{c.trackingTop(
              s.tracking.top.map((t) => t.name),
            )}{/if}.
        {/if}
      </li>
      {#if s.busiest}
        <li>
          {c.busiest} <a href={deviceHref(s.busiest.key)}>{label(s.busiest)}</a>, {c.lookups(s.busiest.lookups ?? 0)}.
        </li>
      {/if}
      <li>
        {#if s.settings_changes === 0}
          {c.noSettingsChanges}
        {:else}
          <a href="#/settings?tab=history">{c.settingsChanges(s.settings_changes)}</a>{#if s.last_change}{c.lastChange(
              s.last_change.actor,
              fmtClock(s.last_change.time),
            )}{/if}.
        {/if}
      </li>
    </ul>
  {/if}
</section>

<style>
  /* Matches the dashboard's card subtitles (the DNSSEC card beside it). */
  h2 small {
    color: var(--text-dim);
    font-size: 0.75rem;
  }

  ul {
    margin: 0;
    padding-left: 1.1rem;
  }

  li {
    margin: 0.3rem 0;
    line-height: 1.45;
  }

  a {
    color: var(--accent);
    overflow-wrap: anywhere;
  }

  .dim,
  .note {
    color: var(--text-dim);
  }

  .note {
    font-size: 0.85rem;
  }
</style>
