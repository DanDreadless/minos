<script lang="ts">
  // Settings → History: every change a person made (here, by script, or with
  // the minos command), what it touched, the change itself with secrets
  // hidden, and a way back.
  import { onMount } from 'svelte';
  import { api, type DiffLine, type HistoryEntry } from '../api';
  import { confirmDialog } from '../confirm';
  import { copy } from '../copy';
  import { docs } from '../docs';
  import { notify, notifyError } from '../toast';

  export let onRestored: () => void;

  const c = copy.history;
  let entries: HistoryEntry[] = [];
  let loaded = false;
  let open: Record<string, DiffLine[] | 'loading'> = {};
  let busy = false;

  // Setting names from the user guide, so the history uses the same words as
  // Help and Settings.
  const labels: Record<string, string> = {};
  for (const sec of docs) {
    for (const st of sec.settings ?? []) {
      labels[st.key] = st.label;
      for (const f of st.fields ?? []) labels[f.key] = st.label;
    }
  }

  // The nearest documented name: a change inside a list entry
  // ("lists.sources") falls back to its parent key's label.
  function labelFor(key: string, names: Record<string, string>): string {
    for (let k = key; k; k = k.includes('.') ? k.slice(0, k.lastIndexOf('.')) : '') {
      if (names[k]) return names[k];
    }
    return key;
  }

  function summary(e: HistoryEntry, names: Record<string, string>): string {
    if (!e.keys.length) return c.baseline;
    return [...new Set(e.keys.map((k) => labelFor(k, names)))].join(', ');
  }

  // DiffOf is the loaded diff for id, or nothing while it loads.
  function diffOf(o: Record<string, DiffLine[] | 'loading'>, id: string): DiffLine[] {
    const v = o[id];
    return Array.isArray(v) ? v : [];
  }

  function when(iso: string): string {
    return new Date(iso).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' });
  }

  async function load(): Promise<void> {
    try {
      entries = await api.settingsHistory();
    } catch (e) {
      notifyError(e);
    } finally {
      loaded = true;
    }
  }

  async function toggle(id: string): Promise<void> {
    if (open[id]) {
      const { [id]: _, ...rest } = open;
      open = rest;
      return;
    }
    open = { ...open, [id]: 'loading' };
    try {
      open = { ...open, [id]: await api.settingsDiff(id) };
    } catch (e) {
      const { [id]: _, ...rest } = open;
      open = rest;
      notifyError(e);
    }
  }

  async function restore(e: HistoryEntry, undo: boolean): Promise<void> {
    const ok = await confirmDialog({
      message: undo ? c.undoConfirm : c.restoreConfirm(when(e.time)),
      confirm: undo ? c.undo : c.restore,
    });
    if (!ok) return;
    busy = true;
    try {
      await api.restoreSettings(e.id);
      notify(undo ? c.undone : c.restored(when(e.time)));
      open = {};
      await load();
      onRestored();
    } catch (err) {
      notifyError(err);
    } finally {
      busy = false;
    }
  }

  onMount(load);
</script>

<section class="card">
  <h2>{c.title} <small>{c.hint}</small></h2>
  <p class="note">{c.note}</p>
  {#if entries.length >= 2}
    <button class="undo" disabled={busy} on:click={() => restore(entries[1], true)}>{c.undo}</button>
  {/if}
  {#if loaded && entries.length === 0}
    <p class="empty">{c.empty}</p>
  {/if}
  <ol class="versions">
    {#each entries as e, i (e.id)}
      <li>
        <div class="head">
          <span class="when">{when(e.time)}</span>
          <span class="who">{e.actor}</span>
          {#if i === 0}<span class="current">{c.current}</span>{/if}
        </div>
        <p class="what">{e.note ? `${e.note}: ` : ''}{summary(e, labels)}</p>
        <div class="actions">
          {#if e.keys.length}
            <button class="link" on:click={() => toggle(e.id)}>{open[e.id] ? c.hideChanges : c.showChanges}</button>
          {/if}
          {#if i > 0}
            <button class="link" disabled={busy} on:click={() => restore(e, false)}>{c.restore}</button>
          {/if}
        </div>
        {#if open[e.id] === 'loading'}
          <p class="note">{c.loading}</p>
        {:else if open[e.id]}
          <pre class="diff">{#each diffOf(open, e.id) as l}<span class={l.op === '+' ? 'add' : l.op === '-' ? 'del' : l.text === '…' ? 'gap' : ''}
                >{l.op === ' ' ? '  ' : l.op + ' '}{l.text}
</span>{/each}</pre>
        {/if}
      </li>
    {/each}
  </ol>
</section>

<style>
  h2 small {
    color: var(--text-dim);
    font-size: 0.75rem;
    margin-left: 0.5rem;
    letter-spacing: 0;
  }

  .note,
  .empty {
    color: var(--text-dim);
    font-size: 0.85rem;
  }

  .undo {
    margin: 0.3rem 0 0.8rem;
  }

  .versions {
    list-style: none;
    padding: 0;
    margin: 0;
  }

  .versions li {
    padding: 0.7rem 0;
    border-top: 1px solid var(--border);
  }

  .head {
    display: flex;
    gap: 0.75rem;
    flex-wrap: wrap;
    align-items: baseline;
  }

  .when {
    font-variant-numeric: tabular-nums;
  }

  .who {
    color: var(--text-dim);
    font-size: 0.85rem;
  }

  .current {
    font-size: 0.72rem;
    color: var(--accent);
    border: 1px solid var(--accent);
    border-radius: 999px;
    padding: 0 0.45rem;
  }

  .what {
    margin: 0.25rem 0 0.2rem;
  }

  .actions {
    display: flex;
    gap: 1rem;
  }

  .link {
    background: none;
    border: none;
    padding: 0;
    color: var(--accent);
    cursor: pointer;
    font-size: 0.85rem;
  }

  .diff {
    margin: 0.5rem 0 0;
    padding: 0.6rem 0.75rem;
    background: var(--bg-sunken);
    border: 1px solid var(--border);
    border-radius: 6px;
    font-size: 0.78rem;
    overflow-x: auto;
    max-height: 26rem;
  }

  .diff span {
    display: block;
    white-space: pre;
  }

  .diff .add {
    color: var(--allowed);
  }

  .diff .del {
    color: var(--blocked);
  }

  .diff .gap {
    color: var(--text-dim);
  }
</style>
