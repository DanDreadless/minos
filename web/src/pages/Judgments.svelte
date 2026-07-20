<script lang="ts">
  import { onMount } from 'svelte';
  import {
    api,
    type CheckResult,
    type CustomService,
    type Group,
    type LocalRecord,
    type Service,
  } from '../lib/api';
  import CustomServiceForm from '../lib/components/CustomServiceForm.svelte';
  import ServiceShuttle from '../lib/components/ServiceShuttle.svelte';
  import { copy } from '../lib/copy';
  import { notify, notifyError } from '../lib/toast';

  let allowlist: string[] = [];
  let denylist: string[] = [];
  let catalog: Service[] = [];
  let customs: CustomService[] = [];
  let allowedServices = new Set<string>();
  let blockedServices = new Set<string>();
  let groups: Group[] = [];
  // '' = global policy; otherwise the name of the group being edited.
  let scope = '';
  let newPardon = '';
  let newSentence = '';
  let checkDomain = '';
  let checkResult: CheckResult | null = null;
  let checkError = '';
  let localRecords: LocalRecord[] = [];
  let newLocalName = '';
  let newLocalType = 'A';
  let newLocalValue = '';

  async function load(): Promise<void> {
    try {
      const [al, dl, cfg, svcs, grps] = await Promise.all([
        api.allowlist(),
        api.denylist(),
        api.getConfig(),
        api.services(),
        api.groups(),
      ]);
      allowlist = al;
      denylist = dl;
      localRecords = cfg.dns.local_records;
      applyServicesView(svcs);
      groups = grps;
    } catch (e) {
      notifyError(e);
    }
  }

  function applyServicesView(view: {
    catalog: Service[];
    blocked: string[];
    allowed: string[];
    custom: CustomService[];
  }): void {
    catalog = view.catalog;
    customs = view.custom;
    blockedServices = new Set(view.blocked);
    allowedServices = new Set(view.allowed);
  }

  // The group currently in scope, or null for global. The four selected-name
  // arrays feed the two shuttles; the view IS the policy at the chosen scope.
  $: activeGroup = scope ? (groups.find((g) => g.name === scope) ?? null) : null;
  $: blockCatalog = scope ? (activeGroup?.services ?? []) : [...blockedServices];
  $: blockCustom = scope
    ? (activeGroup?.custom_services ?? [])
    : customs.filter((c) => c.blocked).map((c) => c.name);
  $: allowCatalog = scope ? (activeGroup?.allowed_services ?? []) : [...allowedServices];
  $: allowCustom = scope
    ? (activeGroup?.allowed_custom_services ?? [])
    : customs.filter((c) => c.allowed).map((c) => c.name);

  type Side = 'block' | 'allow';

  // A shuttle move: route to the global services API (catalog names) or a
  // custom's own flag, or — when a group is in scope — that group's fields.
  async function changeService(
    side: Side,
    on: boolean,
    e: CustomEvent<{ name: string; custom: boolean }>,
  ): Promise<void> {
    const { name, custom } = e.detail;
    try {
      if (scope) {
        await changeGroupService(scope, side, custom, on, name);
      } else if (custom) {
        applyServicesView(
          await api.updateCustomService(name, side === 'block' ? { blocked: on } : { allowed: on }),
        );
      } else if (side === 'block') {
        const next = on
          ? [...blockedServices, name]
          : [...blockedServices].filter((n) => n !== name);
        applyServicesView(await api.updateServices({ blocked: next }));
      } else {
        const next = on
          ? [...allowedServices, name]
          : [...allowedServices].filter((n) => n !== name);
        applyServicesView(await api.updateServices({ allowed: next }));
      }
    } catch (err) {
      notifyError(err);
      await load();
    }
  }

  // Custom names ride separate group fields end to end — a custom must never
  // enter the catalog-validated services keys.
  async function changeGroupService(
    groupName: string,
    side: Side,
    custom: boolean,
    on: boolean,
    name: string,
  ): Promise<void> {
    const g = groups.find((x) => x.name === groupName);
    if (!g) return;
    const pick = (arr: string[] | null): string[] => {
      const cur = new Set(arr ?? []);
      if (on) cur.add(name);
      else cur.delete(name);
      return [...cur];
    };
    const upd: Parameters<typeof api.updateGroup>[1] =
      side === 'block'
        ? custom
          ? { custom_services: pick(g.custom_services) }
          : { services: pick(g.services) }
        : custom
          ? { allowed_custom_services: pick(g.allowed_custom_services) }
          : { allowed_services: pick(g.allowed_services) };
    groups = await api.updateGroup(groupName, upd);
  }

  // --- custom-service management (pardon context: creations start allowed,
  // and the allow-extra hosts are editable here) ---

  let editingCustom: CustomService | null = null;
  let manageOpen = false;
  let showCustomForm = false;

  function startEditCustom(c: CustomService): void {
    editingCustom = c;
    showCustomForm = true;
  }

  function closeCustomForm(): void {
    editingCustom = null;
    showCustomForm = false;
  }

  async function saveCustom(
    e: CustomEvent<{ label: string; name: string; domains: string[]; allow_extra?: string[] }>,
  ): Promise<void> {
    const d = e.detail;
    try {
      if (editingCustom) {
        applyServicesView(
          await api.updateCustomService(editingCustom.name, {
            label: d.label,
            domains: d.domains,
            allow_extra: d.allow_extra ?? [],
          }),
        );
        notify(`Custom service "${d.label || editingCustom.name}" updated.`);
      } else {
        // Defined but inactive — add it to a shuttle to sentence or pardon it.
        applyServicesView(
          await api.addCustomService({
            label: d.label,
            domains: d.domains,
            allow_extra: d.allow_extra ?? [],
            ...(d.name ? { name: d.name } : {}),
          }),
        );
        notify(`Custom service "${d.label || d.name}" created — add it above to activate.`);
      }
      closeCustomForm();
    } catch (err) {
      notifyError(err);
    }
  }

  async function removeCustom(c: CustomService): Promise<void> {
    if (!window.confirm(copy.lists.customConfirmDelete(c.label || c.name))) return;
    try {
      applyServicesView(await api.deleteCustomService(c.name));
      if (editingCustom?.name === c.name) closeCustomForm();
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

  async function check(): Promise<void> {
    checkResult = null;
    checkError = '';
    try {
      checkResult = await api.check(checkDomain.trim());
    } catch (e) {
      checkError = e instanceof Error ? e.message : String(e);
    }
  }

  function describeCheck(r: CheckResult): string {
    if (r.verdict === 'blocked') return copy.domains.checkBlocked(r.list, r.rule);
    if (r.rule) return copy.domains.checkAllowedByRule(r.rule);
    return copy.domains.checkAllowed;
  }

  async function addPardon(): Promise<void> {
    try {
      await api.pardon(newPardon.trim());
      notify(copy.pardon.done(newPardon.trim()));
      newPardon = '';
      await load();
    } catch (e) {
      notifyError(e);
    }
  }

  async function addSentence(): Promise<void> {
    try {
      await api.sentence(newSentence.trim());
      notify(copy.sentence.done(newSentence.trim()));
      newSentence = '';
      await load();
    } catch (e) {
      notifyError(e);
    }
  }

  async function removePardon(d: string): Promise<void> {
    try {
      await api.unpardon(d);
      await load();
    } catch (e) {
      notifyError(e);
    }
  }

  async function removeSentence(d: string): Promise<void> {
    try {
      await api.unsentence(d);
      await load();
    } catch (e) {
      notifyError(e);
    }
  }

  onMount(() => void load());
</script>

<h1>{copy.nav.domains.label} <small>{copy.nav.domains.hint}</small></h1>

<section class="card check">
  <h2>{copy.domains.checkTitle} <small>{copy.domains.checkHint}</small></h2>
  <form on:submit|preventDefault={check}>
    <input placeholder={copy.domains.checkPlaceholder} bind:value={checkDomain} required />
    <button type="submit" class="primary" disabled={!checkDomain.trim()}>
      {copy.domains.checkButton}
    </button>
  </form>
  {#if checkError}
    <p class="check-result error">{checkError}</p>
  {:else if checkResult}
    <p class="check-result" class:blocked={checkResult.verdict === 'blocked'}>
      <span class="domain">{checkResult.domain}</span> — {describeCheck(checkResult)}
    </p>
  {/if}
</section>

<section class="card services">
  <h2>{copy.domains.servicesTitle} <small>{copy.domains.servicesHint}</small></h2>

  <div class="scope-row">
    <label>
      {copy.domains.servicesScopeLabel}
      <select bind:value={scope}>
        <option value="">{copy.domains.servicesScopeGlobal}</option>
        {#each groups as g (g.name)}
          <option value={g.name}>{g.name} ({g.mode})</option>
        {/each}
      </select>
    </label>
  </div>

  <div class="service-grid">
    <div class="service-col">
      <h3>
        {copy.domains.sentencedServicesTitle}
        <small>{copy.domains.sentencedServicesHint}</small>
      </h3>
      <ServiceShuttle
        {catalog}
        {customs}
        selectedCatalog={blockCatalog}
        selectedCustom={blockCustom}
        availableLabel={copy.lists.serviceShuttleAvailable}
        activeLabel={copy.domains.sentencedServicesActive}
        emptyText={copy.domains.sentencedServicesEmpty}
        on:add={(e) => changeService('block', true, e)}
        on:remove={(e) => changeService('block', false, e)}
      />
    </div>
    <div class="service-col">
      <h3>
        {copy.domains.pardonedServicesTitle}
        <small>{copy.domains.pardonedServicesHint}</small>
      </h3>
      <ServiceShuttle
        {catalog}
        {customs}
        selectedCatalog={allowCatalog}
        selectedCustom={allowCustom}
        availableLabel={copy.lists.serviceShuttleAvailable}
        activeLabel={copy.domains.pardonedServicesActive}
        emptyText={copy.domains.pardonedServicesEmpty}
        on:add={(e) => changeService('allow', true, e)}
        on:remove={(e) => changeService('allow', false, e)}
      />
    </div>
  </div>

  <details class="custom-manage" bind:open={manageOpen}>
    <summary>
      {copy.lists.customManage}
      {#if customs.length}
        <span class="count">({customs.length})</span>
      {/if}
      <small>{copy.lists.customManageHint}</small>
    </summary>
    {#if customs.length}
      <ul class="custom-list">
        {#each customs as c (c.name)}
          <li>
            <span class="custom-name">{c.label || c.name}</span>
            <span class="custom-domains" title={c.domains.join(', ')}>
              {copy.lists.serviceDomains(c.domains.length)}
            </span>
            <button class="row-action" on:click={() => startEditCustom(c)}>
              {copy.lists.customEdit}
            </button>
            <button class="row-action danger" on:click={() => removeCustom(c)}>Remove</button>
          </li>
        {/each}
      </ul>
    {/if}
    {#if showCustomForm}
      {#if editingCustom}
        <p class="note editing">
          {copy.lists.customEditing(editingCustom.label || editingCustom.name)}
        </p>
      {/if}
      <CustomServiceForm
        editing={editingCustom}
        showAllowExtra
        on:save={saveCustom}
        on:cancel={closeCustomForm}
      />
    {:else}
      <button class="row-action add-custom" on:click={() => (showCustomForm = true)}>
        + {copy.lists.customAdd}
      </button>
    {/if}
    <p class="note">{copy.lists.customSharedNote}</p>
  </details>
  <p class="note">
    {copy.domains.servicesScopeNote}
    <a href="#/devices">{copy.domains.servicesScopeNoteLink}</a>
  </p>
</section>

<section class="columns">
  <div class="card">
    <h2>{copy.domains.pardonsTitle} <small>{copy.domains.pardonsHint}</small></h2>
    <form on:submit|preventDefault={addPardon}>
      <input placeholder={copy.domains.addPlaceholder} bind:value={newPardon} required />
      <button type="submit" class="primary" disabled={!newPardon.trim()}>Add</button>
    </form>
    {#if allowlist.length === 0}
      <p class="empty">{copy.domains.pardonsEmpty}</p>
    {:else}
      <ul>
        {#each allowlist as d (d)}
          <li>
            <span class="domain">{d}</span>
            <button class="row-action" title="remove from allowlist" on:click={() => removePardon(d)}>
              Remove
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <div class="card">
    <h2>{copy.domains.sentencesTitle} <small>{copy.domains.sentencesHint}</small></h2>
    <form on:submit|preventDefault={addSentence}>
      <input placeholder={copy.domains.addPlaceholder} bind:value={newSentence} required />
      <button type="submit" class="primary" disabled={!newSentence.trim()}>Add</button>
    </form>
    {#if denylist.length === 0}
      <p class="empty">{copy.domains.sentencesEmpty}</p>
    {:else}
      <ul>
        {#each denylist as d (d)}
          <li>
            <span class="domain">{d}</span>
            <button class="row-action" title="remove from denylist" on:click={() => removeSentence(d)}>
              Remove
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</section>

<section class="card local">
  <h2>{copy.domains.localTitle} <small>{copy.domains.localHint}</small></h2>
  <form class="local-add" on:submit|preventDefault={addLocalRecord}>
    <input placeholder={copy.domains.localNamePlaceholder} bind:value={newLocalName} required />
    <select bind:value={newLocalType} title="record type">
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
    <ul>
      {#each localRecords as r (r.name)}
        <li>
          <span class="domain">{r.name}</span>
          <span class="record-value">{describeRecord(r)}</span>
          <button class="row-action" title="remove record" on:click={() => removeLocalRecord(r.name)}>
            Remove
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  h1 small,
  h2 small {
    color: var(--text-dim);
    font-size: 0.78rem;
    margin-left: 0.5rem;
    letter-spacing: 0;
  }

  form {
    display: flex;
    gap: 0.6rem;
    margin-bottom: 0.9rem;
  }

  form input {
    flex: 1;
    max-width: 22rem;
  }

  .check-result {
    margin: 0.75rem 0 0;
  }

  .check-result.blocked {
    color: var(--blocked);
  }

  .check-result.error {
    color: var(--blocked);
  }

  .domain {
    font-family: var(--font-mono);
  }

  .columns {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(20rem, 1fr));
    gap: 1.25rem;
    margin-top: 1.25rem;
  }

  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.35rem 0;
    border-bottom: 1px solid var(--border);
    font-size: 0.85rem;
  }

  li:last-child {
    border-bottom: none;
  }

  .empty {
    color: var(--text-dim);
    font-style: italic;
  }

  .row-action {
    padding: 0.1rem 0.6rem;
    font-size: 0.75rem;
  }

  .local {
    margin-top: 1.25rem;
  }

  .services {
    margin-top: 1.25rem;
  }

  .scope-row {
    margin-bottom: 1.1rem;
  }

  .scope-row label {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
    color: var(--text-dim);
  }

  .service-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(19rem, 1fr));
    gap: 1.25rem;
  }

  .service-col h3 {
    margin: 0 0 0.6rem;
    font-family: var(--font-display);
    font-weight: normal;
    font-size: 0.98rem;
    letter-spacing: 0.02em;
  }

  .service-col h3 small {
    margin-left: 0.4rem;
    font-size: 0.75rem;
    color: var(--text-dim);
    letter-spacing: 0;
  }

  .custom-manage {
    margin-top: 1rem;
    border-top: 1px solid var(--border);
    padding-top: 0.8rem;
  }

  .custom-manage summary {
    cursor: pointer;
    color: var(--text-dim);
    font-size: 0.82rem;
  }

  .custom-manage summary .count {
    color: var(--accent);
  }

  .custom-manage summary small {
    margin-left: 0.5rem;
    font-size: 0.75rem;
  }

  .custom-list {
    list-style: none;
    margin: 0.7rem 0 0;
    padding: 0;
    max-width: 34rem;
  }

  .custom-list li {
    display: flex;
    align-items: center;
    gap: 0.8rem;
    padding: 0.35rem 0;
    border-bottom: 1px solid var(--border);
    font-size: 0.85rem;
  }

  .custom-list li:last-child {
    border-bottom: none;
  }

  .custom-name {
    font-weight: 600;
  }

  .custom-domains {
    color: var(--text-dim);
    font-size: 0.78rem;
    flex: 1;
    cursor: help;
  }

  .add-custom {
    margin-top: 0.7rem;
  }

  .editing {
    color: var(--accent);
  }

  .local-add {
    flex-wrap: wrap;
  }

  .local-add input {
    min-width: 12rem;
  }

  .record-value {
    color: var(--text-dim);
    font-family: var(--font-mono);
    font-size: 0.8rem;
    flex: 1;
    text-align: right;
    margin-right: 0.8rem;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .note {
    color: var(--text-dim);
    font-size: 0.78rem;
    margin: 0 0 0.6rem;
  }
</style>
