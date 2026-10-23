<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type ListStats, type ListStatus, type Service } from '../lib/api';
  import { blocklistPresets, blocklistTiers, type BlocklistPreset } from '../lib/blocklists';
  import { confirmDialog } from '../lib/confirm';
  import { copy } from '../lib/copy';
  import { fmtAgo } from '../lib/format';
  import { docketHref } from '../lib/router';
  import { notify, notifyError } from '../lib/toast';

  let lists: ListStatus[] = [];
  let busy = false;
  let newName = '';
  let newUrl = '';
  let newFormat: 'hosts' | 'plain' | 'adblock' = 'hosts';
  let newAction: 'block' | 'allow' = 'block';
  // Only used to label service:* rows in the builtin block stats.
  let catalog: Service[] = [];
  let listStats: ListStats | null = null;

  // Blocks attributed per list over the stats window, keyed by list name.
  $: blocksByList = new Map((listStats?.lists ?? []).map((s) => [s.list, s.count]));
  // Attributions that aren't a subscribed list: the user's own deny domains,
  // blocked services, group overlays, and device blocks.
  $: builtinStats = (listStats?.lists ?? []).filter((s) => !lists.some((l) => l.name === s.list));

  // services is a parameter, not the closure's catalog: the catalog loads
  // asynchronously, and Svelte only re-runs a template expression when a
  // variable named in it changes — read from closure, a label rendered
  // before the catalog arrived stayed the raw "service:<id>" for good.
  function builtinLabel(name: string, services: Service[]): string {
    if (name.startsWith('service:')) {
      const raw = name.slice('service:'.length);
      const svc = services.find((c) => c.name === raw);
      return copy.lists.statService(svc ? svc.label : raw);
    }
    if (name.startsWith('group:')) return copy.lists.statGroup(name.slice('group:'.length));
    if (name === 'denylist') return copy.lists.statDenylist;
    if (name === 'clients') return copy.lists.statClients;
    return name;
  }

  async function loadStats(): Promise<void> {
    try {
      listStats = await api.listStats();
    } catch {
      // Decorative: the lists table works without the block counts.
    }
  }

  async function loadCatalog(): Promise<void> {
    try {
      catalog = (await api.services()).catalog;
    } catch {
      // Decorative: only labels service:* rows in the builtin block stats.
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

  // How often every list is re-downloaded. Lives here, beside the lists it
  // governs, rather than in Settings.
  let refreshInterval = '';
  let savedInterval = '';

  async function loadInterval(): Promise<void> {
    try {
      refreshInterval = savedInterval = (await api.getConfig()).lists.refresh_interval;
    } catch {
      // cosmetic: the field simply stays empty
    }
  }

  async function saveInterval(): Promise<void> {
    try {
      const cfg = await api.updateConfig({ lists: { refresh_interval: refreshInterval.trim() } });
      refreshInterval = savedInterval = cfg.lists.refresh_interval;
      notify(copy.settings.saved);
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
    if (!(await confirmDialog({ message: copy.lists.confirmDelete(l.name), confirm: copy.confirm.removeList, danger: true })))
      return;
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

  // A catalog card is "Added" when a subscribed source carries its exact URL,
  // like the resolver picker matches presets by exact fields.
  $: subscribedURLs = new Set(lists.map((l) => l.url));

  // A subscription to a URL a catalog entry supersedes (a retired list).
  function replacementFor(l: ListStatus): BlocklistPreset | undefined {
    return blocklistPresets.find((p) => p.replaces?.includes(l.url));
  }

  // Catalog mirrors a subscription is missing — lists subscribed before the
  // catalog carried mirrors, or added by hand with a catalog URL.
  function missingMirrors(l: ListStatus): string[] {
    const p = blocklistPresets.find((p) => p.list.url === l.url);
    if (!p?.list.mirrors?.length || l.mirrors?.length) return [];
    return p.list.mirrors;
  }

  // replaceWith subscribes the replacement first, then removes the retired
  // list, so there is never a moment with neither. The old list's on/off and
  // audit settings carry over.
  async function replaceWith(l: ListStatus, p: BlocklistPreset): Promise<void> {
    busy = true;
    try {
      if (!lists.some((x) => x.name === p.list.name)) {
        await api.addList({ ...p.list, enabled: l.enabled, audit: l.audit });
      }
      lists = await api.deleteList(l.name);
      notify(copy.lists.replaced(l.name, p.list.name));
    } catch (e) {
      notifyError(e);
    } finally {
      busy = false;
    }
  }

  async function addMirrors(l: ListStatus, mirrors: string[]): Promise<void> {
    busy = true;
    try {
      lists = await api.updateList(l.name, { mirrors });
      notify(copy.lists.mirrorsAdded(l.name, mirrors.length));
    } catch (e) {
      notifyError(e);
    } finally {
      busy = false;
    }
  }

  async function addPreset(p: BlocklistPreset): Promise<void> {
    busy = true;
    try {
      // Catalog entries carry their mirrors, so a one-click subscription gets
      // the fallbacks too.
      lists = await api.addList({ ...p.list, enabled: true });
      notify(`List "${p.list.name}" added.`);
    } catch (e) {
      notifyError(e);
    } finally {
      busy = false;
    }
  }

  onMount(() => {
    void load();
    void loadCatalog();
    void loadStats();
    void loadInterval();
  });
</script>

<p class="intro">{copy.lists.subtitle}</p>

<div class="actions">
  <button class="primary" on:click={refreshAll} disabled={busy}>
    {busy ? copy.lists.refreshing : copy.lists.refreshAll}
  </button>
  <form class="interval" on:submit|preventDefault={saveInterval}>
    <label title={copy.settings.refreshIntervalHint}>
      {copy.lists.refreshEvery}
      <input bind:value={refreshInterval} size="6" aria-label={copy.settings.refreshInterval} />
    </label>
    {#if refreshInterval.trim() && refreshInterval.trim() !== savedInterval}
      <button type="submit">{copy.settings.save}</button>
    {/if}
  </form>
</div>

{#if lists.length === 0}
  <p class="empty">{copy.lists.empty}</p>
{:else}
  <div class="table-wrap">
    <table class="stack">
      <thead>
        <tr>
          <th>On</th>
          <th title={copy.lists.auditTitle}>{copy.lists.auditHeader}</th>
          <th>Name</th>
          <th>URL</th>
          <th>Format</th>
          <th>Rules</th>
          <th>Skipped</th>
          <th>{copy.lists.blocksHeader}</th>
          <th>{copy.lists.statusHeader}</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each lists as l (l.name)}
          <tr class:disabled={!l.enabled}>
            <td data-label="On">
              <input
                type="checkbox"
                checked={l.enabled}
                title={l.enabled ? 'disable this list' : 'enable this list'}
                on:change={() => toggle(l)}
              />
            </td>
            <td data-label={l.action !== 'allow' ? copy.lists.auditHeader : undefined}>
              {#if l.action !== 'allow'}
                <input
                  type="checkbox"
                  checked={l.audit}
                  title={copy.lists.auditTitle}
                  on:change={() => toggleAudit(l)}
                />
              {/if}
            </td>
            <td class="primary">
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
            <td
              class="url full"
              title={l.mirrors?.length
                ? `${l.url}\n${copy.lists.mirrorsHint(l.mirrors.length)}\n${l.mirrors.join('\n')}`
                : l.url}
            >
              {l.url}{#if l.mirrors?.length}<span class="mirror-count"> +{l.mirrors.length}</span>{/if}
            </td>
            <td>{l.format}</td>
            <td class="num" data-label="Rules">{l.rules.toLocaleString()}</td>
            <td class="num" data-label="Skipped">{l.skipped ? l.skipped.toLocaleString() : ''}</td>
            <td class="num" data-label={copy.lists.blocksHeader}>
              {#if l.action === 'allow'}
                <span class="quiet" title={copy.lists.allowStatTitle}>—</span>
              {:else if listStats}
                {#if blocksByList.get(l.name)}
                  <a
                    class="count-link"
                    href={docketHref({ list: l.name })}
                    title={copy.lists.blocksLinkTitle}
                  >
                    {blocksByList.get(l.name)?.toLocaleString()}
                  </a>
                {:else}
                  <span class="quiet" title={copy.lists.noBlocksTitle}>{copy.lists.noBlocks}</span>
                {/if}
              {/if}
            </td>
            <td
              class="state full {l.state}"
              title={`${copy.lists.stateHint(l.state, l.last_error ?? '')}${l.last_refresh ? ` Copy in force: ${fmtWhen(l.last_refresh)}.` : ''}`}
            >
              {copy.lists.state(l.state, fmtAgo(l.last_refresh))}
              {#if l.served_by}
                <span
                  class="mirror-badge"
                  title={copy.lists.viaMirrorHint(l.served_by, l.primary_error ?? '')}
                  >{copy.lists.viaMirror}</span
                >
              {/if}
            </td>
            <td class="row-actions end">
              {#if replacementFor(l)}
                {@const p = replacementFor(l)}
                {#if p}
                  <button
                    class="row-action primary"
                    disabled={busy}
                    title={copy.lists.replaceHint(p.label)}
                    on:click={() => replaceWith(l, p)}>{copy.lists.replace(p.label)}</button
                  >
                {/if}
              {/if}
              {#if missingMirrors(l).length}
                <button
                  class="row-action"
                  disabled={busy}
                  title={copy.lists.addMirrorsHint(missingMirrors(l))}
                  on:click={() => addMirrors(l, missingMirrors(l))}>{copy.lists.addMirrors}</button
                >
              {/if}
              <button class="row-action danger" on:click={() => remove(l)}>Remove</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

{#if builtinStats.length > 0}
  <p class="builtin">
    {copy.lists.builtinStatsTitle}
    {#each builtinStats as s, i (s.list)}{i > 0 ? ' · ' : ' '}{builtinLabel(s.list, catalog)}:
      <a class="count-link" href={docketHref({ list: s.list })} title={copy.lists.blocksLinkTitle}
        >{s.count.toLocaleString()}</a
      >{/each}
  </p>
{/if}

<section class="card catalog">
  <h2>{copy.lists.catalogTitle} <small>{copy.lists.catalogHint}</small></h2>
  {#each blocklistTiers as tier (tier)}
    <h3>
      {copy.lists.tiers[tier].label}
      <small>{copy.lists.tiers[tier].hint}</small>
    </h3>
    <div class="preset-grid">
      {#each blocklistPresets.filter((p) => p.tier === tier) as p (p.id)}
        <div class="preset">
          <div class="preset-head">
            <span class="preset-label">{p.label}</span>
            <span class="preset-size">{p.size}</span>
          </div>
          <p class="preset-note">{p.note}</p>
          {#if subscribedURLs.has(p.list.url)}
            <span class="preset-added">{copy.lists.catalogAdded}</span>
          {:else}
            <button class="row-action" disabled={busy} on:click={() => addPreset(p)}>
              {copy.lists.catalogAdd}
            </button>
          {/if}
        </div>
      {/each}
    </div>
  {/each}
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
  .interval {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    margin-left: 1rem;
    font-size: 0.85rem;
    color: var(--text-dim);
  }

  .interval label {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
  }

  .intro {
    color: var(--text-dim);
    font-size: 0.85rem;
    margin: 0 0 0.9rem;
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

  .state {
    font-size: 0.82rem;
    cursor: help;
  }

  .row-actions {
    white-space: nowrap;
  }

  .row-actions .row-action + .row-action {
    margin-left: 0.35rem;
  }

  .mirror-badge,
  .mirror-count {
    color: var(--text-dim);
    font-size: 0.75rem;
  }

  .mirror-badge {
    margin-left: 0.35rem;
    padding: 0 0.4rem;
    border: 1px solid var(--border);
    border-radius: 0.6rem;
  }

  /* Red only when the list is not protecting at all; amber when it is
     blocking from an older copy; quiet when all is well. */
  .state.unavailable {
    color: var(--blocked);
  }

  .state.failing,
  .state.stale,
  .state.retired,
  .state.unauthorized,
  .state.suspicious {
    color: var(--audit, #c9962e);
  }

  .state.ok,
  .state.pending,
  .state.disabled {
    color: var(--text-dim);
  }

  .count-link {
    color: inherit;
    text-decoration: none;
  }

  .count-link:hover {
    color: var(--accent);
    text-decoration: underline;
  }

  .quiet {
    color: var(--text-dim);
    font-size: 0.78rem;
    font-style: italic;
    cursor: help;
  }

  .builtin {
    color: var(--text-dim);
    font-size: 0.78rem;
    margin: 0.6rem 0 0;
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
  .catalog {
    margin-top: 1.5rem;
  }

  .catalog h3 {
    margin: 1.1rem 0 0.5rem;
    font-size: 0.92rem;
  }

  .catalog h3 small {
    color: var(--text-dim);
    font-weight: normal;
    font-size: 0.78rem;
    margin-left: 0.5rem;
  }

  .preset-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(16rem, 1fr));
    gap: 0.6rem;
  }

  .preset {
    border: 1px solid var(--border);
    border-radius: 0.4rem;
    padding: 0.6rem 0.75rem;
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }

  .preset-head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: 0.5rem;
  }

  .preset-label {
    font-weight: 600;
    font-size: 0.88rem;
  }

  .preset-size {
    color: var(--text-dim);
    font-size: 0.72rem;
    white-space: nowrap;
  }

  .preset-note {
    color: var(--text-dim);
    font-size: 0.78rem;
    margin: 0;
    flex: 1;
  }

  .preset .row-action {
    align-self: flex-start;
  }

  .preset-added {
    color: var(--allowed);
    font-size: 0.78rem;
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
