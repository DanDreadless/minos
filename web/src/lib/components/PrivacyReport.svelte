<script lang="ts">
  // The privacy report: which companies the network (or one device) looked
  // up, how much of it was tracking, what got through, and a one-click
  // block for a company's tracking names.
  import { onMount } from 'svelte';
  import { api, type CompanyReport, type PrivacyReport } from '../api';
  import { confirmDialog } from '../confirm';
  import { copy } from '../copy';
  import { deviceHref, docketHref } from '../router';
  import { notify, notifyError } from '../toast';

  // Clients narrows the report to one device's addresses; empty means the
  // whole network.
  export let clients: string[] = [];

  const c = copy.privacy;
  let days = 7;
  let report: PrivacyReport | null = null;
  let loading = false;
  let open: Record<string, boolean> = {};
  let showOther = false;

  async function load(): Promise<void> {
    loading = true;
    try {
      report = await api.privacy(days, clients);
    } catch (e) {
      notifyError(e);
    } finally {
      loading = false;
    }
  }

  async function setBlocked(co: CompanyReport, blocked: boolean): Promise<void> {
    if (blocked) {
      const ok = await confirmDialog({ message: c.blockConfirm(co.name), confirm: c.block });
      if (!ok) return;
    }
    try {
      const res = await api.blockCompany(co.id, blocked);
      notify(blocked ? c.blocked(co.name, res.domains) : c.unblocked(co.name));
      await load();
    } catch (e) {
      notifyError(e);
    }
  }

  function share(co: CompanyReport): number {
    const t = co.allowed + co.blocked;
    return t ? Math.round((100 * co.blocked) / t) : 0;
  }

  // Split takes the report as a parameter so the lists follow it.
  function split(r: PrivacyReport | null): { tracking: CompanyReport[]; other: CompanyReport[] } {
    const all = r?.companies ?? [];
    return { tracking: all.filter((x) => x.tracking), other: all.filter((x) => !x.tracking) };
  }

  $: parts = split(report);
  $: through = parts.tracking.reduce((n, x) => n + x.allowed, 0);
  $: stopped = parts.tracking.reduce((n, x) => n + x.blocked, 0);

  onMount(load);
</script>

