<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import type { CustomService, Service } from '../api';
  import { copy } from '../copy';

  // A dual-pane "shuttle": every known service (catalog + the user's customs)
  // sits in one of two lists — Available on the left, Active on the right.
  // Clicking a row moves it across (add → active, remove → available). The
  // view IS the policy; the component is scope-agnostic and just emits the
  // same add/remove contract the parent wires to global or per-group APIs.
  export let catalog: Service[] = [];
  export let customs: CustomService[] = [];
  // Names currently ON, split the way the API splits catalog vs custom.
  export let selectedCatalog: string[] = [];
  export let selectedCustom: string[] = [];
  export let availableLabel = '';
  export let activeLabel = '';
  export let emptyText = '';

  const dispatch = createEventDispatcher<{
    add: { name: string; custom: boolean };
    remove: { name: string; custom: boolean };
  }>();

  $: selCat = new Set(selectedCatalog);
  $: selCus = new Set(selectedCustom);
  $: entries = [
    ...catalog.map((s) => ({
      name: s.name,
      label: s.label,
      domains: s.domains.length,
      custom: false,
      on: selCat.has(s.name),
    })),
    ...customs.map((c) => ({
      name: c.name,
      label: c.label || c.name,
      domains: c.domains.length,
      custom: true,
      on: selCus.has(c.name),
    })),
  ];
  $: available = entries.filter((e) => !e.on).sort((a, b) => a.label.localeCompare(b.label));
  $: active = entries.filter((e) => e.on).sort((a, b) => a.label.localeCompare(b.label));
</script>

<div class="shuttle">
  <div class="pane">
    <div class="pane-head">
      <span>{availableLabel}</span>
      <span class="pane-count">{available.length}</span>
    </div>
    {#if available.length === 0}
      <p class="pane-empty">{copy.lists.serviceShuttleAllAdded}</p>
    {:else}
      <ul>
        {#each available as e (e.name + (e.custom ? ':c' : ''))}
          <li>
            <button
              class="row"
              title={copy.lists.serviceShuttleMoveIn(e.label)}
              on:click={() => dispatch('add', { name: e.name, custom: e.custom })}
            >
              <span class="row-label">
                {e.label}
                {#if e.custom}
                  <span class="custom-badge" title={copy.lists.customBadgeTitle}
                    >{copy.lists.customBadge}</span
                  >
                {/if}
              </span>
              <span class="arrow" aria-hidden="true">▸</span>
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <div class="pane active">
    <div class="pane-head">
      <span>{activeLabel}</span>
      <span class="pane-count">{active.length}</span>
    </div>
    {#if active.length === 0}
      <p class="pane-empty">{emptyText}</p>
    {:else}
      <ul>
        {#each active as e (e.name + (e.custom ? ':c' : ''))}
          <li>
            <button
              class="row remove"
              title={copy.lists.serviceShuttleMoveOut(e.label)}
              on:click={() => dispatch('remove', { name: e.name, custom: e.custom })}
            >
              <span class="arrow" aria-hidden="true">◂</span>
              <span class="row-label">
                {e.label}
                {#if e.custom}
                  <span class="custom-badge" title={copy.lists.customBadgeTitle}
                    >{copy.lists.customBadge}</span
                  >
                {/if}
              </span>
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>

<style>
  /* Two panes side by side; they wrap to a stack when the shuttle is narrow
     (e.g. two shuttles sharing a row on a small screen). */
  .shuttle {
    display: flex;
    flex-wrap: wrap;
    gap: 0.6rem;
    align-items: stretch;
  }

  .pane {
    flex: 1 1 11rem;
    min-width: 0;
    display: flex;
    flex-direction: column;
    background: var(--bg-sunken);
    border: 1px solid var(--border);
    border-radius: 6px;
    overflow: hidden;
  }

  .pane-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.4rem 0.65rem;
    border-bottom: 1px solid var(--border);
    font-family: var(--font-display);
    font-size: 0.75rem;
    letter-spacing: 0.04em;
    color: var(--text-dim);
  }

  .pane.active .pane-head {
    color: var(--accent);
  }

  .pane-count {
    font-family: var(--font-mono);
    font-size: 0.72rem;
    color: var(--text-dim);
  }

  ul {
    list-style: none;
    margin: 0;
    padding: 0.25rem;
    overflow-y: auto;
    max-height: 15rem;
  }

  .row {
    all: unset;
    box-sizing: border-box;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.4rem;
    width: 100%;
    padding: 0.28rem 0.5rem;
    border-radius: 4px;
    font-size: 0.84rem;
  }

  .row.remove {
    justify-content: flex-start;
  }

  .row:hover {
    background: var(--bg-hover);
  }

  .row:focus-visible {
    outline: 1px solid var(--accent);
  }

  .row-label {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .arrow {
    flex: none;
    color: var(--text-dim);
    font-size: 0.9rem;
  }

  .row:hover .arrow {
    color: var(--accent);
  }

  .custom-badge {
    flex: none;
    font-size: 0.6rem;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-dim);
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 0 0.3rem;
    cursor: help;
  }

  .pane-empty {
    margin: 0;
    padding: 0.5rem 0.65rem;
    color: var(--text-dim);
    font-style: italic;
    font-size: 0.8rem;
  }
</style>
