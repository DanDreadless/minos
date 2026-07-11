<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type ListStatus, type Service } from '../lib/api';
  import { copy } from '../lib/copy';
  import { notify, notifyError } from '../lib/toast';

  let lists: ListStatus[] = [];
  let busy = false;
  let newName = '';
  let newUrl = '';
  let newFormat: 'hosts' | 'plain' | 'adblock' = 'hosts';
  let newAction: 'block' | 'allow' = 'block';
  let catalog: Service[] = [];
  let blockedServices = new Set<string>();

  async function loadServices(): Promise<void> {
    try {
      const view = await api.services();
      catalog = view.catalog;
      blockedServices = new Set(view.blocked);
    } catch (e) {
      notifyError(e);
    }
  }

  async function toggleService(name: string): Promise<void> {
    const next = new Set(blockedServices);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    try {
      const view = await api.updateServices({ blocked: [...next] });
      blockedServices = new Set(view.blocked);
    } catch (e) {
      notifyError(e);
      await loadServices();
    }
  }

  function fmtWhen(iso?: string): string {
    if (!iso) return '—';
    return new Date(iso).toLocaleString();
  }

  async function load(): Promise<void> {
    try {
      lists = await api.lists();
    } catch (e) {
      notifyError(e);
    }
  }

  async function refreshAll(): Promise<void> {
    busy = true;
    try {
      lists = await api.refreshLists();
      notify('All lists refreshed.');
    } catch (e) {
      notifyError(e);
    } finally {
      busy = false;
    }
  }

  async function toggle(l: ListStatus): Promise<void> {
    try {
      lists = await api.updateList(l.name, { enabled: !l.enabled });
    } catch (e) {
      notifyError(e);
      await load();
    }
  }

  async function toggleAudit(l: ListStatus): Promise<void> {
    try {
      lists = await api.updateList(l.name, { audit: !l.audit });
    } catch (e) {
      notifyError(e);
      await load();
    }
  }

  async function remove(l: ListStatus): Promise<void> {
    if (!window.confirm(copy.lists.confirmDelete(l.name))) return;
    try {
      lists = await api.deleteList(l.name);
      notify(`List "${l.name}" removed.`);
    } catch (e) {
      notifyError(e);
    }
  }

  async function add(): Promise<void> {
    busy = true;
    try {
      lists = await api.addList({
        name: newName.trim(),
        url: newUrl.trim(),
        format: newFormat,
        action: newAction,
        enabled: true,
      });
      notify(`List "${newName.trim()}" added.`);
      newName = '';
      newUrl = '';
      newFormat = 'hosts';
      newAction = 'block';
    } catch (e) {
      notifyError(e);
    } finally {
      busy = false;
    }
  }

  onMount(() => {
    void load();
    void loadServices();
  });
</script>

<h1>{copy.lists.title} <small>{copy.lists.subtitle}</small></h1>

<div class="actions">
  <button class="primary" on:click={refreshAll} disabled={busy}>
    {busy ? copy.lists.refreshing : copy.lists.refreshAll}
  </button>
</div>

