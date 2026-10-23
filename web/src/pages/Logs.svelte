<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, openLogStream, type AppLogRecord } from '../lib/api';
  import HelpLink from '../lib/components/HelpLink.svelte';
  import { copy } from '../lib/copy';
  import { fmtLogTimeFull } from '../lib/format';
  import { bootOf, markLogsSeen } from '../lib/logseen';
  import { notify, notifyError } from '../lib/toast';

  type Level = AppLogRecord['level'];
  const RANK: Record<Level, number> = { debug: 0, info: 1, warn: 2, error: 3 };
  const MAX = 2000; // the server's ring size

  let records: AppLogRecord[] = []; // newest first
  let held: AppLogRecord[] = []; // arrivals while paused
  let paused = false;
  let show: Level = 'info';
  let search = '';
  let recording: Level = 'info'; // the server's level
  let loaded = false;
  let connected = false;
  let boot = 0;
  let ws: WebSocket | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let destroyed = false;

  $: visible = filterRecords(records, show, search);

  function filterRecords(rs: AppLogRecord[], min: Level, q: string): AppLogRecord[] {
    const needle = q.trim().toLowerCase();
    return rs.filter((r) => {
      if (RANK[r.level] < RANK[min]) return false;
      if (!needle) return true;
      if (r.message.toLowerCase().includes(needle)) return true;
      return (r.attrs ?? []).some((a) => `${a.key}=${a.value}`.toLowerCase().includes(needle));
    });
  }

  function lineText(r: AppLogRecord): string {
    const attrs = (r.attrs ?? [])
      .map((a) => `${a.key}=${/[\s"=]/.test(a.value) || a.value === '' ? JSON.stringify(a.value) : a.value}`)
      .join(' ');
    return `${r.time} ${r.level.toUpperCase()} ${r.message}${attrs ? ' ' + attrs : ''}`;
  }

  function seen(r: AppLogRecord): void {
    if ((r.level === 'warn' || r.level === 'error') && boot) markLogsSeen(r.seq, boot);
  }

  function connect(): void {
    if (destroyed) return;
    ws = openLogStream(
      (r) => {
        connected = true;
        if (paused) {
          held = [r, ...held].slice(0, MAX);
          return;
        }
        records = [r, ...records].slice(0, MAX);
        seen(r);
      },
      () => {
        ws = null;
        connected = false;
        if (!destroyed) reconnectTimer = setTimeout(reload, 2000);
      },
    );
    ws.onopen = () => (connected = true);
  }

  // reload fetches what the server holds, then streams what follows. Used on
  // open and after a dropped stream (a restart begins a new log).
  async function reload(): Promise<void> {
    try {
      const [logs, status] = await Promise.all([api.appLogs(), api.status()]);
      boot = bootOf(status.uptime_seconds);
      records = logs.records.slice().reverse();
      recording = logs.level;
      if (logs.alert_seq) markLogsSeen(logs.alert_seq, boot);
      loaded = true;
    } catch (e) {
      notifyError(e);
    }
    connect();
  }

  function togglePause(): void {
    paused = !paused;
    if (!paused && held.length) {
      records = [...held, ...records].slice(0, MAX);
      held.forEach(seen);
      held = [];
    }
  }

  async function setRecording(level: Level): Promise<void> {
    try {
      await api.updateConfig({ log_level: level });
      recording = level;
      notify(copy.settings.saved);
    } catch (e) {
      notifyError(e);
    }
  }

  async function download(): Promise<void> {
    try {
      const { blob, filename } = await api.appLogsDownload();
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 10_000);
    } catch (e) {
      notifyError(e);
    }
  }

  async function copyLine(r: AppLogRecord): Promise<void> {
    try {
      await navigator.clipboard.writeText(lineText(r));
      notify(copy.logs.copied);
    } catch (e) {
      notifyError(e);
    }
  }

  onMount(reload);

  onDestroy(() => {
    destroyed = true;
    if (reconnectTimer) clearTimeout(reconnectTimer);
    ws?.close();
  });
</script>

<h1>{copy.logs.title} <small>{copy.logs.subtitle}</small><HelpLink section="logs" /></h1>

