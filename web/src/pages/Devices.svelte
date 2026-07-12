<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, type ClientOverview, type Device, type Group, type Service } from '../lib/api';
  import BarList from '../lib/components/BarList.svelte';
  import { copy } from '../lib/copy';
  import { docketHref } from '../lib/router';
  import { notify, notifyError } from '../lib/toast';

  let devices: Device[] = [];
  let groups: Group[] = [];
  let catalog: Service[] = [];
  let names: Record<string, string> = {}; // per-row label drafts
  let newGroupName = '';
  let newGroupMode = 'filter';
  let timer: ReturnType<typeof setInterval> | null = null;

  // A device is addressed by its MAC when it has one (so an assignment follows
  // it across DHCP leases), else by its IP.
  function deviceKey(d: Device): string {
    return d.mac || d.ip;
  }

  async function load(): Promise<void> {
    try {
      [devices, groups] = await Promise.all([api.clients(), api.groups()]);
      const drafts: Record<string, string> = {};
      for (const d of devices) drafts[deviceKey(d)] = d.name ?? '';
      names = drafts;
    } catch (e) {
      notifyError(e);
    }
  }

  function blurOnEnter(e: KeyboardEvent): void {
    if (e.key === 'Enter') (e.target as HTMLInputElement).blur();
  }

  function fmtLastSeen(iso?: string): string {
    if (!iso) return copy.devices.neverSeen;
    const s = (Date.now() - new Date(iso).getTime()) / 1000;
    if (s < 60) return 'just now';
    if (s < 3600) return `${Math.floor(s / 60)}m ago`;
    if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
    return `${Math.floor(s / 86400)}d ago`;
  }

  // The primary IP rides along as a last-known-address hint, needed only when
  // creating a MAC-keyed assignment for a device that's currently offline.
  async function apply(d: Device, upd: Parameters<typeof api.updateClient>[1]): Promise<void> {
    try {
      devices = await api.updateClient(deviceKey(d), { ...upd, ip: d.ip });
    } catch (e) {
      notifyError(e);
      await load();
    }
  }

  async function saveName(d: Device): Promise<void> {
    const label = (names[deviceKey(d)] ?? '').trim();
    if (label === (d.name ?? '')) return;
    await apply(d, { name: label });
  }

  async function setGroup(d: Device, ev: Event): Promise<void> {
    await apply(d, { group: (ev.target as HTMLSelectElement).value });
  }

  async function toggleBlock(d: Device): Promise<void> {
    await apply(d, { blocked: !d.blocked });
    notify(
      d.blocked ? `DNS restored for ${d.name || d.ip}.` : `DNS blocked for ${d.name || d.ip}.`,
    );
  }

  async function forget(d: Device): Promise<void> {
    try {
      devices = await api.deleteClient(deviceKey(d));
    } catch (e) {
      notifyError(e);
    }
  }

  // --- groups ---

  function domainsToText(list: string[] | null): string {
    return (list ?? []).join(', ');
  }

  function textToDomains(text: string): string[] {
    return text
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
  }

  async function addGroup(): Promise<void> {
    try {
      groups = await api.addGroup({ name: newGroupName.trim(), mode: newGroupMode });
      newGroupName = '';
      newGroupMode = 'filter';
    } catch (e) {
      notifyError(e);
    }
  }

  const weekdays = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'];

  function submitSchedule(g: Group, e: Event): void {
    const f = e.currentTarget as HTMLFormElement;
    const days = weekdays.filter(
      (d) => (f.elements.namedItem(`day-${d}`) as HTMLInputElement).checked,
    );
    const start = (f.elements.namedItem('start') as HTMLInputElement).value;
    const end = (f.elements.namedItem('end') as HTMLInputElement).value;
    if (!start || !end) return;
    void saveSchedule(g, { days: days.length === 7 ? [] : days, start, end });
  }

  async function saveSchedule(g: Group, schedule: Group['schedule']): Promise<void> {
    try {
      groups = await api.updateGroup(g.name, { schedule: schedule ?? null });
      notify(`Group "${g.name}" updated.`);
    } catch (e) {
      notifyError(e);
    }
  }

  async function saveGroupSafeSearch(g: Group): Promise<void> {
    try {
      groups = await api.updateGroup(g.name, { safe_search: !g.safe_search });
    } catch (e) {
      notifyError(e);
      await load();
    }
  }

  async function toggleGroupService(g: Group, name: string): Promise<void> {
    const next = new Set(g.services ?? []);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    try {
      groups = await api.updateGroup(g.name, { services: [...next] });
    } catch (e) {
      notifyError(e);
      await load();
    }
  }

  async function toggleGroupAllowedService(g: Group, name: string): Promise<void> {
    const next = new Set(g.allowed_services ?? []);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    try {
      groups = await api.updateGroup(g.name, { allowed_services: [...next] });
    } catch (e) {
      notifyError(e);
      await load();
    }
  }

  async function setGroupMode(g: Group, ev: Event): Promise<void> {
    try {
      groups = await api.updateGroup(g.name, {
        mode: (ev.target as HTMLSelectElement).value,
      });
    } catch (e) {
      notifyError(e);
      await load();
    }
  }

  function submitGroupLists(g: Group, e: Event): void {
    const f = e.currentTarget as HTMLFormElement;
    void saveGroupLists(
      g,
      (f.elements.namedItem('allow') as HTMLInputElement).value,
      (f.elements.namedItem('deny') as HTMLInputElement).value,
    );
  }

  async function saveGroupLists(g: Group, allowText: string, denyText: string): Promise<void> {
    try {
      groups = await api.updateGroup(g.name, {
        allowlist: textToDomains(allowText),
        denylist: textToDomains(denyText),
      });
      notify(`Group "${g.name}" updated.`);
    } catch (e) {
      notifyError(e);
    }
  }

  async function removeGroup(g: Group): Promise<void> {
    if (!window.confirm(copy.devices.confirmDelete(g.name))) return;
    try {
      groups = await api.deleteGroup(g.name);
      await load();
    } catch (e) {
      notifyError(e);
    }
  }

  function memberCount(name: string): number {
    return devices.filter((d) => d.group === name).length;
  }

  // --- per-device activity panel ---

  let statsFor: Device | null = null;
  let statsHours = 24;
  let overview: ClientOverview | null = null;

  async function showStats(d: Device, hours = statsHours): Promise<void> {
    statsFor = d;
    statsHours = hours;
    overview = null;
    try {
      overview = await api.clientStats(d.ips ?? [d.ip], hours);
    } catch (e) {
      notifyError(e);
      statsFor = null;
    }
  }

  function deviceLabel(d: Device): string {
    return d.name || d.hostname || d.ip;
  }

  onMount(() => {
    void load();
    void api
      .services()
      .then((v) => (catalog = v.catalog))
      .catch(notifyError);
    timer = setInterval(load, 15000);
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
  });
