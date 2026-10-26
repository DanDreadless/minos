<script lang="ts">
  import { onMount } from 'svelte';
  import {
    api,
    type CustomService,
    type Group,
    type Service,
    type TemporaryAllow,
  } from '../lib/api';
  import CustomServiceForm from '../lib/components/CustomServiceForm.svelte';
  import ServiceShuttle from '../lib/components/ServiceShuttle.svelte';
  import { checkOpen } from '../lib/check';
  import { confirmDialog } from '../lib/confirm';
  import { copy } from '../lib/copy';
  import { fmtClock } from '../lib/format';
  import { notify, notifyError } from '../lib/toast';

  // Which tab this is: what you always allow, or what you always block.
  // Both share one loaded state, so switching tabs is instant.
  export let mode: 'allow' | 'block';

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
  let temporary: TemporaryAllow[] = [];
  // Now ticks each minute so the time left stays current.
  let now = Date.now();

  async function load(): Promise<void> {
    try {
      const [al, dl, svcs, grps, tmp] = await Promise.all([
        api.allowlist(),
        api.denylist(),
        api.services(),
        api.groups(),
        api.temporaryAllows(),
      ]);
      allowlist = al;
      denylist = dl;
      temporary = tmp;
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
    if (
      !(await confirmDialog({
        message: copy.lists.customConfirmDelete(c.label || c.name),
        confirm: copy.confirm.removeService,
        danger: true,
      }))
    )
      return;
    try {
      applyServicesView(await api.deleteCustomService(c.name));
      if (editingCustom?.name === c.name) closeCustomForm();
    } catch (e) {
      notifyError(e);
    }
  }

  async function addPardon(): Promise<void> {
    try {
      await api.pardon(newPardon.trim());
      notify(copy.allow.done(newPardon.trim()));
      newPardon = '';
      await load();
    } catch (e) {
      notifyError(e);
    }
  }

  async function addSentence(): Promise<void> {
    try {
      await api.sentence(newSentence.trim());
      notify(copy.block.done(newSentence.trim()));
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

  async function endTemporary(domain: string): Promise<void> {
    try {
      await api.removeTemporaryAllow(domain);
      temporary = temporary.filter((t) => t.domain !== domain);
    } catch (e) {
      notifyError(e);
    }
  }

  function timeLeft(until: string, at: number): string {
    const mins = Math.max(0, Math.round((new Date(until).getTime() - at) / 60000));
    if (mins < 60) return `${mins} min`;
    const h = Math.floor(mins / 60);
    return mins % 60 ? `${h} h ${mins % 60} min` : `${h} h`;
  }

  onMount(() => {
    void load();
    const t = setInterval(() => {
      now = Date.now();
      // Drop ones that have run out; the server removes them too.
      temporary = temporary.filter((x) => new Date(x.until).getTime() > now);
    }, 30000);
    return () => clearInterval(t);
  });
</script>

<div class="intro">
  <p>{mode === 'allow' ? copy.blocking.allowIntro : copy.blocking.blockIntro}</p>
  <button class="small" on:click={() => checkOpen.set(true)}>{copy.blocking.checkButton}</button>
</div>

<section class="card">
  <h2>
    {mode === 'allow' ? copy.domains.pardonsTitle : copy.domains.sentencesTitle}
    <span class="count">{(mode === 'allow' ? allowlist : denylist).length}</span>
    <small>{mode === 'allow' ? copy.domains.pardonsHint : copy.domains.sentencesHint}</small>
  </h2>
  {#if mode === 'allow'}
    <form on:submit|preventDefault={addPardon}>
      <input placeholder={copy.domains.addPlaceholder} bind:value={newPardon} required />
      <button type="submit" class="primary" disabled={!newPardon.trim()}>{copy.blocking.add}</button>
    </form>
  {:else}
    <form on:submit|preventDefault={addSentence}>
      <input placeholder={copy.domains.addPlaceholder} bind:value={newSentence} required />
      <button type="submit" class="primary" disabled={!newSentence.trim()}>{copy.blocking.add}</button>
    </form>
  {/if}
  {#if (mode === 'allow' ? allowlist : denylist).length === 0}
    <p class="empty">{mode === 'allow' ? copy.domains.pardonsEmpty : copy.domains.sentencesEmpty}</p>
  {:else}
    <ul class="chips" class:block={mode === 'block'}>
      {#each mode === 'allow' ? allowlist : denylist as d (d)}
        <li>
          <span class="domain">{d}</span>
          <button
            class="chip-x"
            title={copy.blocking.removeDomain(d)}
            aria-label={copy.blocking.removeDomain(d)}
            on:click={() => (mode === 'allow' ? removePardon(d) : removeSentence(d))}>×</button
          >
        </li>
      {/each}
    </ul>
  {/if}
</section>

{#if mode === 'allow' && temporary.length}
  <section class="card">
    <h2>
      {copy.allow.temporaryTitle}
      <span class="count">{temporary.length}</span>
      <small>{copy.allow.temporaryHint}</small>
    </h2>
    <ul class="chips">
      {#each temporary as t (t.domain)}
        <li>
          <span class="domain">{t.domain}</span>
          <span class="until">{copy.allow.until(fmtClock(t.until), timeLeft(t.until, now))}</span>
          <button
            class="chip-x"
            title={copy.allow.endNow(t.domain)}
            aria-label={copy.allow.endNow(t.domain)}
            on:click={() => endTemporary(t.domain)}>×</button
          >
        </li>
      {/each}
    </ul>
  </section>
{/if}

<section class="card">
  <h2>
    {mode === 'allow' ? copy.domains.pardonedServicesTitle : copy.domains.sentencedServicesTitle}
    <small>{mode === 'allow' ? copy.blocking.allowServicesHint : copy.blocking.blockServicesHint}</small>
  </h2>

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
    <span class="note">
      {copy.domains.servicesScopeNote}
      <a href="#/devices">{copy.domains.servicesScopeNoteLink}</a>
    </span>
  </div>

  {#if mode === 'allow'}
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
  {:else}
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
  {/if}
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

  form {
    display: flex;
    gap: 0.6rem;
    margin-bottom: 1rem;
    max-width: 30rem;
  }

  form input {
    flex: 1;
    min-width: 0;
  }

  /* Domains as removable chips: a list of dozens stays scannable, and each
     one is a single tap to remove. */
  .chips {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-wrap: wrap;
    gap: 0.45rem;
  }

  .chips li {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    padding: 0.2rem 0.3rem 0.2rem 0.7rem;
    border: 1px solid color-mix(in srgb, var(--allowed) 45%, var(--border));
    border-radius: 999px;
    background: var(--bg-sunken, transparent);
    font-size: 0.85rem;
    max-width: 100%;
  }

  .chips.block li {
    border-color: color-mix(in srgb, var(--blocked) 45%, var(--border));
  }

  .domain {
    font-family: var(--font-mono);
    overflow-wrap: anywhere;
  }

  .until {
    color: var(--text-dim);
    font-size: 0.78rem;
  }

  .chip-x {
    all: unset;
    cursor: pointer;
    width: 1.4rem;
    height: 1.4rem;
    border-radius: 50%;
    text-align: center;
    line-height: 1.4rem;
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

  .scope-row .note {
    margin: 0;
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
