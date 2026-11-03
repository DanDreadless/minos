<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type CustomService, type Device, type Group, type Rule, type Service } from '../lib/api';
  import CustomServiceForm from '../lib/components/CustomServiceForm.svelte';
  import ServiceShuttle from '../lib/components/ServiceShuttle.svelte';
  import { confirmDialog } from '../lib/confirm';
  import { copy } from '../lib/copy';
  import { currentParams } from '../lib/router';
  import { deviceKey, deviceLabel, findByKey } from '../lib/rules';
  import { notify, notifyError } from '../lib/toast';

  // DNS Filter → Services: block or allow whole services for everyone, a
  // group or one device. Every change goes through the rules API, so a
  // service is either blocked or allowed at a scope, never both, and the
  // most specific scope wins — the same as domain rules.

  let catalog: Service[] = [];
  let customs: CustomService[] = [];
  let rules: Rule[] = [];
  let groups: Group[] = [];
  let devices: Device[] = [];
  // Scope is 'everyone', 'group:<name>' or 'device:<key>'.
  let scope = 'everyone';

  async function load(): Promise<void> {
    try {
      const [svcs, rs, gs, ds] = await Promise.all([api.services(), api.rules(), api.groups(), api.clients()]);
      catalog = svcs.catalog;
      customs = svcs.custom;
      rules = rs;
      groups = gs;
      devices = ds;
    } catch (e) {
      notifyError(e);
    }
  }

  function scopeOf(s: string): Pick<Rule, 'scope' | 'target'> {
    if (s === 'everyone') return { scope: 'everyone' };
    const i = s.indexOf(':');
    return { scope: s.slice(0, i) as Rule['scope'], target: s.slice(i + 1) };
  }

  // The services selected at one scope, split catalog / custom, for the
  // two shuttles.
  function selected(all: Rule[], cs: CustomService[], s: string, action: 'allow' | 'block', custom: boolean): string[] {
    const sc = scopeOf(s);
    const customNames = new Set(cs.map((c) => c.name));
    return all
      .filter(
        (r) =>
          r.kind === 'service' &&
          r.action === action &&
          r.scope === sc.scope &&
          (r.target ?? '').toLowerCase() === (sc.target ?? '').toLowerCase() &&
          customNames.has(r.value) === custom,
      )
      .map((r) => r.value);
  }

  $: blockCatalog = selected(rules, customs, scope, 'block', false);
  $: blockCustom = selected(rules, customs, scope, 'block', true);
  $: allowCatalog = selected(rules, customs, scope, 'allow', false);
  $: allowCustom = selected(rules, customs, scope, 'allow', true);
  $: namedDevices = [...devices].sort((a, b) => deviceLabel(a).localeCompare(deviceLabel(b)));
  $: scopeGroup = scopeGroupOf(scope, groups, devices);

  // The group whose mode makes this scope's choices inert, if any.
  function scopeGroupOf(s: string, gs: Group[], ds: Device[]): Group | undefined {
    const sc = scopeOf(s);
    const name = sc.scope === 'group' ? sc.target : sc.scope === 'device' ? findByKey(ds, sc.target ?? '')?.group : '';
    const g = gs.find((x) => x.name === name);
    return g && g.mode !== 'filter' ? g : undefined;
  }

  async function change(action: 'allow' | 'block', on: boolean, e: CustomEvent<{ name: string }>): Promise<void> {
    const rule: Rule = { action, kind: 'service', value: e.detail.name, ...scopeOf(scope) };
    try {
      if (on) {
        const dev = rule.scope === 'device' ? findByKey(devices, rule.target ?? '') : undefined;
        rules = await api.addRule(rule, dev?.ip);
      } else {
        rules = await api.removeRule(rule);
      }
    } catch (err) {
      notifyError(err);
      await load();
    }
  }

  // --- custom-service management ---

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
        await api.updateCustomService(editingCustom.name, {
          label: d.label,
          domains: d.domains,
          allow_extra: d.allow_extra ?? [],
        });
        notify(`Custom service "${d.label || editingCustom.name}" updated.`);
      } else {
        // Defined but not in force — add it to a list above to use it.
        await api.addCustomService({
          label: d.label,
          domains: d.domains,
          allow_extra: d.allow_extra ?? [],
          ...(d.name ? { name: d.name } : {}),
        });
        notify(`Custom service "${d.label || d.name}" created — add it above to use it.`);
      }
      closeCustomForm();
      await load();
    } catch (err) {
      notifyError(err);
    }
  }

  async function removeCustom(c: CustomService): Promise<void> {
    if (
      !(await confirmDialog({
        message: copy.lists.customConfirmDelete(c.label || c.name),
        confirm: copy.confirm.removeService,
        danger: true,
      }))
    )
      return;
    try {
      await api.deleteCustomService(c.name);
      if (editingCustom?.name === c.name) closeCustomForm();
      await load();
    } catch (e) {
      notifyError(e);
    }
  }

  onMount(() => {
    const p = currentParams();
    if (p.for) scope = p.for;
    void load();
  });
