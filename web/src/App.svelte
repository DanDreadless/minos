<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, ApiError, setToken, type Status, type UpdateInfo } from './lib/api';
  import ConfirmHost from './lib/components/ConfirmHost.svelte';
  import CheckDialog from './lib/components/CheckDialog.svelte';
  import Nav from './lib/components/Nav.svelte';
  import UpdateBanner from './lib/components/UpdateBanner.svelte';
  import { copy } from './lib/copy';
  import { confirmDialog } from './lib/confirm';
  import { leaveGuarded, route, setLeaveGuard } from './lib/router';
  import { toasts } from './lib/toast';
  import Blocking from './pages/Blocking.svelte';
  import Dashboard from './pages/Dashboard.svelte';
  import DevicePage from './pages/Device.svelte';
  import Devices from './pages/Devices.svelte';
  import QueryLog from './pages/QueryLog.svelte';
  import Help from './pages/Help.svelte';
  import LocalDNS from './pages/LocalDNS.svelte';
  import Logs from './pages/Logs.svelte';
  import Settings from './pages/Settings.svelte';

  let status: Status | null = null;
  let updateInfo: UpdateInfo | null = null;
  // Gate is the sign-in screen to show instead of the app: the API token, or
  // the password (and an authenticator code when needCode).
  let gate: '' | 'token' | 'password' = '';
  let tokenInput = '';
  let passwordInput = '';
  let codeInput = '';
  let needCode = false;
  let gateError = '';
  let signingIn = false;
  $: needsToken = gate !== '';
  let fatalError = '';
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  async function refresh(): Promise<void> {
    try {
      status = await api.status();
      gate = '';
      fatalError = '';
      // Fetch the actionable upgrade guidance once an update is flagged.
      if (status.update_available && !updateInfo) {
        updateInfo = await api.update().catch(() => null);
      }
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        if (!gate) {
          const auth = await api.authStatus().catch(() => null);
          gate = auth?.password_set ? 'password' : 'token';
        }
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

  async function submitPassword(): Promise<void> {
    signingIn = true;
    gateError = '';
    try {
      await api.login(passwordInput, codeInput.trim());
      passwordInput = '';
      codeInput = '';
      needCode = false;
      await refresh();
    } catch (e) {
      if (e instanceof ApiError && e.needCode) {
        // Keep the password; a code is all that is missing (or was wrong).
        if (needCode) gateError = e.message;
        needCode = true;
        codeInput = '';
      } else {
        gateError = e instanceof Error ? e.message : String(e);
        passwordInput = '';
      }
    } finally {
      signingIn = false;
    }
  }

  // Ask before an in-app link leaves a page with unsaved changes.
  async function guardLinks(ev: MouseEvent): Promise<void> {
    if (!leaveGuarded() || ev.defaultPrevented || ev.button !== 0 || ev.ctrlKey || ev.metaKey) return;
    const a = (ev.target as HTMLElement | null)?.closest('a');
    const href = a?.getAttribute('href') ?? '';
    if (!href.startsWith('#/') || href.startsWith('#/settings')) return;
    ev.preventDefault();
    if (await confirmDialog({ message: copy.confirm.leaveMessage, confirm: copy.confirm.leave, danger: true })) {
      setLeaveGuard(false);
      location.hash = href;
    }
  }

  onMount(() => {
    document.addEventListener('click', guardLinks, true);
    void refresh();
    pollTimer = setInterval(refresh, 5000);
  });

  onDestroy(() => {
    document.removeEventListener('click', guardLinks, true);
    if (pollTimer) clearInterval(pollTimer);
  });
</script>

<div class="shell">
  <aside>
    <Nav {status} onStatusChange={refresh} />
  </aside>
  <main class:fill={!needsToken && ($route === 'querylog' || $route === 'logs')}>
    {#if needsToken}
      <section class="token-gate">
        <img class="gate-logo" src="/logo.png" alt="" />
        <h1>{copy.appName}</h1>
        {#if gate === 'password'}
          <p>{needCode ? copy.login.codePrompt : copy.login.prompt}</p>
          <form class="login" on:submit|preventDefault={submitPassword}>
            {#if !needCode}
              <!-- svelte-ignore a11y-autofocus -->
              <input
                type="password"
                placeholder={copy.login.password}
                aria-label={copy.login.password}
                bind:value={passwordInput}
                autocomplete="current-password"
                autofocus
              />
            {:else}
              <!-- svelte-ignore a11y-autofocus -->
              <input
                class="code"
                inputmode="numeric"
                pattern="[0-9 ]*"
                maxlength="7"
                placeholder={copy.login.code}
                aria-label={copy.login.code}
                bind:value={codeInput}
                autocomplete="one-time-code"
                autofocus
              />
            {/if}
            <button type="submit" class="primary" disabled={signingIn || (!needCode && !passwordInput)}>
              {signingIn ? copy.login.signingIn : copy.login.submit}
            </button>
          </form>
          {#if gateError}
            <p class="gate-error" role="alert">{gateError}</p>
          {/if}
          {#if needCode}
            <button
              class="link"
              on:click={() => {
                needCode = false;
                passwordInput = '';
                gateError = '';
              }}>{copy.login.back}</button
            >
          {/if}
          <p class="gate-hint">{copy.login.forgot}</p>
        {:else}
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
        {/if}
      </section>
    {:else}
      {#if fatalError}
        <p class="fatal" role="alert">{fatalError}</p>
      {/if}
      {#if status?.update_available && updateInfo}
        <UpdateBanner info={updateInfo} />
      {/if}
      {#if $route === 'dashboard'}
        <Dashboard {status} />
      {:else if $route === 'querylog'}
        <QueryLog />
      {:else if $route === 'devices'}
        <Devices />
      {:else if $route === 'device'}
        <DevicePage />
      {:else if $route === 'lists' || $route === 'allowed' || $route === 'blocked'}
        <Blocking />
      {:else if $route === 'local'}
        <LocalDNS />
      {:else if $route === 'logs'}
        <Logs />
      {:else if $route === 'settings'}
        <Settings />
      {:else if $route === 'help'}
        <Help />
      {/if}
    {/if}
  </main>
</div>

<ConfirmHost />
<CheckDialog />

<div class="toasts" aria-live="polite">
  {#each $toasts as t (t.id)}
    <div class="toast {t.kind}">{t.text}</div>
  {/each}
</div>

<style>
  .shell {
    display: grid;
    /* minmax(0, …): a plain 1fr can't shrink below its widest content, so
       one wide table would stretch the whole page past the screen. */
    grid-template-columns: 230px minmax(0, 1fr);
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

  /* The Query Log fills the viewport and scrolls its table internally instead
     of growing the page (see QueryLog.svelte); every other page scrolls
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

  .token-gate input {
    min-width: 0;
    flex: 1;
  }

  .token-gate .code {
    letter-spacing: 0.3em;
    text-align: center;
  }

  .gate-error {
    color: var(--blocked);
  }

  .gate-hint {
    margin-top: 2rem;
    font-size: 0.8rem;
    color: var(--text-dim);
  }

  .token-gate .link {
    background: none;
    border: none;
    color: var(--accent);
    cursor: pointer;
    margin-top: 0.5rem;
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
      grid-template-columns: minmax(0, 1fr);
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
       scroll inside the Query Log table on a short screen. */
    main.fill {
      display: block;
      overflow: visible;
      padding-bottom: 3rem;
    }
  }
</style>
