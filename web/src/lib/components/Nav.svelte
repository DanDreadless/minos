<script lang="ts">
  import { onDestroy } from 'svelte';
  import type { Status } from '../api';
  import { copy } from '../copy';
  import { bootOf, hasUnseen, logSeen } from '../logseen';
  import StatusControl from './StatusControl.svelte';
  import { hrefFor, route, type Route } from '../router';

  export let status: Status | null;
  export let onStatusChange: () => Promise<void>;

  const items: { key: Route; label: string; hint: string }[] = [
    { key: 'dashboard', ...copy.nav.dashboard },
    { key: 'querylog', ...copy.nav.querylog },
    { key: 'devices', ...copy.nav.devices },
    { key: 'lists', ...copy.nav.lists },
    { key: 'logs', ...copy.nav.logs },
    { key: 'settings', ...copy.nav.settings },
    { key: 'help', ...copy.nav.help },
  ];

  // A dot on Logs while there is a warning or error the user hasn't seen.
  $: logAlert = status
    ? hasUnseen(status.log_alert_seq ?? 0, bootOf(status.uptime_seconds), $logSeen)
    : false;

  // The drawer is a mobile-only concern: on desktop the CSS keeps <nav>
  // static and hides the burger/backdrop, so `open` is inert there.
  let open = false;
  const closeDrawer = () => (open = false);
  const toggleDrawer = () => (open = !open);

  // Lock the background from scrolling while the drawer is over it.
  function setBodyLock(locked: boolean): void {
    if (typeof document !== 'undefined') {
      document.body.style.overflow = locked ? 'hidden' : '';
    }
  }
  $: setBodyLock(open);
  onDestroy(() => setBodyLock(false));
</script>

<svelte:window on:keydown={(e) => e.key === 'Escape' && closeDrawer()} />

