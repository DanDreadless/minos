<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type CheckResult, type CustomService, type LocalRecord, type Service } from '../lib/api';
  import CustomServiceForm from '../lib/components/CustomServiceForm.svelte';
  import { copy } from '../lib/copy';
  import { notify, notifyError } from '../lib/toast';

  let allowlist: string[] = [];
  let denylist: string[] = [];
  let catalog: Service[] = [];
  let customs: CustomService[] = [];
  let allowedServices = new Set<string>();
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
      const [al, dl, cfg, svcs] = await Promise.all([
        api.allowlist(),
        api.denylist(),
        api.getConfig(),
        api.services(),
      ]);
      allowlist = al;
      denylist = dl;
      localRecords = cfg.dns.local_records;
      catalog = svcs.catalog;
      customs = svcs.custom;
      allowedServices = new Set(svcs.allowed);
    } catch (e) {
      notifyError(e);
    }
  }

  async function togglePardonService(name: string): Promise<void> {
    const next = new Set(allowedServices);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    try {
      const view = await api.updateServices({ allowed: [...next] });
      allowedServices = new Set(view.allowed);
    } catch (e) {
      notifyError(e);
      await load();
    }
  }

  // Customs carry their global pardon flag on the definition itself.
  async function togglePardonCustom(c: CustomService): Promise<void> {
    try {
      const view = await api.updateCustomService(c.name, { allowed: !c.allowed });
      customs = view.custom;
    } catch (e) {
      notifyError(e);
      await load();
    }
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
        const view = await api.updateCustomService(editingCustom.name, {
          label: d.label,
          domains: d.domains,
          allow_extra: d.allow_extra ?? [],
        });
        customs = view.custom;
        notify(`Custom service "${d.label || editingCustom.name}" updated.`);
      } else {
        // Created from the pardon context → starts allowed.
        const view = await api.addCustomService({
          label: d.label,
          domains: d.domains,
          allow_extra: d.allow_extra ?? [],
          allowed: true,
          ...(d.name ? { name: d.name } : {}),
        });
        customs = view.custom;
        notify(`Custom service "${d.label || d.name}" created and pardoned.`);
      }
      closeCustomForm();
    } catch (err) {
      notifyError(err);
    }
  }

  async function removeCustom(c: CustomService): Promise<void> {
    if (!window.confirm(copy.lists.customConfirmDelete(c.label || c.name))) return;
    try {
      const view = await api.deleteCustomService(c.name);
      customs = view.custom;
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

<section class="card service-pardons">
  <h2>{copy.domains.servicePardonsTitle} <small>{copy.domains.servicePardonsHint}</small></h2>
  <div class="service-grid">
    {#each catalog as svc (svc.name)}
      <label class="service" title={copy.lists.serviceDomains(svc.domains.length)}>
        <input
          type="checkbox"
          checked={allowedServices.has(svc.name)}
          on:change={() => togglePardonService(svc.name)}
        />
        {svc.label}
      </label>
    {/each}
    {#each customs as c (c.name)}
      <label class="service" title={copy.lists.serviceDomains(c.domains.length)}>
        <input type="checkbox" checked={c.allowed} on:change={() => togglePardonCustom(c)} />
        {c.label || c.name}
        <span class="custom-badge" title={copy.lists.customBadgeTitle}>
          {copy.lists.customBadge}
        </span>
      </label>
    {/each}
  </div>
  <details class="custom-manage" bind:open={manageOpen}>
    <summary>
      {copy.lists.customManage}
      {#if customs.length}
        <span class="count">({customs.length})</span>
      {/if}
      <small>{copy.lists.customManagePardonHint}</small>
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
    {copy.domains.servicePardonsNote}
    <a href="#/devices">{copy.domains.servicePardonsNoteLink}</a>
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

  .service-pardons {
    margin-top: 1.25rem;
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

  .custom-badge {
    font-size: 0.65rem;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-dim);
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 0 0.4rem;
    cursor: help;
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
