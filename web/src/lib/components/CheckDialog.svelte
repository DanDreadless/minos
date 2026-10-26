<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { api, type CheckResult } from '../api';
  import { checkOpen } from '../check';
  import { copy } from '../copy';
  import { fmtClock } from '../format';
  import { docketHref } from '../router';
  import { notify, notifyError } from '../toast';

  // Check a domain, from any page: paste a name or URL, see whether it is
  // blocked and which rule decides, then allow or block it, or open its
  // lookups in the Query Log — two clicks from "why is this broken" to fixed.
  let query = '';
  let checked: CheckResult | null = null;
  let checking = false;
  // TempUntil is when a temporary allow deciding the checked name ends.
  let tempUntil = '';
  let input: HTMLInputElement;

  $: if ($checkOpen) void focusInput();

  async function focusInput(): Promise<void> {
    await tick();
    input?.focus();
    input?.select();
  }

  // A pasted URL or "example.com/path" checks the host part.
  function hostOf(s: string): string {
    let v = s.trim().toLowerCase();
    v = v.replace(/^[a-z][a-z0-9+.-]*:\/\//, '');
    v = v.split(/[/?#]/)[0];
    return v.replace(/:\d+$/, '').replace(/\.$/, '');
  }

  async function judge(d: string): Promise<void> {
    checked = await api.check(d);
    tempUntil = '';
    if (checked.list === 'temporary-allow') {
      const t = (await api.temporaryAllows().catch(() => [])).find((x) => x.domain === checked?.rule);
      if (t) tempUntil = fmtClock(t.until);
    }
  }

  async function endTemporary(d: string): Promise<void> {
    try {
      await api.removeTemporaryAllow(d);
      if (checked) await judge(checked.domain);
    } catch (e) {
      notifyError(e);
    }
  }

  async function check(): Promise<void> {
    const d = hostOf(query);
    if (!d) return;
    checking = true;
    try {
      await judge(d);
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
      await judge(d);
    } catch (e) {
      notifyError(e);
    }
  }

  async function allowHour(d: string): Promise<void> {
    try {
      const r = await api.allowFor(d, 60);
      notify(r.until ? copy.allow.doneFor(r.domain, fmtClock(r.until)) : copy.allow.alreadyAlways(r.domain));
      await judge(d);
    } catch (e) {
      notifyError(e);
    }
  }

  async function block(d: string): Promise<void> {
    try {
      await api.sentence(d);
      notify(copy.block.done(d));
      await judge(d);
    } catch (e) {
      notifyError(e);
    }
  }

  function close(): void {
    checkOpen.set(false);
  }

  function checkText(r: CheckResult, until: string): string {
    if (r.verdict === 'blocked') return copy.domains.checkBlocked(r.list, r.rule);
    if (r.list === 'temporary-allow' && until) return copy.domains.checkAllowedFor(r.rule, until);
    return r.rule ? copy.domains.checkAllowedByRule(r.rule) : copy.domains.checkAllowed;
  }

  function onKey(ev: KeyboardEvent): void {
    if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === 'k') {
      ev.preventDefault();
      checkOpen.set(true);
      void focusInput();
    } else if (ev.key === 'Escape' && $checkOpen) {
      close();
    }
  }

  onMount(() => window.addEventListener('keydown', onKey));
  onDestroy(() => window.removeEventListener('keydown', onKey));
</script>

{#if $checkOpen}
  <div class="backdrop" role="presentation" on:click={close}></div>
  <div class="dialog card" role="dialog" aria-modal="true" aria-label={copy.header.searchLabel}>
    <h2>{copy.domains.checkTitle} <small>{copy.domains.checkHint}</small></h2>
    <form on:submit|preventDefault={check}>
      <input
        bind:this={input}
        bind:value={query}
        type="search"
        placeholder={copy.check.placeholder}
        aria-label={copy.header.searchLabel}
        spellcheck="false"
        autocomplete="off"
      />
      <button type="submit" class="primary" disabled={checking || !query.trim()}>{copy.domains.checkButton}</button>
    </form>
    {#if checked}
      <div class="result">
        <p class="domain">{checked.domain}</p>
        <p class:blocked={checked.verdict === 'blocked'}>{checkText(checked, tempUntil)}</p>
        <div class="actions">
          {#if checked.list === 'temporary-allow'}
            <button class="primary" on:click={() => checked && allow(checked.rule)}>{copy.domains.checkAllowForGood}</button>
            <button on:click={() => checked && endTemporary(checked.rule)}>{copy.domains.checkEndNow}</button>
          {:else if checked.verdict === 'blocked'}
            <button class="primary" on:click={() => checked && allow(checked.domain)}>{copy.allow.action}</button>
            <button title={copy.allow.hourHint} on:click={() => checked && allowHour(checked.domain)}
              >{copy.allow.hourActionLong}</button
            >
          {:else}
            <button on:click={() => checked && block(checked.domain)}>{copy.block.action}</button>
          {/if}
          <a href={docketHref({ domain: checked.domain })} on:click={close}>{copy.header.openInLog}</a>
        </div>
      </div>
    {/if}
    <p class="hint">{copy.check.keys}</p>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 80;
  }

  .dialog {
    position: fixed;
    z-index: 81;
    top: 18%;
    left: 50%;
    transform: translateX(-50%);
    width: min(32rem, calc(100vw - 2rem));
    box-shadow: 0 12px 40px rgba(0, 0, 0, 0.5);
  }

  h2 small {
    color: var(--text-dim);
    font-size: 0.78rem;
    margin-left: 0.5rem;
  }

  form {
    display: flex;
    gap: 0.5rem;
  }

  form input {
    flex: 1;
  }

  .result {
    margin-top: 0.9rem;
  }

  .result p {
    margin: 0 0 0.3rem;
  }

  .result .domain {
    font-family: var(--font-mono);
    font-weight: 600;
  }

  .result .blocked {
    color: var(--blocked);
  }

  .actions {
    display: flex;
    align-items: center;
    gap: 0.9rem;
    margin-top: 0.5rem;
  }

  .hint {
    margin: 0.9rem 0 0;
    font-size: 0.75rem;
    color: var(--text-dim);
  }
</style>
