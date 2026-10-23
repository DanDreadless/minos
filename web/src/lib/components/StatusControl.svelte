<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, type Status } from '../api';
  import { checkOpen } from '../check';
  import { copy } from '../copy';
  import { notifyError } from '../toast';

  // The bottom of the side menu: whether Minos is actually blocking (said
  // honestly — "no rules loaded" is not "blocking"), pause/resume, and the
  // way into Check a domain.
  export let status: Status | null;
  export let onStatusChange: () => Promise<void>;

  let pauseOpen = false;
  let customPause = '';
  let root: HTMLElement;

  async function pause(duration: string): Promise<void> {
    pauseOpen = false;
    try {
      await api.pause(duration);
      await onStatusChange();
    } catch (e) {
      notifyError(e);
    }
  }

  async function pauseCustom(): Promise<void> {
    const d = customPause.trim();
    if (!d) return;
    await pause(d);
    customPause = '';
  }

  async function resume(): Promise<void> {
    try {
      await api.resume();
      await onStatusChange();
    } catch (e) {
      notifyError(e);
    }
  }

  function onClick(ev: MouseEvent): void {
    if (pauseOpen && root && !root.contains(ev.target as Node)) pauseOpen = false;
  }

  function onKey(ev: KeyboardEvent): void {
    if (ev.key === 'Escape') pauseOpen = false;
  }

  onMount(() => {
    window.addEventListener('click', onClick);
    window.addEventListener('keydown', onKey);
  });
  onDestroy(() => {
    window.removeEventListener('click', onClick);
    window.removeEventListener('keydown', onKey);
  });
</script>

<div class="status-control" bind:this={root}>
  {#if status}
    <div class="state">
      {#if status.paused}
        <span class="dot paused" aria-hidden="true"></span>
        <span class="text paused">
          {status.paused_until
            ? copy.pause.active(
                new Date(status.paused_until).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
              )
            : copy.pause.activeIndefinite}
        </span>
      {:else if status.rules === 0}
        <span class="dot off" aria-hidden="true"></span>
        <a class="text off" href="#/lists" title={copy.header.noRulesHint}>{copy.header.noRules}</a>
      {:else}
        <span class="dot on" aria-hidden="true"></span>
        <span class="text">{copy.header.blocking(status.rules)}</span>
      {/if}
    </div>
    <div class="buttons">
      {#if status.paused}
        <button class="small primary" on:click={resume}>{copy.pause.resume}</button>
      {:else}
        <div class="pause">
          <button
            class="small"
            aria-expanded={pauseOpen}
            on:click|stopPropagation={() => (pauseOpen = !pauseOpen)}>{copy.pause.action}</button
          >
          {#if pauseOpen}
            <div class="menu" role="menu">
              <p class="menu-hint">{copy.pause.actionHint}</p>
              {#each copy.header.pauseFor as p (p.duration)}
                <button role="menuitem" on:click={() => pause(p.duration)}>{p.label}</button>
              {/each}
              <form class="custom" on:submit|preventDefault={pauseCustom}>
                <input placeholder={copy.header.pauseCustom} bind:value={customPause} size="7" />
                <button type="submit" disabled={!customPause.trim()}>{copy.header.pauseGo}</button>
              </form>
            </div>
          {/if}
        </div>
      {/if}
      <button class="small" title={copy.header.searchLabel} on:click={() => checkOpen.set(true)}>
        {copy.header.searchPlaceholder} <kbd>Ctrl K</kbd>
      </button>
    </div>
  {/if}
</div>

<style>
  .status-control {
    display: flex;
    flex-direction: column;
    gap: 0.55rem;
  }

  .state {
    display: flex;
    align-items: baseline;
    gap: 0.45rem;
    font-size: 0.8rem;
  }

  .dot {
    flex: none;
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
  }

  .dot.on {
    background: var(--allowed);
  }

  .dot.paused {
    background: var(--accent);
  }

  .dot.off {
    background: var(--blocked);
  }

  .text.paused {
    color: var(--accent);
  }

  .text.off {
    color: var(--blocked);
  }

  .buttons {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
  }

  button.small {
    padding: 0.15rem 0.55rem;
    font-size: 0.75rem;
  }

  kbd {
    font-size: 0.62rem;
    color: var(--text-dim);
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 0 0.25rem;
    margin-left: 0.2rem;
  }

  .pause {
    position: relative;
  }

  /* Opens upwards: the control sits at the bottom of the menu. */
  .menu {
    position: absolute;
    bottom: calc(100% + 0.35rem);
    left: 0;
    z-index: 30;
    min-width: 12rem;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.6rem;
    box-shadow: 0 -6px 24px rgba(0, 0, 0, 0.35);
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }

  .menu-hint {
    margin: 0 0 0.2rem;
    font-size: 0.72rem;
    color: var(--text-dim);
  }

  .menu .custom {
    display: flex;
    gap: 0.35rem;
  }

  @media (hover: none) {
    kbd {
      display: none;
    }
  }
</style>