</script>

<div class="intro">
  <p>{copy.blocking.servicesIntro}</p>
</div>

<div class="scope-row">
  <label>
    {copy.domains.servicesScopeLabel}
    <select bind:value={scope}>
      <option value="everyone">{copy.domains.servicesScopeGlobal}</option>
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
  <span class="note">
    {copy.domains.servicesScopeNote}
    <a href="#/devices">{copy.domains.servicesScopeNoteLink}</a>
  </span>
</div>
{#if scopeGroup}
  <p class="inert">{copy.rules.groupBypassNote(scopeGroup.name)}</p>
{/if}

<section class="card">
  <h2>
    {copy.domains.sentencedServicesTitle}
    <small>{copy.blocking.blockServicesHint}</small>
  </h2>
  <ServiceShuttle
    {catalog}
    {customs}
    selectedCatalog={blockCatalog}
    selectedCustom={blockCustom}
    availableLabel={copy.lists.serviceShuttleAvailable}
    activeLabel={copy.domains.sentencedServicesActive}
    emptyText={copy.domains.sentencedServicesEmpty}
    on:add={(e) => change('block', true, e)}
    on:remove={(e) => change('block', false, e)}
  />
</section>

<section class="card">
  <h2>
    {copy.domains.pardonedServicesTitle}
    <small>{copy.blocking.allowServicesHint}</small>
  </h2>
  <ServiceShuttle
    {catalog}
    {customs}
    selectedCatalog={allowCatalog}
    selectedCustom={allowCustom}
    availableLabel={copy.lists.serviceShuttleAvailable}
    activeLabel={copy.domains.pardonedServicesActive}
    emptyText={copy.domains.pardonedServicesEmpty}
    on:add={(e) => change('allow', true, e)}
    on:remove={(e) => change('allow', false, e)}
  />
</section>

<section class="card">
  <details class="custom-manage" bind:open={manageOpen}>
    <summary>
      <h2>
        {copy.lists.customManage}
        {#if customs.length}<span class="count">{customs.length}</span>{/if}
        <small>{copy.lists.customManageHint}</small>
      </h2>
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
            <button class="row-action danger" on:click={() => removeCustom(c)}>{copy.blocking.remove}</button>
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
      <CustomServiceForm editing={editingCustom} showAllowExtra on:save={saveCustom} on:cancel={closeCustomForm} />
    {:else}
      <button class="row-action add-custom" on:click={() => (showCustomForm = true)}>
        + {copy.lists.customAdd}
      </button>
    {/if}
    <p class="note">{copy.lists.customSharedNote}</p>
  </details>
</section>

<style>
  section + section {
    margin-top: 1.1rem;
  }

  .intro p {
    margin: 0 0 1rem;
    color: var(--text-dim);
    font-size: 0.88rem;
    max-width: 52rem;
    line-height: 1.45;
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
    letter-spacing: 0;
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

  .scope-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 1rem;
    margin-bottom: 1rem;
  }

  .scope-row label {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
    color: var(--text-dim);
  }

  .scope-row select {
    max-width: 18rem;
  }

  .scope-row .note {
    margin: 0;
  }

  .inert {
    color: var(--accent);
    font-size: 0.85rem;
    margin: -0.4rem 0 1rem;
  }

  .custom-manage summary {
    cursor: pointer;
    list-style: none;
  }

  .custom-manage summary::-webkit-details-marker {
    display: none;
  }

  .custom-manage summary h2 {
    margin: 0;
  }

  .custom-manage summary h2::before {
    content: '▸';
    color: var(--text-dim);
    font-size: 0.8rem;
    margin-right: 0.2rem;
  }

  .custom-manage[open] summary h2::before {
    content: '▾';
  }

  .custom-list {
    list-style: none;
    margin: 0.9rem 0 0;
    padding: 0;
    max-width: 34rem;
  }

  .custom-list li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem 0.8rem;
    padding: 0.4rem 0;
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

  .row-action {
    padding: 0.1rem 0.6rem;
    font-size: 0.75rem;
  }

  .add-custom {
    margin-top: 0.8rem;
  }

  .editing {
    color: var(--accent);
  }

  .note {
    color: var(--text-dim);
    font-size: 0.78rem;
    margin: 0.8rem 0 0;
  }
</style>
