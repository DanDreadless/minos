<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type Device, type Group, type Rule, type TemporaryAllow } from '../lib/api';
  import { checkOpen } from '../lib/check';
  import { copy } from '../lib/copy';
  import { fmtClock } from '../lib/format';
  import { currentParams, deviceHref } from '../lib/router';
  import { deviceKey, deviceLabel, findByKey, scopeText } from '../lib/rules';
  import { notify, notifyError } from '../lib/toast';

  // DNS Filter → Rules: every domain you allow or block, for everyone, a
  // group or a device, in one table — firewall style. Services have their
  // own tab. The old Allowed and Blocked tabs (#/allowed, #/blocked) open
  // here with the matching filter.

  let rules: Rule[] = [];
  let devices: Device[] = [];
  let groups: Group[] = [];
  let temporary: TemporaryAllow[] = [];
  let loaded = false;

  // Filters. Who is '' (anyone), 'everyone', 'group:<name>' or 'device:<key>'.
  let actionFilter: '' | 'allow' | 'block' = '';
  let who = '';
  let search = '';

  // The add form.
  let newAction: 'allow' | 'block' = 'block';
  let newDomain = '';
  let newWho = 'everyone';
  let adding = false;

  async function load(): Promise<void> {
    try {
      [rules, devices, groups, temporary] = await Promise.all([
        api.rules(),
        api.clients(),
        api.groups(),
        api.temporaryAllows(),
      ]);
    } catch (e) {
      notifyError(e);
    } finally {
      loaded = true;
    }
  }

  function scopeKey(r: Pick<Rule, 'scope' | 'target'>): string {
    return r.scope === 'everyone' ? 'everyone' : `${r.scope}:${r.target}`;
  }

  // Device rules are keyed by MAC; match a filter case-insensitively.
  function sameScope(a: string, b: string): boolean {
    return a.toLowerCase() === b.toLowerCase();
  }

  $: domainRules = rules.filter((r) => r.kind === 'domain');
  $: shown = domainRules.filter(
    (r) =>
      (!actionFilter || r.action === actionFilter) &&
      (!who || sameScope(scopeKey(r), who)) &&
      (!search || r.value.toLowerCase().includes(search.trim().toLowerCase())),
  );
  // Temporary allows are everyone-wide; show them when the filters allow.
  $: shownTemporary = temporary.filter(
    (t) =>
      actionFilter !== 'block' &&
      (!who || who === 'everyone') &&
      (!search || t.domain.includes(search.trim().toLowerCase())),
  );

  // Scopes that have rules, for the filter, plus every group and device for
  // the add form.
  $: namedDevices = [...devices].sort((a, b) => deviceLabel(a).localeCompare(deviceLabel(b)));

  // A device rule whose device is in a bypass or block group does nothing.
  function inert(r: Rule, all: Device[], gs: Group[]): string {
    const gname = r.scope === 'group' ? r.target : r.scope === 'device' ? findByKey(all, r.target ?? '')?.group : '';
    const g = gs.find((x) => x.name === gname);
    return g && g.mode !== 'filter' ? copy.rules.groupBypassNote(g.name) : '';
  }

  function scopeLink(r: Rule, all: Device[]): string {
    if (r.scope === 'device') {
      const d = findByKey(all, r.target ?? '');
      return d ? deviceHref(deviceKey(d)) : '';
    }
    return '';
  }

  function toRule(action: 'allow' | 'block', value: string, w: string): Rule {
    if (w === 'everyone') return { action, kind: 'domain', value, scope: 'everyone' };
    const i = w.indexOf(':');
    return { action, kind: 'domain', value, scope: w.slice(0, i) as Rule['scope'], target: w.slice(i + 1) };
  }

  async function add(): Promise<void> {
    const value = newDomain.trim();
    if (!value) return;
    adding = true;
    try {
      const rule = toRule(newAction, value, newWho);
      const dev = rule.scope === 'device' ? findByKey(devices, rule.target ?? '') : undefined;
      rules = await api.addRule(rule, dev?.ip);
      const whoText =
        rule.scope === 'everyone'
          ? copy.rules.forEveryone
          : rule.scope === 'group'
            ? copy.rules.forGroup(rule.target ?? '')
            : copy.rules.forDevice(deviceLabel(dev, rule.target));
      notify(newAction === 'allow' ? copy.rules.allowedDone(value, whoText) : copy.rules.blockedDone(value, whoText));
      newDomain = '';
    } catch (e) {
      notifyError(e);
    } finally {
      adding = false;
    }
  }

  async function remove(r: Rule): Promise<void> {
    try {
      rules = await api.removeRule(r);
      notify(copy.rules.removed(r.value));
    } catch (e) {
      notifyError(e);
      await load();
    }
  }

  async function endTemporary(domain: string): Promise<void> {
    try {
      await api.removeTemporaryAllow(domain);
      temporary = temporary.filter((t) => t.domain !== domain);
    } catch (e) {
      notifyError(e);
    }
  }

  onMount(() => {
    // Deep links: ?for=<scope> and the old tab addresses.
    const p = currentParams();
    if (p.for) {
      who = p.for;
      if (p.for !== 'everyone') newWho = p.for;
    }
    if (location.hash.startsWith('#/allowed')) actionFilter = 'allow';
    else if (location.hash.startsWith('#/blocked')) actionFilter = 'block';
    if (p.action === 'allow' || p.action === 'block') actionFilter = p.action;
    void load();
    const t = setInterval(() => {
      const now = Date.now();
      temporary = temporary.filter((x) => new Date(x.until).getTime() > now);
    }, 30000);
    return () => clearInterval(t);
  });