</script>

<h1>{copy.devices.title} <small>{copy.devices.subtitle}</small></h1>

{#if devices.length === 0}
  <p class="empty">{copy.devices.empty}</p>
{:else}
  <div class="table-wrap">
    <table>
      <thead>
        <tr>
          <th>IP</th>
          <th>MAC</th>
          <th>Vendor</th>
          <th>Hostname</th>
          <th>Label</th>
          <th>Group</th>
          <th>Queries</th>
          <th>Blocked</th>
          <th>Last seen</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each devices as d (d.mac || d.ip)}
          <tr class:dns-blocked={d.blocked}>
            <td>
              <a
                class="ip-link"
                href={docketHref({ clients: d.ips ?? [d.ip] })}
                title={copy.devices.viewInDocket}
              >
                {d.ip}
              </a>
              {#if d.ips && d.ips.length > 1}
                <span class="ip-more" title={d.ips.join(', ')}>+{d.ips.length - 1}</span>
              {/if}
            </td>
            <td>{d.mac ?? ''}</td>
            <td>
              {#if d.vendor}
                {d.vendor}
              {:else if d.private_mac}
                <span class="private-mac" title={copy.devices.privateMACTitle}>
                  {copy.devices.privateMAC}
                </span>
              {/if}
            </td>
            <td title={d.hostname}>{d.hostname ?? ''}</td>
            <td>
              <input
                class="label-input"
                placeholder={copy.devices.namePlaceholder}
                bind:value={names[d.mac || d.ip]}
                on:blur={() => saveName(d)}
                on:keydown={blurOnEnter}
              />
            </td>
            <td>
              <select value={d.group} on:change={(e) => setGroup(d, e)}>
                <option value="default">{copy.devices.groupDefault}</option>
                {#each groups as g (g.name)}
                  <option value={g.name}>{g.name} ({g.mode})</option>
                {/each}
              </select>
            </td>
            <td class="num">
              <button
                class="count-link"
                title={copy.devices.activityHint}
                on:click={() => showStats(d)}
              >
                {d.queries.toLocaleString()}
              </button>
            </td>
            <td class="num">{d.queries_blocked.toLocaleString()}</td>
            <td>{fmtLastSeen(d.last_seen)}</td>
            <td class="row-actions">
              {#if d.blocked}
                <span class="badge blocked">{copy.devices.blockedBadge}</span>
                <button class="row-action" on:click={() => toggleBlock(d)}>
                  {copy.devices.unblockAction}
                </button>
              {:else}
                <button
                  class="row-action danger"
                  title={copy.devices.blockHint}
                  on:click={() => toggleBlock(d)}
                >
                  {copy.devices.blockAction}
                </button>
              {/if}
              {#if d.name || d.group !== 'default'}
                <button
                  class="row-action subtle"
                  title={copy.devices.forgetHint}
                  on:click={() => forget(d)}
                >
                  {copy.devices.forget}
                </button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

{#if statsFor}
  <section class="card activity">
    <div class="activity-head">
      <h2>
        {deviceLabel(statsFor)}
        <small>{copy.devices.activityTitle(statsHours)}</small>
      </h2>
      <div class="activity-controls">
        <button class="row-action" class:active={statsHours === 24} on:click={() => showStats(statsFor!, 24)}>
          24h
        </button>
        <button class="row-action" class:active={statsHours === 168} on:click={() => showStats(statsFor!, 168)}>
          7d
        </button>
        <a class="row-action" href={docketHref({ clients: statsFor.ips ?? [statsFor.ip] })}>
          {copy.devices.viewInDocket}
        </a>
        <button class="row-action subtle" on:click={() => (statsFor = null)}>✕</button>
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
              href: docketHref({ clients: statsFor?.ips ?? [statsFor?.ip ?? ''], qname: x.qname }),
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
              href: docketHref({ clients: statsFor?.ips ?? [statsFor?.ip ?? ''], qname: x.qname }),
            }))}
          />
        </div>
      </div>
    {:else}
      <p class="empty">{copy.devices.activityLoading}</p>
    {/if}
  </section>
{/if}

<section class="groups">
  <h2>{copy.devices.groupsTitle} <small>{copy.devices.groupsHint}</small></h2>
  {#if groups.length === 0}
    <p class="empty">{copy.devices.groupsEmpty}</p>
  {/if}
  {#each groups as g (g.name)}
    <div class="card group">
      <div class="group-head">
        <span class="group-name">{g.name}</span>
        <span class="members">{memberCount(g.name)} device(s)</span>
        <select value={g.mode} on:change={(e) => setGroupMode(g, e)}>
          <option value="filter">{copy.devices.modeFilter}</option>
          <option value="bypass">{copy.devices.modeBypass}</option>
          <option value="block">{copy.devices.modeBlock}</option>
        </select>
        <button class="row-action danger" on:click={() => removeGroup(g)}>Delete</button>
      </div>
      {#if g.mode === 'filter'}
        {@const allowText = domainsToText(g.allowlist)}
        {@const denyText = domainsToText(g.denylist)}
        <form class="group-lists" on:submit|preventDefault={(e) => submitGroupLists(g, e)}>
          <label>
            <span>{copy.devices.extraAllow}</span>
            <input name="allow" value={allowText} placeholder={copy.devices.listPlaceholder} />
          </label>
          <label>
            <span>{copy.devices.extraDeny}</span>
            <input name="deny" value={denyText} placeholder={copy.devices.listPlaceholder} />
          </label>
          <button type="submit" class="primary">{copy.settings.save}</button>
        </form>
        <label class="group-safesearch">
          <input
            type="checkbox"
            checked={g.safe_search}
            on:change={() => void saveGroupSafeSearch(g)}
          />
          {copy.devices.groupSafeSearch}
        </label>
        <details class="group-services">
          <summary>
            {copy.devices.groupServices}
            {#if g.services?.length}
              <span class="count">({g.services.length})</span>
            {/if}
          </summary>
          <div class="service-grid">
            {#each catalog as svc (svc.name)}
              <label class="service">
                <input
                  type="checkbox"
                  checked={(g.services ?? []).includes(svc.name)}
                  on:change={() => toggleGroupService(g, svc.name)}
                />
                {svc.label}
              </label>
            {/each}
          </div>
        </details>
        <details class="group-services">
          <summary>
            {copy.devices.groupAllowedServices}
            {#if g.allowed_services?.length}
              <span class="count">({g.allowed_services.length})</span>
            {/if}
          </summary>
          <div class="service-grid">
            {#each catalog as svc (svc.name)}
              <label class="service">
                <input
                  type="checkbox"
                  checked={(g.allowed_services ?? []).includes(svc.name)}
                  on:change={() => toggleGroupAllowedService(g, svc.name)}
                />
                {svc.label}
              </label>
            {/each}
          </div>
        </details>
      {/if}
      <details class="group-schedule">
        <summary>
          {copy.devices.scheduleSummary}
          {#if g.schedule}
            <span class="count">({copy.devices.scheduleOn(g.schedule.start, g.schedule.end)})</span>
          {/if}
        </summary>
        <form class="schedule-form" on:submit|preventDefault={(e) => submitSchedule(g, e)}>
          <div class="day-row">
            {#each weekdays as d (d)}
              <label class="day">
                <input
                  type="checkbox"
                  name="day-{d}"
                  checked={!g.schedule?.days?.length || g.schedule.days.includes(d)}
                />
                {d}
              </label>
            {/each}
          </div>
          <div class="time-row">
            <input type="time" name="start" value={g.schedule?.start ?? '21:00'} required />
            <span class="range-dash">–</span>
            <input type="time" name="end" value={g.schedule?.end ?? '07:00'} required />
            <button type="submit" class="primary">{copy.devices.scheduleSet}</button>
            {#if g.schedule}
              <button type="button" on:click={() => saveSchedule(g, null)}>
                {copy.devices.scheduleClear}
              </button>
            {/if}
          </div>
          <p class="schedule-note">{copy.devices.scheduleNote}</p>
        </form>
      </details>
    </div>
  {/each}

  <div class="card add">
    <h2>{copy.devices.addTitle}</h2>
    <form on:submit|preventDefault={addGroup}>
      <input placeholder="group name" bind:value={newGroupName} required size="14" />
      <select bind:value={newGroupMode}>
        <option value="filter">{copy.devices.modeFilter}</option>
        <option value="bypass">{copy.devices.modeBypass}</option>
        <option value="block">{copy.devices.modeBlock}</option>
      </select>
      <button type="submit" class="primary" disabled={!newGroupName.trim()}>Create</button>
    </form>
  </div>
</section>

<style>
  h1 small,
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

  td.num {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }

  .ip-link {
    color: inherit;
    text-decoration: none;
  }

  .ip-link:hover {
    color: var(--accent);
    text-decoration: underline;
  }

  .ip-more {
    margin-left: 0.35rem;
    font-size: 0.7rem;
    color: var(--text-dim);
    cursor: help;
  }

  .private-mac {
    color: var(--text-dim);
    font-style: italic;
    font-size: 0.82rem;
    cursor: help;
  }

  tr.dns-blocked td {
    opacity: 0.6;
  }

  .label-input {
    width: 9rem;
    padding: 0.15rem 0.4rem;
    font-size: 0.82rem;
  }

  .row-actions {
    white-space: nowrap;
  }

  .row-action {
    padding: 0.1rem 0.6rem;
    font-size: 0.75rem;
  }

  .row-action.subtle {
    opacity: 0.55;
  }

  .count-link {
    background: none;
    border: none;
    padding: 0;
    font: inherit;
    font-variant-numeric: tabular-nums;
    color: inherit;
    cursor: pointer;
  }

  .count-link:hover {
    color: var(--accent);
    text-decoration: underline;
  }

  .activity {
    margin-top: 1.5rem;
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

  .activity-controls a.row-action {
    text-decoration: none;
  }

  .activity-controls .row-action.active {
    color: var(--accent);
    border-color: var(--accent);
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

  .groups {
    margin-top: 1.75rem;
  }

  .group {
    margin-bottom: 0.9rem;
  }

  .group-head {
    display: flex;
    align-items: center;
    gap: 0.8rem;
    flex-wrap: wrap;
  }

  .group-name {
    font-family: var(--font-display);
    font-size: 1.05rem;
    color: var(--accent);
  }

  .members {
    color: var(--text-dim);
    font-size: 0.78rem;
  }

  .group-head select {
    margin-left: auto;
    max-width: 24rem;
  }

  .group-lists {
    display: flex;
    gap: 0.8rem;
    align-items: end;
    flex-wrap: wrap;
    margin-top: 0.8rem;
  }

  .group-lists label {
    flex: 1;
    min-width: 14rem;
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.78rem;
    color: var(--text-dim);
  }

  .add form {
    display: flex;
    gap: 0.6rem;
    flex-wrap: wrap;
    align-items: center;
  }

  .group-services,
  .group-schedule {
    margin-top: 0.8rem;
  }

  .group-safesearch {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    margin-top: 0.8rem;
    font-size: 0.84rem;
    cursor: pointer;
    color: var(--text-dim);
  }

  .group-services summary,
  .group-schedule summary {
    cursor: pointer;
    color: var(--text-dim);
    font-size: 0.82rem;
  }

  .group-services .count,
  .group-schedule .count {
    color: var(--accent);
  }

  .schedule-form {
    margin-top: 0.6rem;
  }

  .day-row {
    display: flex;
    gap: 0.9rem;
    flex-wrap: wrap;
    margin-bottom: 0.6rem;
  }

  .day {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    font-size: 0.8rem;
    cursor: pointer;
    text-transform: capitalize;
  }

  .time-row {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-wrap: wrap;
  }

  .range-dash {
    color: var(--text-dim);
  }

  .schedule-note {
    color: var(--text-dim);
    font-size: 0.75rem;
    margin: 0.6rem 0 0;
  }

  .service-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(12rem, 1fr));
    gap: 0.3rem 1rem;
    margin-top: 0.6rem;
  }

  .service {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    cursor: pointer;
    font-size: 0.84rem;
  }
</style>