{#if lists.length === 0}
  <p class="empty">{copy.lists.empty}</p>
{:else}
  <div class="table-wrap">
    <table>
      <thead>
        <tr>
          <th>On</th>
          <th title={copy.lists.auditTitle}>{copy.lists.auditHeader}</th>
          <th>Name</th>
          <th>URL</th>
          <th>Format</th>
          <th>Rules</th>
          <th>Skipped</th>
          <th>Last refresh</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each lists as l (l.name)}
          <tr class:disabled={!l.enabled}>
            <td>
              <input
                type="checkbox"
                checked={l.enabled}
                title={l.enabled ? 'disable this list' : 'enable this list'}
                on:change={() => toggle(l)}
              />
            </td>
            <td>
              {#if l.action !== 'allow'}
                <input
                  type="checkbox"
                  checked={l.audit}
                  title={copy.lists.auditTitle}
                  on:change={() => toggleAudit(l)}
                />
              {/if}
            </td>
            <td>
              {l.name}
              {#if l.audit}
                <span class="audit-badge" title={copy.lists.auditBadgeTitle}>
                  {copy.lists.auditBadge}
                </span>
              {/if}
              {#if l.action === 'allow'}
                <span class="allow-badge" title={copy.lists.allowBadgeTitle}>
                  {copy.lists.allowBadge}
                </span>
              {/if}
            </td>
            <td class="url" title={l.url}>{l.url}</td>
            <td>{l.format}</td>
            <td class="num">{l.rules.toLocaleString()}</td>
            <td class="num">{l.skipped ? l.skipped.toLocaleString() : ''}</td>
            <td>
              {#if l.last_error}
                <span class="err" title={l.last_error}>fetch failed</span>
              {:else}
                {fmtWhen(l.last_refresh)}
              {/if}
            </td>
            <td>
              <button class="row-action danger" on:click={() => remove(l)}>Remove</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<section class="card services">
  <h2>{copy.lists.servicesTitle} <small>{copy.lists.servicesHint}</small></h2>
  <div class="service-grid">
    {#each catalog as svc (svc.name)}
      <label class="service" title={copy.lists.serviceDomains(svc.domains.length)}>
        <input
          type="checkbox"
          checked={blockedServices.has(svc.name)}
          on:change={() => toggleService(svc.name)}
        />
        {svc.label}
      </label>
    {/each}
  </div>
  <p class="note">
    {copy.lists.servicesNote}
    <a href="#/devices">{copy.lists.servicesNoteLink}</a>
  </p>
</section>

<section class="card add">
  <h2>{copy.lists.addTitle}</h2>
  <form on:submit|preventDefault={add}>
    <input placeholder="name" bind:value={newName} required size="14" />
    <input
      placeholder="https://example.com/hosts.txt"
      bind:value={newUrl}
      required
      type="url"
      class="grow"
    />
    <select bind:value={newFormat} title="list format">
      <option value="hosts">hosts</option>
      <option value="plain">plain domains</option>
      <option value="adblock">adblock</option>
    </select>
    <select bind:value={newAction} title="what the list's entries do">
      <option value="block">{copy.lists.actionBlock}</option>
      <option value="allow">{copy.lists.actionAllow}</option>
    </select>
    <button type="submit" class="primary" disabled={busy || !newName.trim() || !newUrl.trim()}>
      Add list
    </button>
  </form>
</section>

<style>
  h1 small {
    color: var(--text-dim);
    font-size: 0.85rem;
    margin-left: 0.5rem;
  }

  .actions {
    margin-bottom: 1rem;
  }

  .empty {
    color: var(--text-dim);
    font-style: italic;
  }

  tr.disabled td {
    opacity: 0.45;
  }

  td.url {
    max-width: 18rem;
  }

  td.num {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }

  .err {
    color: var(--blocked);
  }

  .allow-badge {
    display: inline-block;
    margin-left: 0.4rem;
    padding: 0 0.4rem;
    border: 1px solid var(--allowed);
    border-radius: 0.6rem;
    color: var(--allowed);
    font-size: 0.68rem;
    line-height: 1.3;
    vertical-align: middle;
    cursor: help;
  }

  .audit-badge {
    display: inline-block;
    margin-left: 0.4rem;
    padding: 0 0.4rem;
    border: 1px solid var(--audit, #c9962e);
    border-radius: 0.6rem;
    color: var(--audit, #c9962e);
    font-size: 0.68rem;
    line-height: 1.3;
    vertical-align: middle;
    cursor: help;
  }

  .row-action {
    padding: 0.1rem 0.6rem;
    font-size: 0.78rem;
  }

  .add,
  .services {
    margin-top: 1.5rem;
  }

  .service-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(13rem, 1fr));
    gap: 0.35rem 1rem;
  }

  .service {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    cursor: pointer;
    font-size: 0.88rem;
    padding: 0.15rem 0;
  }

  .note {
    color: var(--text-dim);
    font-size: 0.78rem;
    margin: 0.8rem 0 0;
  }

  .note a {
    color: var(--accent);
    text-decoration: none;
  }

  .note a:hover {
    text-decoration: underline;
  }

  .add form {
    display: flex;
    flex-wrap: wrap;
    gap: 0.6rem;
    align-items: center;
  }

  .add .grow {
    flex: 1;
    min-width: 16rem;
  }
</style>