</script>

<div class="intro">
  <p>{copy.rules.intro}</p>
  <button class="small" on:click={() => checkOpen.set(true)}>{copy.blocking.checkButton}</button>
</div>

<section class="card">
  <h2>{copy.rules.addTitle}</h2>
  <form class="add" on:submit|preventDefault={add}>
    <label>
      <span>{copy.rules.addAction}</span>
      <select bind:value={newAction}>
        <option value="block">{copy.rules.blockBadge}</option>
        <option value="allow">{copy.rules.allowBadge}</option>
      </select>
    </label>
    <label class="grow">
      <span>{copy.rules.addDomain}</span>
      <input placeholder={copy.domains.addPlaceholder} bind:value={newDomain} required />
    </label>
    <label>
      <span>{copy.rules.addWhere}</span>
      <select bind:value={newWho}>
        <option value="everyone">{copy.rules.everyone}</option>
        {#if groups.length}
          <optgroup label={copy.rules.groupsHeading}>
            {#each groups as g (g.name)}
              <option value="group:{g.name}">{g.name}</option>
            {/each}
          </optgroup>
        {/if}
        {#if namedDevices.length}
          <optgroup label={copy.rules.devicesHeading}>
            {#each namedDevices as d, i (i)}
              <option value="device:{deviceKey(d)}">{deviceLabel(d)}{deviceLabel(d) !== d.ip ? ` (${d.ip})` : ''}</option>
            {/each}
          </optgroup>
        {/if}
      </select>
    </label>
    <button type="submit" class="primary" disabled={adding || !newDomain.trim()}>{copy.blocking.add}</button>
  </form>
</section>

<section class="card">
  <h2>
    {copy.rules.listTitle}
    <span class="count">{domainRules.length + temporary.length}</span>
    <small>{copy.rules.listHint}</small>
  </h2>

  <div class="filters">
    <label>
      <span>{copy.rules.filterAction}</span>
      <select bind:value={actionFilter}>
        <option value="">{copy.rules.filterAllActions}</option>
        <option value="allow">{copy.rules.filterAllows}</option>
        <option value="block">{copy.rules.filterBlocks}</option>
      </select>
    </label>
    <label>
      <span>{copy.rules.filterWho}</span>
      <select bind:value={who}>
        <option value="">{copy.rules.filterAnyone}</option>
        <option value="everyone">{copy.rules.everyone}</option>
        {#if groups.length}
          <optgroup label={copy.rules.groupsHeading}>
            {#each groups as g (g.name)}
              <option value="group:{g.name}">{g.name}</option>
            {/each}
          </optgroup>
        {/if}
        {#if namedDevices.length}
          <optgroup label={copy.rules.devicesHeading}>
            {#each namedDevices as d, i (i)}
              <option value="device:{deviceKey(d)}">{deviceLabel(d)}</option>
            {/each}
          </optgroup>
        {/if}
      </select>
    </label>
    <input class="search" type="search" placeholder={copy.rules.search} bind:value={search} />
  </div>

  {#if !loaded}
    <p class="empty">{copy.rules.loading}</p>
  {:else if domainRules.length === 0 && temporary.length === 0}
    <p class="empty">{copy.rules.empty}</p>
  {:else if shown.length === 0 && shownTemporary.length === 0}
    <p class="empty">{copy.rules.emptyFiltered}</p>
  {:else}
    <table class="stack">
      <thead>
        <tr>
          <th>{copy.rules.colAction}</th>
          <th>{copy.rules.colDomain}</th>
          <th>{copy.rules.colWho}</th>
          <th aria-label={copy.blocking.remove}></th>
        </tr>
      </thead>
      <tbody>
        {#each shown as r, i (i)}
          {@const note = inert(r, devices, groups)}
          {@const link = scopeLink(r, devices)}
          <tr>
            <td data-label={copy.rules.colAction}>
              <span class="badge" class:allow={r.action === 'allow'} class:block={r.action === 'block'}
                >{r.action === 'allow' ? copy.rules.allowBadge : copy.rules.blockBadge}</span
              >
            </td>
            <td class="primary mono">{r.value}</td>
            <td data-label={copy.rules.colWho}>
              {#if link}<a href={link}>{scopeText(r, devices)}</a>{:else}{scopeText(r, devices)}{/if}
              {#if note}<small class="inert">{note}</small>{/if}
            </td>
            <td class="end">
              <button class="chip-x" title={copy.rules.remove(r.value)} aria-label={copy.rules.remove(r.value)} on:click={() => remove(r)}
                >×</button
              >
            </td>
          </tr>
        {/each}
        {#each shownTemporary as t (t.domain)}
          <tr>
            <td data-label={copy.rules.colAction}>
              <span class="badge allow">{copy.rules.temporaryRow(fmtClock(t.until))}</span>
            </td>
            <td class="primary mono">{t.domain}</td>
            <td data-label={copy.rules.colWho}>{copy.rules.everyone}</td>
            <td class="end">
              <button class="chip-x" title={copy.allow.endNow(t.domain)} aria-label={copy.allow.endNow(t.domain)} on:click={() => endTemporary(t.domain)}
                >×</button
              >
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<style>
  section + section {
    margin-top: 1.1rem;
  }

  .intro {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.6rem 1rem;
    margin: 0 0 1rem;
  }

  .intro p {
    margin: 0;
    color: var(--text-dim);
    font-size: 0.88rem;
    max-width: 52rem;
    line-height: 1.45;
  }

  button.small {
    padding: 0.2rem 0.7rem;
    font-size: 0.8rem;
  }

  h2 {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.3rem 0.5rem;
  }

  h2 small {
    color: var(--text-dim);
    font-size: 0.78rem;
    flex-basis: 100%;
    font-family: var(--font-body, inherit);
  }

  .count {
    font-size: 0.75rem;
    color: var(--text-dim);
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 0 0.5rem;
    font-family: var(--font-body, inherit);
  }

  .add,
  .filters {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: 0.6rem;
  }

  .filters {
    margin-bottom: 0.9rem;
  }

  .add label,
  .filters label {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.78rem;
    color: var(--text-dim);
  }

  .add .grow {
    flex: 1 1 14rem;
  }

  .add select,
  .filters select {
    max-width: 16rem;
  }

  .search {
    flex: 1 1 12rem;
    max-width: 20rem;
  }

  table {
    width: 100%;
    border-collapse: collapse;
  }

  th {
    text-align: left;
    font-weight: normal;
    color: var(--text-dim);
    font-size: 0.78rem;
    padding: 0.3rem 0.5rem;
    border-bottom: 1px solid var(--border);
  }

  td {
    padding: 0.4rem 0.5rem;
    border-bottom: 1px solid var(--border);
    font-size: 0.88rem;
    vertical-align: middle;
  }

  .mono {
    font-family: var(--font-mono);
    overflow-wrap: anywhere;
  }

  .badge {
    display: inline-block;
    font-size: 0.75rem;
    padding: 0.05rem 0.55rem;
    border-radius: 999px;
    border: 1px solid var(--border);
    white-space: nowrap;
  }

  .badge.allow {
    color: var(--allowed);
    border-color: color-mix(in srgb, var(--allowed) 55%, var(--border));
  }

  .badge.block {
    color: var(--blocked);
    border-color: color-mix(in srgb, var(--blocked) 55%, var(--border));
  }

  td a {
    color: var(--accent);
    text-decoration: none;
  }

  td a:hover {
    text-decoration: underline;
  }

  .inert {
    display: block;
    color: var(--accent);
    font-size: 0.75rem;
  }

  .end {
    text-align: right;
  }

  .chip-x {
    all: unset;
    cursor: pointer;
    width: 1.6rem;
    height: 1.6rem;
    border-radius: 50%;
    text-align: center;
    line-height: 1.6rem;
    color: var(--text-dim);
  }

  .chip-x:hover,
  .chip-x:focus-visible {
    color: var(--blocked);
    background: var(--bg-hover);
  }

  .empty {
    color: var(--text-dim);
    font-style: italic;
    margin: 0;
  }
</style>