<div class="controls">
  <label>
    {c.period}
    <select bind:value={days} on:change={load}>
      <option value={1}>{c.today}</option>
      <option value={7}>{c.days(7)}</option>
      <option value={30}>{c.days(30)}</option>
    </select>
  </label>
  {#if loading}<span class="note">{c.loading}</span>{/if}
</div>

{#if report}
  {#if parts.tracking.length === 0}
    <p class="note">{c.none}</p>
  {:else}
    <p class="summary">{c.summary(parts.tracking.length, through, stopped)}</p>
    <ul class="companies">
      {#each parts.tracking as co (co.id)}
        <li>
          <button class="head" aria-expanded={!!open[co.id]} on:click={() => (open = { ...open, [co.id]: !open[co.id] })}>
            <span class="name">{co.name}</span>
            <span class="cats">{co.categories.join(' · ')}</span>
            <span class="counts">
              {#if co.allowed}<span class="through">{c.through(co.allowed)}</span>{/if}
              {#if co.blocked}<span class="stopped">{c.stopped(co.blocked)}</span>{/if}
            </span>
            <span class="bar" title={c.shareTitle(share(co))}><span style="width: {share(co)}%"></span></span>
          </button>
          {#if open[co.id]}
            <div class="detail">
              <table class="stack">
                <thead>
                  <tr><th>{c.colName}</th><th>{c.colKind}</th><th>{c.colThrough}</th><th>{c.colStopped}</th></tr>
                </thead>
                <tbody>
                  {#each co.domains as d, i (i)}
                    <tr>
                      <td class="primary">
                        <a href={docketHref({ qname: d.name, clients: clients.length ? clients : undefined })}>{d.name}</a>
                      </td>
                      <td data-label={c.colKind}>{d.tracking ? d.category : c.notTracking(d.category)}</td>
                      <td data-label={c.colThrough}>{d.allowed.toLocaleString()}</td>
                      <td data-label={c.colStopped}>{d.blocked.toLocaleString()}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
              {#if co.devices?.length}
                <p class="devices">
                  {c.devicesLabel}
                  {#each co.devices as dv, i (i)}{i > 0 ? ', ' : ' '}<a href={deviceHref(dv.key)}>{dv.name}</a>{/each}
                </p>
              {/if}
              {#if co.blockable}
                <div class="actions">
                  {#if co.blocked_all}
                    <span class="note">{c.isBlocked}</span>
                    <button on:click={() => setBlocked(co, false)}>{c.unblock}</button>
                  {:else}
                    <button class="primary" on:click={() => setBlocked(co, true)}>{c.blockButton(co.name)}</button>
                    <span class="note">{c.blockHint}</span>
                  {/if}
                </div>
              {/if}
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
  {#if parts.other.length}
    <button class="link" on:click={() => (showOther = !showOther)}>
      {showOther ? c.hideOther : c.showOther(parts.other.length)}
    </button>
    {#if showOther}
      <p class="note">{c.otherNote}</p>
      <ul class="other">
        {#each parts.other as co (co.id)}
          <li><span>{co.name}</span> <span class="note">{co.categories.join(' · ')} · {c.lookups(co.other)}</span></li>
        {/each}
      </ul>
    {/if}
  {/if}
  <p class="note attribution">{c.footnote} {report.attribution}.</p>
{/if}

<style>
  .controls {
    display: flex;
    gap: 0.75rem;
    align-items: center;
    flex-wrap: wrap;
    margin-bottom: 0.6rem;
  }

  .controls label {
    display: inline-flex;
    gap: 0.4rem;
    align-items: center;
    font-size: 0.85rem;
    color: var(--text-dim);
  }

  .note {
    color: var(--text-dim);
    font-size: 0.85rem;
  }

  .summary {
    margin: 0.2rem 0 0.8rem;
  }

  .companies,
  .other {
    list-style: none;
    padding: 0;
    margin: 0;
  }

  .companies > li {
    border-top: 1px solid var(--border);
  }

  .head {
    display: grid;
    grid-template-columns: minmax(8rem, 1.2fr) minmax(0, 1.5fr) auto 6rem;
    gap: 0.75rem;
    align-items: center;
    width: 100%;
    padding: 0.6rem 0;
    background: none;
    border: none;
    color: inherit;
    text-align: left;
    cursor: pointer;
    font: inherit;
  }

  .name {
    font-weight: 600;
  }

  .cats {
    color: var(--text-dim);
    font-size: 0.82rem;
  }

  .counts {
    display: flex;
    gap: 0.6rem;
    font-size: 0.85rem;
    white-space: nowrap;
  }

  .through {
    color: var(--accent);
  }

  .stopped {
    color: var(--text-dim);
  }

  .bar {
    height: 0.4rem;
    background: var(--accent);
    border-radius: 999px;
    overflow: hidden;
  }

  .bar span {
    display: block;
    height: 100%;
    background: var(--allowed);
  }

  .detail {
    padding: 0 0 0.8rem;
  }

  .detail table {
    width: 100%;
    font-size: 0.85rem;
  }

  .detail td:not(.primary) {
    font-family: var(--font-body);
    font-weight: normal;
  }

  .detail a,
  .devices a {
    color: var(--accent);
    overflow-wrap: anywhere;
  }

  .devices {
    font-size: 0.85rem;
    margin: 0.5rem 0;
  }

  .actions {
    display: flex;
    gap: 0.75rem;
    align-items: center;
    flex-wrap: wrap;
    margin-top: 0.5rem;
  }

  .link {
    background: none;
    border: none;
    padding: 0;
    color: var(--accent);
    cursor: pointer;
    font-size: 0.85rem;
    margin-top: 0.8rem;
  }

  .other li {
    padding: 0.25rem 0;
    font-size: 0.88rem;
  }

  .attribution {
    margin-top: 1rem;
    font-size: 0.78rem;
  }

  @media (max-width: 640px) {
    .head {
      grid-template-columns: 1fr auto;
    }

    .cats {
      grid-column: 1 / -1;
      order: 3;
    }

    .bar {
      grid-column: 1 / -1;
      order: 4;
    }
  }
</style>
