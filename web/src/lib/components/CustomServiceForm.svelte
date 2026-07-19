<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import type { CustomService } from '../api';
  import { copy } from '../copy';

  // The definition being edited, or null to create a new one.
  export let editing: CustomService | null = null;
  // Pardon contexts also edit the allow-extra hosts; block contexts hide
  // them (the PUT is partial, so a hidden field is never clobbered).
  export let showAllowExtra = false;

  const dispatch = createEventDispatcher<{
    save: { label: string; name: string; domains: string[]; allow_extra?: string[] };
    cancel: undefined;
  }>();

  let label = '';
  let name = '';
  let domains = '';
  let allowExtra = '';

  // Refill whenever a different definition is picked for editing.
  let filledFor: string | null = null;
  $: if ((editing?.name ?? null) !== filledFor) {
    filledFor = editing?.name ?? null;
    label = editing ? editing.label || editing.name : '';
    name = editing?.name ?? '';
    domains = (editing?.domains ?? []).join('\n');
    allowExtra = (editing?.allow_extra ?? []).join('\n');
  }

  function toDomains(text: string): string[] {
    return text
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
  }

  function submit(): void {
    const out: { label: string; name: string; domains: string[]; allow_extra?: string[] } = {
      label: label.trim(),
      name: name.trim(),
      domains: toDomains(domains),
    };
    if (showAllowExtra) out.allow_extra = toDomains(allowExtra);
    dispatch('save', out);
  }
</script>

<form class="custom-form" on:submit|preventDefault={submit}>
  <div class="row">
    <label>
      <span>{copy.lists.customLabelLabel}</span>
      <input placeholder={copy.lists.customLabelPlaceholder} bind:value={label} required />
    </label>
    {#if !editing}
      <label>
        <span>{copy.lists.customNameLabel}</span>
        <input placeholder={copy.lists.customNamePlaceholder} bind:value={name} />
      </label>
    {/if}
  </div>
  <label>
    <span>{copy.lists.customDomainsLabel}</span>
    <textarea rows="3" placeholder={copy.lists.customDomainsPlaceholder} bind:value={domains} required
    ></textarea>
  </label>
  {#if showAllowExtra}
    <label>
      <span>{copy.lists.customAllowExtraLabel}</span>
      <textarea rows="2" placeholder={copy.lists.customAllowExtraPlaceholder} bind:value={allowExtra}
      ></textarea>
    </label>
  {/if}
  <div class="row actions">
    <button type="submit" class="primary" disabled={!label.trim() || !domains.trim()}>
      {editing ? copy.lists.customSave : copy.lists.customCreate}
    </button>
    <button type="button" on:click={() => dispatch('cancel')}>{copy.lists.customCancel}</button>
  </div>
</form>

<style>
  .custom-form {
    display: flex;
    flex-direction: column;
    gap: 0.7rem;
    max-width: 34rem;
    margin-top: 0.7rem;
    padding: 0.9rem 1rem;
    background: var(--bg-sunken);
    border: 1px solid var(--border);
    border-radius: 6px;
  }

  .row {
    display: flex;
    gap: 0.7rem;
    flex-wrap: wrap;
  }

  .row label {
    flex: 1;
    min-width: 13rem;
  }

  label {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    font-size: 0.78rem;
    color: var(--text-dim);
  }

  textarea {
    resize: vertical;
    font-size: 0.85rem;
  }

  .actions {
    flex-direction: row;
    align-items: center;
  }
</style>
