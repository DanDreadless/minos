<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, ApiError, setToken, type Status, type UpdateInfo } from './lib/api';
  import Nav from './lib/components/Nav.svelte';
  import { copy } from './lib/copy';
  import { route } from './lib/router';
  import { notify, notifyError, toasts } from './lib/toast';
  import Codex from './pages/Codex.svelte';
  import Dashboard from './pages/Dashboard.svelte';
  import Devices from './pages/Devices.svelte';
  import Docket from './pages/Docket.svelte';
  import Judgments from './pages/Judgments.svelte';
  import Settings from './pages/Settings.svelte';

  let status: Status | null = null;
  let updateInfo: UpdateInfo | null = null;
  let needsToken = false;
  let tokenInput = '';
  let fatalError = '';
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  async function refresh(): Promise<void> {
    try {
      status = await api.status();
      needsToken = false;
      fatalError = '';
      // Fetch the actionable upgrade guidance once an update is flagged.
      if (status.update_available && !updateInfo) {
        updateInfo = await api.update().catch(() => null);
      }
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        needsToken = true;
      } else {
        fatalError = e instanceof Error ? e.message : String(e);
      }
    }
  }

  async function submitToken(): Promise<void> {
    setToken(tokenInput.trim());
    tokenInput = '';
    await refresh();
  }

  async function copyCommand(): Promise<void> {
    if (!updateInfo) return;
    try {
      await navigator.clipboard.writeText(updateInfo.command);
      notify(copy.update.copied);
    } catch (e) {
      // Clipboard needs a secure context; the command is still visible to copy.
      notifyError(e);
    }
  }

  onMount(() => {
    void refresh();
    pollTimer = setInterval(refresh, 5000);
  });

  onDestroy(() => {
    if (pollTimer) clearInterval(pollTimer);
  });
</script>

<div class="shell">
  <aside>
    <Nav {status} />
  </aside>
  <main class:fill={!needsToken && $route === 'querylog'}>
    {#if needsToken}
      <section class="token-gate">
        <img class="gate-logo" src="/logo.png" alt="" />
        <h1>{copy.appName}</h1>
        <p>{copy.token.prompt}</p>
        <form on:submit|preventDefault={submitToken}>
          <input
            type="password"
            placeholder={copy.token.placeholder}
            bind:value={tokenInput}
            autocomplete="off"
          />
          <button type="submit" class="primary">{copy.token.submit}</button>
        </form>
      </section>
    {:else}
      {#if fatalError}
        <p class="fatal" role="alert">{fatalError}</p>
      {/if}
      {#if status?.update_available && updateInfo}
        <section class="update-banner">
          <div class="line">
            <span>{copy.update.available(updateInfo.latest ?? '')}</span>
            <a href={updateInfo.notes_url} target="_blank" rel="noreferrer">{copy.update.whatsNew}</a>
          </div>
          <div class="cmd-row">
            <span class="how">{copy.update.howTo(updateInfo.install_method)}</span>
            <code>{updateInfo.command}</code>
            <button on:click={copyCommand}>{copy.update.copy}</button>
          </div>
        </section>
      {/if}
      {#if $route === 'dashboard'}
        <Dashboard {status} onStatusChange={refresh} />
      {:else if $route === 'querylog'}
        <Docket />
      {:else if $route === 'devices'}
        <Devices />
      {:else if $route === 'lists'}
        <Codex />
      {:else if $route === 'domains'}
        <Judgments />
      {:else if $route === 'settings'}
        <Settings />
      {/if}
    {/if}
  </main>
</div>

<div class="toasts" aria-live="polite">
  {#each $toasts as t (t.id)}
    <div class="toast {t.kind}">{t.text}</div>
  {/each}
</div>

<style>
  .shell {
    display: grid;
    grid-template-columns: 230px 1fr;
    height: 100vh;
    overflow: hidden;
  }

  aside {
    height: 100vh;
  }

  main {
    padding: 1.75rem 2rem 4rem;
    min-width: 0;
    overflow-y: auto;
  }

  /* The Docket fills the viewport and scrolls its table internally instead
     of growing the page (see Docket.svelte); every other page scrolls
     normally within main. */
  main.fill {
    display: flex;
    flex-direction: column;
    overflow: hidden;
    padding-bottom: 1.75rem;
  }

  .token-gate {
    max-width: 24rem;
    margin: 4rem auto;
    text-align: center;
  }

  .gate-logo {
    width: 160px;
    height: 160px;
  }

  .token-gate h1 {
    letter-spacing: 0.28em;
    text-transform: uppercase;
    color: var(--accent);
  }

  .token-gate form {
    display: flex;
    gap: 0.5rem;
    justify-content: center;
  }

  .fatal {
    color: var(--blocked);
  }

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

  .update-banner .line {
    display: flex;
    gap: 0.6rem;
    align-items: baseline;
    flex-wrap: wrap;
  }

  .update-banner .line a {
    color: var(--accent);
  }

  .update-banner .cmd-row {
    display: flex;
    gap: 0.6rem;
    align-items: center;
    flex-wrap: wrap;
  }

  .update-banner .how {
    color: var(--text-dim);
    font-size: 0.85rem;
  }

  .update-banner code {
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

  .update-banner .cmd-row button {
    flex: none;
  }

  .toasts {
    position: fixed;
    bottom: 1.25rem;
    right: 1.25rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    z-index: 10;
    max-width: 26rem;
  }

  .toast {
    padding: 0.6rem 1rem;
    border-radius: 4px;
    background: var(--bg-raised);
    border: 1px solid var(--accent);
    color: var(--text);
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
    font-size: 0.85rem;
  }

  .toast.error {
    border-color: var(--blocked);
    color: var(--blocked);
  }

  @media (max-width: 800px) {
    .shell {
      grid-template-columns: 1fr;
      height: auto;
      overflow: visible;
    }

    aside {
      height: auto;
    }

    main {
      padding: 1.25rem 1rem 3rem;
    }

    /* On mobile the whole page scrolls (nav stacks on top); don't trap
       scroll inside the Docket table on a short screen. */
    main.fill {
      display: block;
      overflow: visible;
      padding-bottom: 3rem;
    }
  }
</style>
