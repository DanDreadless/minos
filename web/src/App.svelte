<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, ApiError, setToken, type Status } from './lib/api';
  import Nav from './lib/components/Nav.svelte';
  import { copy } from './lib/copy';
  import { route } from './lib/router';
  import { toasts } from './lib/toast';
  import Codex from './pages/Codex.svelte';
  import Dashboard from './pages/Dashboard.svelte';
  import Docket from './pages/Docket.svelte';
  import Judgments from './pages/Judgments.svelte';
  import Settings from './pages/Settings.svelte';

  let status: Status | null = null;
  let needsToken = false;
  let tokenInput = '';
  let fatalError = '';
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  async function refresh(): Promise<void> {
    try {
      status = await api.status();
      needsToken = false;
      fatalError = '';
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
  <main>
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
      {#if $route === 'dashboard'}
        <Dashboard {status} onStatusChange={refresh} />
      {:else if $route === 'querylog'}
        <Docket />
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
    min-height: 100vh;
  }

  aside {
    position: sticky;
    top: 0;
    height: 100vh;
  }

  main {
    padding: 1.75rem 2rem 4rem;
    min-width: 0;
    max-width: 1200px;
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
    }

    aside {
      position: static;
      height: auto;
    }

    main {
      padding: 1.25rem 1rem 3rem;
    }
  }
</style>
