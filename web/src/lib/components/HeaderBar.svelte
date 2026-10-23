<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, type CheckResult, type Status } from '../api';
  import { copy } from '../copy';
  import { docketHref } from '../router';
  import { notify, notifyError } from '../toast';

  // The bar on every page: whether Minos is actually blocking (said
  // honestly — "no rules loaded" is not "blocking"), the pause control, and
  // a quick domain check that leads straight to allow / block / the log.
  export let status: Status | null;
  export let onStatusChange: () => Promise<void>;

  let pauseOpen = false;
  let customPause = '';
  let query = '';
  let checked: CheckResult | null = null;
  let checking = false;
  let searchEl: HTMLInputElement;
  let bar: HTMLElement;

  // A pasted URL or "example.com/path" checks the host part.
  function hostOf(s: string): string {
    let v = s.trim().toLowerCase();
    v = v.replace(/^[a-z][a-z0-9+.-]*:\/\//, '');
    v = v.split(/[/?#]/)[0];
    return v.replace(/:\d+$/, '').replace(/\.$/, '');
  }

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

  async function check(): Promise<void> {
    const d = hostOf(query);
    if (!d) return;
    checking = true;
    try {
      checked = await api.check(d);
    } catch (e) {
      checked = null;
      notifyError(e);
    } finally {
      checking = false;
    }
  }

  async function allow(d: string): Promise<void> {
    try {
      await api.pardon(d);
      notify(copy.allow.done(d));
      checked = await api.check(d);
    } catch (e) {
      notifyError(e);
    }
  }

  async function block(d: string): Promise<void> {
    try {
      await api.sentence(d);
      notify(copy.block.done(d));
      checked = await api.check(d);
    } catch (e) {
      notifyError(e);
    }
  }

  function close(): void {
    checked = null;
    pauseOpen = false;
  }

  function onKey(ev: KeyboardEvent): void {
    if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === 'k') {
      ev.preventDefault();
      searchEl?.focus();
      searchEl?.select();
    } else if (ev.key === 'Escape') {
      close();
    }
  }

  function onClick(ev: MouseEvent): void {
    if (bar && !bar.contains(ev.target as Node)) close();
  }

  function checkText(r: CheckResult): string {
    if (r.verdict === 'blocked') return copy.domains.checkBlocked(r.list, r.rule);
    return r.rule ? copy.domains.checkAllowedByRule(r.rule) : copy.domains.checkAllowed;
  }

  onMount(() => {
    window.addEventListener('keydown', onKey);
    window.addEventListener('click', onClick);
  });
  onDestroy(() => {
    window.removeEventListener('keydown', onKey);
    window.removeEventListener('click', onClick);
  });
</script>

<header class="bar" bind:this={bar}>
  {#if status}
    <div class="state">
      {#if status.paused}
        <span class="dot paused" aria-hidden="true"></span>
        <span class="text paused">
          {status.paused_until
            ? copy.pause.active(new Date(status.paused_until).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }))
            : copy.pause.activeIndefinite}
        </span>
        <button class="small primary" on:click={resume}>{copy.pause.resume}</button>
      {:else if status.rules === 0}
        <span class="dot off" aria-hidden="true"></span>
        <a class="text off" href="#/lists" title={copy.header.noRulesHint}>{copy.header.noRules}</a>
      {:else}
        <span class="dot on" aria-hidden="true"></span>
        <span class="text">{copy.header.blocking(status.rules)}</span>
        <div class="pause">
          <button
            class="small"
            aria-expanded={pauseOpen}
            on:click|stopPropagation={() => {
              pauseOpen = !pauseOpen;
              checked = null;
            }}>{copy.pause.action}</button
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
    </div>
  {/if}

  <form class="quick" on:submit|preventDefault={check} role="search">
    <input
      bind:this={searchEl}
      bind:value={query}
      type="search"
      placeholder={copy.header.searchPlaceholder}
      aria-label={copy.header.searchLabel}
      spellcheck="false"
      autocomplete="off"
    />
    <kbd aria-hidden="true">Ctrl K</kbd>
    {#if checked}
      <div class="menu result" role="dialog" aria-label={copy.header.searchLabel}>
        <p class="domain">{checked.domain}</p>
        <p class:blocked={checked.verdict === 'blocked'}>{checkText(checked)}</p>
        <div class="actions">
          {#if checked.verdict === 'blocked'}
            <button class="primary" on:click={() => checked && allow(checked.domain)}>{copy.allow.action}</button>
          {:else}
            <button on:click={() => checked && block(checked.domain)}>{copy.block.action}</button>
          {/if}
          <a
            href={docketHref({ domain: checked.domain })}
            on:click={close}>{copy.header.openInLog}</a
          >
        </div>
      </div>
    {:else if checking}
      <div class="menu result"><p>…</p></div>
    {/if}
  </form>
</header>

<style>
  .bar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.75rem 1.25rem;
    margin-bottom: 1.25rem;
    flex: none;
  }

  .state {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
  }

  .dot {
    width: 0.55rem;
    height: 0.55rem;
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

  button.small {
    padding: 0.15rem 0.6rem;
    font-size: 0.78rem;
  }

  .pause,
  .quick {
    position: relative;
  }

  .menu {
    position: absolute;
    top: calc(100% + 0.35rem);
    left: 0;
    z-index: 20;
    min-width: 13rem;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.6rem;
    box-shadow: 0 6px 24px rgba(0, 0, 0, 0.35);
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }

  .menu-hint {
    margin: 0 0 0.2rem;
    font-size: 0.75rem;
    color: var(--text-dim);
  }

  .menu .custom {
    display: flex;
    gap: 0.35rem;
  }

  .quick {
    margin-left: auto;
    display: flex;
    align-items: center;
    flex: 0 1 22rem;
    min-width: 12rem;
  }

  .quick input {
    width: 100%;
    padding-right: 3.6rem;
  }

  kbd {
    position: absolute;
    right: 0.5rem;
    font-size: 0.65rem;
    color: var(--text-dim);
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 0 0.3rem;
    pointer-events: none;
  }

  .result {
    left: auto;
    right: 0;
    width: 100%;
    min-width: 16rem;
  }

  .result p {
    margin: 0;
    font-size: 0.85rem;
  }

  .result .domain {
    font-family: var(--font-mono);
    font-weight: 600;
  }

  .result .blocked {
    color: var(--blocked);
  }

  .result .actions {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin-top: 0.3rem;
    font-size: 0.82rem;
  }

  @media (hover: none) {
    kbd {
      display: none;
    }
  }
</style>
