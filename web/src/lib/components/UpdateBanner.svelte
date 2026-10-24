<script lang="ts">
  // The "new version" banner. Where the root updater is installed it offers
  // a one-click update and follows its progress; elsewhere it shows the
  // upgrade command for this kind of install.
  import { onDestroy, onMount } from 'svelte';
  import { api, type UpdateInfo, type UpdateProgress } from '../api';
  import { confirmDialog } from '../confirm';
  import { copy } from '../copy';
  import { notify, notifyError } from '../toast';

  export let info: UpdateInfo;

  const c = copy.update;
  let progress: UpdateProgress | null = null;
  let showCommand = false;
  let timer: ReturnType<typeof setInterval> | null = null;

  const working = (p: UpdateProgress | null): boolean =>
    !!p && ['requested', 'downloading', 'verifying', 'installing', 'restarting'].includes(p.state);

  async function poll(): Promise<void> {
    try {
      const p = await api.updateProgress();
      // The server answering with no progress after a restart means the new
      // version is up but has not read the helper's file yet; keep waiting.
      if (p.state) progress = p;
      if (p.state === 'done') {
        stop();
        notify(c.done(p.version ?? ''));
        setTimeout(() => location.reload(), 1500);
      } else if (p.state === 'failed' || p.state === 'rolled_back') {
        stop();
      }
    } catch {
      // Minos is restarting; the next poll will reach the new version.
      if (progress) progress = { ...progress, state: 'restarting', message: c.restarting };
    }
  }

  function start(): void {
    stop();
    timer = setInterval(poll, 2000);
  }

  function stop(): void {
    if (timer) clearInterval(timer);
    timer = null;
  }

  async function updateNow(): Promise<void> {
    const ok = await confirmDialog({ message: c.confirm(info.latest ?? ''), confirm: c.updateNow });
    if (!ok) return;
    try {
      await api.installUpdate();
      progress = { state: 'requested', message: c.waiting };
      start();
    } catch (e) {
      notifyError(e);
    }
  }

  async function copyCommand(): Promise<void> {
    try {
      await navigator.clipboard.writeText(info.command);
      notify(c.copied);
    } catch (e) {
      // Clipboard needs a secure context; the command is still visible to copy.
      notifyError(e);
    }
  }

  // A failed or rolled-back update stays shown for an hour after it ended,
  // until dismissed, so a reload does not hide why the update is still here.
  const DISMISSED = 'minos-update-dismissed';
  function dismiss(): void {
    try {
      if (progress?.time) localStorage.setItem(DISMISSED, String(progress.time));
    } catch {
      // Storage may be blocked; the notice just returns on the next load.
    }
    progress = null;
  }

  function wasDismissed(t: number): boolean {
    try {
      return localStorage.getItem(DISMISSED) === String(t);
    } catch {
      return false;
    }
  }

  onMount(async () => {
    if (!info.self_update) return;
    const p = await api.updateProgress().catch(() => null);
    if (!p) return;
    if (working(p)) {
      // An update already running (the page was reloaded mid-way).
      progress = p;
      start();
    } else if ((p.state === 'failed' || p.state === 'rolled_back') && p.time) {
      if (Date.now() / 1000 - p.time < 3600 && !wasDismissed(p.time)) progress = p;
    }
  });

  onDestroy(stop);
</script>

<section class="update-banner" class:bad={progress?.state === 'failed' || progress?.state === 'rolled_back'}>
  <div class="line">
    <span>{c.available(info.latest ?? '')}</span>
    <a href={info.notes_url} target="_blank" rel="noreferrer">{c.whatsNew}</a>
  </div>

  {#if progress}
    <div class="progress" role="status" aria-live="polite">
      {#if working(progress)}<span class="spinner" aria-hidden="true"></span>{/if}
      <span>{progress.message || c.state(progress.state)}</span>
      {#if !working(progress)}
        <button class="link" on:click={dismiss}>{c.dismiss}</button>
      {/if}
    </div>
  {:else if info.self_update}
    <div class="cmd-row">
      <button class="primary" on:click={updateNow}>{c.updateNow}</button>
      <span class="how">{c.selfHint}</span>
      <button class="link" on:click={() => (showCommand = !showCommand)}>
        {showCommand ? c.hideCommand : c.showCommand}
      </button>
    </div>
  {/if}

  {#if !progress && (!info.self_update || showCommand)}
    <div class="cmd-row">
      <span class="how">{c.howTo(info.install_method)}</span>
      <code>{info.command}</code>
      <button on:click={copyCommand}>{c.copy}</button>
    </div>
    {#if !info.self_update && info.self_update_blocked && info.install_method === 'binary'}
      <p class="how">{info.self_update_blocked}</p>
    {/if}
  {/if}
</section>

<style>
  .update-banner {
    border: 1px solid var(--accent);
    border-radius: 6px;
    background: var(--bg-raised);
    padding: 0.75rem 1rem;
    margin-bottom: 1.25rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    flex: none;
  }

  .update-banner.bad {
    border-color: var(--blocked);
  }

  .line {
    display: flex;
    gap: 0.6rem;
    align-items: baseline;
    flex-wrap: wrap;
  }

  .line a {
    color: var(--accent);
  }

  .cmd-row,
  .progress {
    display: flex;
    gap: 0.6rem;
    align-items: center;
    flex-wrap: wrap;
  }

  .how {
    color: var(--text-dim);
    font-size: 0.85rem;
    margin: 0;
  }

  code {
    font-family: var(--font-mono);
    font-size: 0.8rem;
    background: var(--bg-sunken);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.3rem 0.5rem;
    overflow-x: auto;
    flex: 1;
    min-width: 12rem;
  }

  .cmd-row button {
    flex: none;
  }

  .link {
    background: none;
    border: none;
    padding: 0;
    color: var(--accent);
    cursor: pointer;
    font-size: 0.85rem;
  }

  .spinner {
    width: 0.9rem;
    height: 0.9rem;
    border: 2px solid var(--border);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.9s linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
