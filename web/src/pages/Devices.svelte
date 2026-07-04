<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, type Device, type Group, type Service } from '../lib/api';
  import { copy } from '../lib/copy';
  import { notify, notifyError } from '../lib/toast';

  let devices: Device[] = [];
  let groups: Group[] = [];
  let catalog: Service[] = [];
  let names: Record<string, string> = {}; // per-row label drafts
  let newGroupName = '';
  let newGroupMode = 'filter';
  let timer: ReturnType<typeof setInterval> | null = null;

  async function load(): Promise<void> {
    try {
      [devices, groups] = await Promise.all([api.clients(), api.groups()]);
      const drafts: Record<string, string> = {};
      for (const d of devices) drafts[d.ip] = d.name ?? '';
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

  async function apply(ip: string, upd: Parameters<typeof api.updateClient>[1]): Promise<void> {
    try {
      devices = await api.updateClient(ip, upd);
    } catch (e) {
      notifyError(e);
      await load();
    }
  }

  async function saveName(d: Device): Promise<void> {
    const label = (names[d.ip] ?? '').trim();
    if (label === (d.name ?? '')) return;
    await apply(d.ip, { name: label });
  }

  async function setGroup(d: Device, ev: Event): Promise<void> {
    await apply(d.ip, { group: (ev.target as HTMLSelectElement).value });
  }

  async function toggleBlock(d: Device): Promise<void> {
    await apply(d.ip, { blocked: !d.blocked });
    notify(
      d.blocked ? `DNS restored for ${d.name || d.ip}.` : `DNS blocked for ${d.name || d.ip}.`,
    );
  }

  async function forget(d: Device): Promise<void> {
    try {
      devices = await api.deleteClient(d.ip);
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
        {#each devices as d (d.ip)}
          <tr class:dns-blocked={d.blocked}>
            <td>{d.ip}</td>
            <td>{d.mac ?? ''}</td>
            <td title={d.hostname}>{d.hostname ?? ''}</td>
            <td>
              <input
                class="label-input"
                placeholder={copy.devices.namePlaceholder}
                bind:value={names[d.ip]}
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
            <td class="num">{d.queries.toLocaleString()}</td>
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
      {/if}
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

  .group-services {
    margin-top: 0.8rem;
  }

  .group-services summary {
    cursor: pointer;
    color: var(--text-dim);
    font-size: 0.82rem;
  }

  .group-services .count {
    color: var(--accent);
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
