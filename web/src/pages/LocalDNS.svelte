<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type LocalRecord, type Upstream } from '../lib/api';
  import HelpLink from '../lib/components/HelpLink.svelte';
  import { copy } from '../lib/copy';
  import { notify, notifyError } from '../lib/toast';

  // Everything about names on your own network, in one place: records Minos
  // answers itself, and domains it sends to another resolver (usually the
  // router, so DHCP hostnames resolve).

  let localRecords: LocalRecord[] = [];
  let newLocalName = '';
  let newLocalType = 'A';
  let newLocalValue = '';
  // Inline edit of one record's value. The name is the record's identity
  // (removal keys on it, and duplicates are a validation error), so a
  // rename stays a remove-then-add; what people actually come here to
  // change is the address behind a name that moved.
  let editingLocal: string | null = null;
  let editLocalType = 'A';
  let editLocalValue = '';

  interface RouteRow {
    domains: string;
    address: string;
    protocol: Upstream['protocol'];
  }
  let routeRows: RouteRow[] = [];

  async function load(): Promise<void> {
    try {
      const cfg = await api.getConfig();
      localRecords = cfg.dns.local_records;
      routeRows = cfg.dns.routes.map((r) => ({
        domains: r.domains.join(', '),
        address: r.upstream.address,
        protocol: r.upstream.protocol,
      }));
    } catch (e) {
      notifyError(e);
    }
  }

  function describeRecord(r: LocalRecord): string {
    if (r.cname) return `CNAME → ${r.cname}`;
    const parts: string[] = [];
    if (r.a?.length) parts.push(r.a.join(', '));
    if (r.aaaa?.length) parts.push(r.aaaa.join(', '));
    return parts.join(' · ');
  }

  // The record's type, for the badge and for pre-selecting the edit form.
  function recordType(r: LocalRecord): string {
    if (r.cname) return 'CNAME';
    if (r.a?.length) return 'A';
    if (r.aaaa?.length) return 'AAAA';
    return 'A';
  }

  function startEditLocal(r: LocalRecord): void {
    editingLocal = r.name;
    editLocalType = recordType(r);
    editLocalValue = r.cname ?? (editLocalType === 'AAAA' ? r.aaaa : r.a)?.join(', ') ?? '';
  }

  function cancelEditLocal(): void {
    editingLocal = null;
    editLocalValue = '';
  }

  async function saveEditLocal(name: string): Promise<void> {
    const value = editLocalValue.trim();
    if (!value) return;
    const values = value
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    const updated = localRecords.map((r) => {
      if (r.name !== name) return r;
      // Rebuild from the original so the other address family survives an
      // edit: A and AAAA may coexist, and only CNAME excludes them both.
      const next: LocalRecord = { name: r.name };
      if (editLocalType === 'CNAME') {
        next.cname = values[0];
      } else if (editLocalType === 'AAAA') {
        next.aaaa = values;
        if (r.a?.length) next.a = r.a;
      } else {
        next.a = values;
        if (r.aaaa?.length) next.aaaa = r.aaaa;
      }
      return next;
    });
    await saveLocalRecords(updated);
    if (editingLocal === name) cancelEditLocal();
  }

  async function saveLocalRecords(records: LocalRecord[]): Promise<void> {
    try {
      const cfg = await api.updateConfig({ dns: { local_records: records } });
      localRecords = cfg.dns.local_records;
      notify(copy.settings.saved);
    } catch (e) {
      notifyError(e);
    }
  }

  async function addLocalRecord(): Promise<void> {
    const name = newLocalName.trim();
    const value = newLocalValue.trim();
    if (!name || !value) return;
    const rec: LocalRecord = { name };
    const values = value
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    if (newLocalType === 'CNAME') rec.cname = values[0];
    else if (newLocalType === 'AAAA') rec.aaaa = values;
    else rec.a = values;
    await saveLocalRecords([...localRecords, rec]);
    newLocalName = '';
    newLocalValue = '';
  }

  async function removeLocalRecord(name: string): Promise<void> {
    await saveLocalRecords(localRecords.filter((r) => r.name !== name));
  }

  // A pasted https:// URL can only be DoH; switch the protocol to match
  // rather than let the row save as DoT and fail every exchange.
  function matchProtocol(row: RouteRow): void {
    if (/^https:\/\//i.test(row.address.trim()) && row.protocol !== 'doh') {
      row.protocol = 'doh';
      routeRows = routeRows;
    }
  }

  async function saveRoutes(): Promise<void> {
    try {
      const cfg = await api.updateConfig({
        dns: {
          routes: routeRows.map((r) => ({
            domains: r.domains
              .split(/[\s,]+/)
              .map((s) => s.trim())
              .filter(Boolean),
            upstream: { address: r.address.trim(), protocol: r.protocol },
          })),
        },
      });
      routeRows = cfg.dns.routes.map((r) => ({
        domains: r.domains.join(', '),
        address: r.upstream.address,
        protocol: r.upstream.protocol,
      }));
      notify(copy.settings.saved);
    } catch (e) {
      notifyError(e);
    }
  }

  function addRoute(): void {
    routeRows = [...routeRows, { domains: '', address: '', protocol: 'udp' }];
  }

  function removeRoute(i: number): void {
    routeRows = routeRows.filter((_, idx) => idx !== i);
  }

  onMount(() => void load());