<div class="topbar">
  <button
    class="burger"
    aria-label={open ? copy.nav.closeMenu : copy.nav.openMenu}
    aria-expanded={open}
    aria-controls="primary-nav"
    on:click={toggleDrawer}
  >
    <span class="bars" aria-hidden="true"><span></span><span></span><span></span></span>
  </button>
  <img class="topbar-wordmark" src="/banner.png" alt={copy.appName} />
  {#if status && (status.paused || status.rules === 0)}
    <!-- The drawer hides the full status on phones; a paused or
         not-blocking state must still be visible without opening it. -->
    <button class="topbar-state" class:off={!status.paused} on:click={toggleDrawer}>
      {status.paused ? copy.pause.headerPill : copy.header.notBlockingShort}
    </button>
  {/if}
</div>

{#if open}
  <button class="backdrop" aria-label={copy.nav.closeMenu} on:click={closeDrawer}></button>
{/if}

<nav id="primary-nav" class:open>
  <button class="close" aria-label={copy.nav.closeMenu} on:click={closeDrawer}>&times;</button>
  <div class="meander" aria-hidden="true"></div>
  <a class="brand" href="#/">
    <img class="wordmark" src="/banner.png" alt={copy.appName} />
    <span class="tagline">{copy.tagline}</span>
  </a>
  <ul>
    {#each items as item (item.key)}
      <li>
        <a
          href={hrefFor[item.key]}
          class:active={$route === item.key ||
            ($route === 'device' && item.key === 'devices') ||
            (($route === 'allowed' || $route === 'blocked' || $route === 'privacy') && item.key === 'lists')}
          title={item.hint}
          on:click={closeDrawer}
        >
          <span class="label"
            >{item.label}{#if item.key === 'logs' && logAlert}<span
                class="alert-dot"
                title={copy.nav.logsAlert}
                aria-label={copy.nav.logsAlert}
              ></span>{/if}</span
          >
          <span class="hint">{item.hint}</span>
        </a>
      </li>
    {/each}
  </ul>
  <div class="foot">
    <StatusControl {status} {onStatusChange} />
    {#if status}
      <span class="version">
        v{status.version}
        {#if status.update_available && status.latest_version}
          <a
            class="update"
            href="https://github.com/DanDreadless/minos/releases"
            target="_blank"
            rel="noreferrer"
          >
            {copy.settings.updateAvailable(status.latest_version)}
          </a>
        {/if}
      </span>
    {/if}
  </div>
</nav>

<style>
  .alert-dot {
    display: inline-block;
    width: 0.45rem;
    height: 0.45rem;
    margin-left: 0.4rem;
    border-radius: 50%;
    background: var(--blocked);
    vertical-align: middle;
  }

  nav {
    display: flex;
    flex-direction: column;
    height: 100%;
    background: var(--bg-sunken);
    border-right: 1px solid var(--border);
  }

  .meander {
    height: 5px;
    background: repeating-linear-gradient(90deg, var(--accent) 0 10px, transparent 10px 20px);
    opacity: 0.55;
  }

  .brand {
    display: block;
    padding: 1.4rem 1.25rem 1.1rem;
    text-decoration: none;
    color: var(--text);
    border-bottom: 1px solid var(--border);
  }

  /* The banner's baked-in background is #101217 — identical to
     --bg-sunken, so it sits flush on the sidebar. */
  .brand .wordmark {
    display: block;
    width: 100%;
    max-width: 180px;
    height: auto;
    margin-inline: auto;
  }

  .brand .tagline {
    display: block;
    margin-top: 0.35rem;
    font-family: var(--font-display);
    font-style: italic;
    font-size: 0.78rem;
    color: var(--text-dim);
    text-align: center;
  }

  ul {
    list-style: none;
    margin: 0.75rem 0 0;
    padding: 0;
    flex: 1;
  }

  li a {
    display: block;
    padding: 0.55rem 1.25rem;
    text-decoration: none;
    color: var(--text);
    border-left: 3px solid transparent;
    transition:
      background 0.12s,
      border-color 0.12s;
  }

  li a:hover {
    background: var(--bg-hover);
  }

  li a.active {
    border-left-color: var(--accent);
    background: var(--bg-raised);
  }

  li a .label {
    display: block;
    font-family: var(--font-display);
    letter-spacing: 0.04em;
  }

  li a.active .label {
    color: var(--accent);
  }

  li a .hint {
    display: block;
    font-size: 0.72rem;
    color: var(--text-dim);
  }

  .foot {
    padding: 1rem 1.25rem;
    border-top: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 0.75rem;
    font-size: 0.75rem;
  }

  .topbar-state {
    margin-left: auto;
    padding: 0.1rem 0.55rem;
    font-size: 0.72rem;
    border-color: var(--accent);
    color: var(--accent);
  }

  .topbar-state.off {
    border-color: var(--blocked);
    color: var(--blocked);
  }

  .version {
    margin-left: auto;
    color: var(--text-dim);
    text-align: right;
  }

  .version .update {
    display: block;
    color: var(--accent);
    text-decoration: none;
  }

  .version .update:hover {
    text-decoration: underline;
  }

  /* The mobile top bar, the burger, the drawer close button and the
     backdrop are all hidden on desktop, where <nav> is the static
     sidebar. The @media block below switches them on and turns <nav>
     into an off-canvas drawer. */
  .topbar,
  .close,
  .backdrop {
    display: none;
  }

  .topbar {
    align-items: center;
    gap: 0.6rem;
    padding: 0.4rem 0.75rem;
    background: var(--bg-sunken);
    border-bottom: 1px solid var(--border);
  }

  .burger {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 2.6rem;
    height: 2.6rem;
    padding: 0;
    border-color: transparent;
  }

  .burger .bars {
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    width: 20px;
    height: 15px;
  }

  .burger .bars span {
    display: block;
    height: 2px;
    background: currentColor;
    border-radius: 1px;
  }

  .topbar-wordmark {
    height: 26px;
    width: auto;
  }

  .close {
    position: absolute;
    top: 0.5rem;
    right: 0.6rem;
    width: 2rem;
    height: 2rem;
    padding: 0;
    line-height: 1;
    font-size: 1.4rem;
    border-color: transparent;
    color: var(--text-dim);
  }

  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 40;
    width: 100%;
    height: 100%;
    padding: 0;
    border: none;
    border-radius: 0;
    background: rgba(0, 0, 0, 0.55);
  }

  @media (max-width: 800px) {
    .topbar {
      display: flex;
    }

    .close {
      display: block;
    }

    .backdrop {
      display: block;
    }

    nav {
      position: fixed;
      top: 0;
      left: 0;
      bottom: 0;
      z-index: 50;
      width: min(82vw, 300px);
      height: auto;
      overflow-y: auto;
      box-shadow: 2px 0 18px rgba(0, 0, 0, 0.45);
      transform: translateX(-100%);
      transition: transform 0.22s ease;
    }

    nav.open {
      transform: translateX(0);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    nav {
      transition: none;
    }
  }
</style>