<div class="toolbar">
  <input
    type="search"
    placeholder={copy.logs.searchPlaceholder}
    bind:value={search}
    aria-label={copy.logs.searchPlaceholder}
  />
  <label class="inline">
    {copy.logs.showLabel}
    <select bind:value={show}>
      {#each copy.logs.show as s (s.level)}
        <option value={s.level}>{s.label}</option>
      {/each}
    </select>
  </label>
  <label class="inline" title={copy.logs.recordHint}>
    {copy.logs.recordLabel}
    <select value={recording} on:change={(e) => setRecording(e.currentTarget.value as Level)}>
      {#each ['debug', 'info', 'warn', 'error'] as l (l)}
        <option value={l}>{copy.logs.recordLevels[l]}</option>
      {/each}
    </select>
  </label>
</div>

<div class="status">
  {#if paused}
    <span class="mode paused">{copy.logs.paused(held.length)}</span>
  {:else if connected}
    <span class="mode live">{copy.logs.live}</span>
  {:else}
    <span>{copy.logs.reconnecting}</span>
  {/if}
  <span>{copy.logs.count(visible.length, records.length)}</span>
  <span class="spacer"></span>
  <button class="small" class:active={paused} on:click={togglePause}>
    {paused ? copy.logs.resume : copy.logs.pause}
  </button>
  <button class="small" title={copy.logs.downloadHint} on:click={download}>{copy.logs.download}</button>
</div>

{#if RANK[show] < RANK[recording]}
  <p class="note">{copy.logs.belowLevel(copy.logs.levelName[recording])}</p>
{/if}

{#if !loaded}
  <p class="empty">…</p>
{:else if visible.length === 0}
  <p class="empty">{records.length ? copy.logs.noMatches : copy.logs.empty}</p>
{:else}
  <div class="table-wrap">
    <table class="stack">
      <tbody>
        {#each visible as r (r.seq)}
          <tr class={r.level}>
            <td class="when" title={fmtLogTimeFull(r.time)}>{new Date(r.time).toLocaleTimeString()}</td>
            <td class="lvl"><span class="level {r.level}">{copy.logs.levelName[r.level]}</span></td>
            <td class="msg full">
              <span class="text">{r.message}</span>
              {#each r.attrs ?? [] as a, i (i)}
                <span class="attr" title="{a.key}={a.value}"><span class="k">{a.key}</span>={a.value}</span>
              {/each}
            </td>
            <td class="actions end">
              <button class="row-action" on:click={() => copyLine(r)}>{copy.logs.copyLine}</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
<p class="note foot">{copy.logs.heldNote}</p>

<style>
  h1 small {
    color: var(--text-dim);
    font-size: 0.85rem;
    margin-left: 0.5rem;
  }

  .toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.6rem;
    margin-bottom: 0.5rem;
    flex: none;
  }

  .toolbar input {
    flex: 1 1 18rem;
    max-width: 28rem;
  }

  label.inline {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.82rem;
    color: var(--text-dim);
  }

  .status {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.8rem;
    color: var(--text-dim);
    margin-bottom: 0.4rem;
    flex: none;
  }

  .spacer {
    flex: 1;
  }

  .mode {
    font-size: 0.65rem;
    letter-spacing: 0.15em;
    text-transform: uppercase;
    border: 1px solid;
    border-radius: 999px;
    padding: 0.05rem 0.5rem;
  }

  .mode.live {
    color: var(--allowed);
  }

  .mode.paused {
    color: var(--accent);
  }

  button.small {
    padding: 0.1rem 0.55rem;
    font-size: 0.75rem;
  }

  button.small.active {
    border-color: var(--accent);
    color: var(--accent);
  }

  .note {
    flex: none;
    font-size: 0.8rem;
    color: var(--text-dim);
    margin: 0 0 0.5rem;
  }

  .note.foot {
    margin-top: 0.5rem;
  }

  .empty {
    color: var(--text-dim);
    font-style: italic;
  }

  .table-wrap {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }

  td.when {
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
    color: var(--text-dim);
    width: 1%;
  }

  td.lvl {
    width: 1%;
  }

  td.msg {
    white-space: normal;
    max-width: none;
  }

  .text {
    margin-right: 0.5rem;
  }

  .level {
    display: inline-block;
    min-width: 4.2rem;
    text-align: center;
    padding: 0 0.4rem;
    border-radius: 999px;
    font-size: 0.7rem;
    border: 1px solid var(--border);
    color: var(--text-dim);
  }

  .level.warn {
    color: var(--audit, #c9962e);
    border-color: var(--audit, #c9962e);
  }

  .level.error {
    color: var(--blocked);
    border-color: var(--blocked);
  }

  tr.error td.msg .text {
    color: var(--blocked);
  }

  .attr {
    display: inline-block;
    margin: 0.1rem 0.3rem 0.1rem 0;
    padding: 0 0.4rem;
    border-radius: 3px;
    background: var(--surface-2, rgba(127, 127, 127, 0.15));
    font-size: 0.75rem;
    max-width: min(32rem, 100%);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    vertical-align: middle;
  }

  .attr .k {
    color: var(--text-dim);
  }

  td.actions {
    width: 1%;
    text-align: right;
  }

  .row-action {
    padding: 0.1rem 0.6rem;
    font-size: 0.78rem;
    opacity: 0;
  }

  tr:hover .row-action,
  .row-action:focus-visible {
    opacity: 1;
  }

  @media (hover: none) {
    .row-action {
      opacity: 1;
    }
  }

  /* Stacked on a phone (table.stack): the 1% widths that keep the desktop
     columns tight would collapse these cells to nothing. */
  @media (max-width: 640px) {
    td.when,
    td.lvl,
    td.actions {
      width: auto;
    }
  }
</style>