</script>

<h1>{copy.nav.local.label} <small>{copy.nav.local.hint}</small><HelpLink section="localdns" /></h1>

<section class="card">
  <h2>{copy.domains.localTitle} <small>{copy.domains.localHint}</small></h2>
  <form class="local-add" on:submit|preventDefault={addLocalRecord}>
    <input placeholder={copy.domains.localNamePlaceholder} bind:value={newLocalName} required />
    <select bind:value={newLocalType} title="Record type">
      <option value="A">A (IPv4)</option>
      <option value="AAAA">AAAA (IPv6)</option>
      <option value="CNAME">CNAME (alias)</option>
    </select>
    <input
      placeholder={copy.domains.localValuePlaceholder(newLocalType)}
      bind:value={newLocalValue}
      required
    />
    <button type="submit" class="primary" disabled={!newLocalName.trim() || !newLocalValue.trim()}>
      {copy.domains.localAdd}
    </button>
  </form>
  <p class="note">{copy.domains.localWildcardNote}</p>
  {#if localRecords.length === 0}
    <p class="empty">{copy.domains.localEmpty}</p>
  {:else}
    <ul class="local-list">
      {#each localRecords as r (r.name)}
        <li>
          {#if editingLocal === r.name}
            <form class="local-edit" on:submit|preventDefault={() => saveEditLocal(r.name)}>
              <span class="domain">{r.name}</span>
              <select bind:value={editLocalType} title="Record type">
                <option value="A">A</option>
                <option value="AAAA">AAAA</option>
                <option value="CNAME">CNAME</option>
              </select>
              <!-- svelte-ignore a11y-autofocus -->
              <input
                class="edit-value"
                autofocus
                bind:value={editLocalValue}
                placeholder={copy.domains.localValuePlaceholder(editLocalType)}
                aria-label={copy.domains.localEditValueLabel(r.name)}
                required
              />
              <span class="row-actions">
                <button type="submit" class="row-action primary" disabled={!editLocalValue.trim()}>
                  {copy.domains.localSave}
                </button>
                <button type="button" class="row-action" on:click={cancelEditLocal}>
                  {copy.domains.localCancel}
                </button>
              </span>
            </form>
          {:else}
            <span class="domain">{r.name}</span>
            <span class="record-type">{recordType(r)}</span>
            <span class="record-value">{describeRecord(r)}</span>
            <span class="row-actions">
              <button class="row-action" title={copy.domains.localEditTitle} on:click={() => startEditLocal(r)}>
                {copy.domains.localEdit}
              </button>
              <button class="row-action" title="Remove record" on:click={() => removeLocalRecord(r.name)}>
                Remove
              </button>
            </span>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>

<section class="card">
  <h2>{copy.settings.routesTitle} <small>{copy.settings.routesHint}</small></h2>
  {#if routeRows.length === 0}
    <p class="note">{copy.settings.routesEmpty}</p>
  {/if}
  {#each routeRows as r, i (i)}
    <div class="route-row">
      <input
        class="grow"
        placeholder={copy.settings.routesDomainsPlaceholder}
        title={copy.settings.routesDomains}
        bind:value={r.domains}
      />
      <span class="route-arrow" aria-hidden="true">→</span>
      <input
        placeholder={r.protocol === 'doh' ? 'https://resolver/dns-query' : 'host:port'}
        bind:value={r.address}
        on:input={() => matchProtocol(r)}
      />
      <select bind:value={r.protocol} title="Protocol">
        <option value="udp">UDP</option>
        <option value="tcp">TCP</option>
        <option value="dot">DoT</option>
        <option value="doh">DoH</option>
        <option value="doh3">DoH3</option>
        <option value="doq">DoQ</option>
      </select>
      <button class="danger" title="Remove route" on:click={() => removeRoute(i)}>✕</button>
    </div>
  {/each}
  <p class="note">{copy.settings.routesNote}</p>
  <div class="section-actions">
    <button on:click={addRoute}>{copy.settings.routesAdd}</button>
    <button class="primary" on:click={saveRoutes}>{copy.settings.save}</button>
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

  section + section {
    margin-top: 1.25rem;
  }

  form {
    display: flex;
    gap: 0.6rem;
    margin-bottom: 0.9rem;
  }

  .local-add {
    flex-wrap: wrap;
  }

  .local-add input {
    flex: 1;
    max-width: 22rem;
    min-width: 12rem;
  }

  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  /* The rows read left to right as one phrase — name, type, value — with
     the actions on the right, width-capped so a host and its address never
     sit half a screen apart. */
  .local-list {
    max-width: 48rem;
  }

  .local-list li {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-wrap: wrap;
    padding: 0.4rem 0;
    border-bottom: 1px solid var(--border);
    font-size: 0.85rem;
  }

  .local-list li:last-child {
    border-bottom: none;
  }

  .domain {
    font-family: var(--font-mono);
  }

  .local-list .domain {
    min-width: 11rem;
  }

  .record-type {
    color: var(--text-dim);
    font-size: 0.68rem;
    letter-spacing: 0.06em;
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 0.05rem 0.35rem;
    flex: 0 0 auto;
  }

  .record-value {
    color: var(--text-dim);
    font-family: var(--font-mono);
    font-size: 0.8rem;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .row-actions {
    display: flex;
    gap: 0.35rem;
    margin-left: auto;
    flex: 0 0 auto;
  }

  .row-action {
    padding: 0.1rem 0.6rem;
    font-size: 0.75rem;
  }

  .local-edit {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-wrap: wrap;
    width: 100%;
    margin: 0;
  }

  .local-edit .edit-value {
    flex: 1 1 12rem;
    min-width: 8rem;
  }

  .route-row {
    display: flex;
    gap: 0.5rem;
    align-items: center;
    flex-wrap: wrap;
    margin-bottom: 0.5rem;
  }

  .grow {
    flex: 1;
  }

  .route-arrow {
    color: var(--text-dim);
  }

  .section-actions {
    display: flex;
    gap: 0.6rem;
    margin-top: 0.9rem;
  }

  .note {
    color: var(--text-dim);
    font-size: 0.8rem;
    margin: 0 0 0.6rem;
  }

  .empty {
    color: var(--text-dim);
    font-style: italic;
  }
</style>
