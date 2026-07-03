<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type CheckResult } from '../lib/api';
  import { copy } from '../lib/copy';
  import { notify, notifyError } from '../lib/toast';

  let allowlist: string[] = [];
  let denylist: string[] = [];
  let newPardon = '';
  let newSentence = '';
  let checkDomain = '';
  let checkResult: CheckResult | null = null;
  let checkError = '';

  async function load(): Promise<void> {
    try {
      [allowlist, denylist] = await Promise.all([api.allowlist(), api.denylist()]);
    } catch (e) {
      notifyError(e);
    }
  }

  async function check(): Promise<void> {
    checkResult = null;
    checkError = '';
    try {
      checkResult = await api.check(checkDomain.trim());
    } catch (e) {
      checkError = e instanceof Error ? e.message : String(e);
    }
  }

  function describeCheck(r: CheckResult): string {
    if (r.verdict === 'blocked') return copy.domains.checkBlocked(r.list, r.rule);
    if (r.rule) return copy.domains.checkAllowedByRule(r.rule);
    return copy.domains.checkAllowed;
  }

  async function addPardon(): Promise<void> {
    try {
      await api.pardon(newPardon.trim());
      notify(copy.pardon.done(newPardon.trim()));
      newPardon = '';
      await load();
    } catch (e) {
      notifyError(e);
    }
  }

  async function addSentence(): Promise<void> {
    try {
      await api.sentence(newSentence.trim());
      notify(copy.sentence.done(newSentence.trim()));
      newSentence = '';
      await load();
    } catch (e) {
      notifyError(e);
    }
  }

  async function removePardon(d: string): Promise<void> {
    try {
      await api.unpardon(d);
      await load();
    } catch (e) {
      notifyError(e);
    }
  }

  async function removeSentence(d: string): Promise<void> {
    try {
      await api.unsentence(d);
      await load();
    } catch (e) {
      notifyError(e);
    }
  }

  onMount(() => void load());
</script>

<h1>{copy.nav.domains.label} <small>{copy.nav.domains.hint}</small></h1>

<section class="card check">
  <h2>{copy.domains.checkTitle} <small>{copy.domains.checkHint}</small></h2>
  <form on:submit|preventDefault={check}>
    <input placeholder={copy.domains.checkPlaceholder} bind:value={checkDomain} required />
    <button type="submit" class="primary" disabled={!checkDomain.trim()}>
      {copy.domains.checkButton}
    </button>
  </form>
  {#if checkError}
    <p class="check-result error">{checkError}</p>
  {:else if checkResult}
    <p class="check-result" class:blocked={checkResult.verdict === 'blocked'}>
      <span class="domain">{checkResult.domain}</span> — {describeCheck(checkResult)}
    </p>
  {/if}
</section>

<section class="columns">
  <div class="card">
    <h2>{copy.domains.pardonsTitle} <small>{copy.domains.pardonsHint}</small></h2>
    <form on:submit|preventDefault={addPardon}>
      <input placeholder={copy.domains.addPlaceholder} bind:value={newPardon} required />
      <button type="submit" class="primary" disabled={!newPardon.trim()}>Add</button>
    </form>
    {#if allowlist.length === 0}
      <p class="empty">{copy.domains.pardonsEmpty}</p>
    {:else}
      <ul>
        {#each allowlist as d (d)}
          <li>
            <span class="domain">{d}</span>
            <button class="row-action" title="remove from allowlist" on:click={() => removePardon(d)}>
              Remove
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <div class="card">
    <h2>{copy.domains.sentencesTitle} <small>{copy.domains.sentencesHint}</small></h2>
    <form on:submit|preventDefault={addSentence}>
      <input placeholder={copy.domains.addPlaceholder} bind:value={newSentence} required />
      <button type="submit" class="primary" disabled={!newSentence.trim()}>Add</button>
    </form>
    {#if denylist.length === 0}
      <p class="empty">{copy.domains.sentencesEmpty}</p>
    {:else}
      <ul>
        {#each denylist as d (d)}
          <li>
            <span class="domain">{d}</span>
            <button class="row-action" title="remove from denylist" on:click={() => removeSentence(d)}>
              Remove
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</section>

<style>
  h1 small,
  h2 small {
    color: var(--text-dim);
    font-size: 0.78rem;
    margin-left: 0.5rem;
    letter-spacing: 0;
  }

  form {
    display: flex;
    gap: 0.6rem;
    margin-bottom: 0.9rem;
  }

  form input {
    flex: 1;
    max-width: 22rem;
  }

  .check-result {
    margin: 0.75rem 0 0;
  }

  .check-result.blocked {
    color: var(--blocked);
  }

  .check-result.error {
    color: var(--blocked);
  }

  .domain {
    font-family: var(--font-mono);
  }

  .columns {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(20rem, 1fr));
    gap: 1.25rem;
    margin-top: 1.25rem;
  }

  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.35rem 0;
    border-bottom: 1px solid var(--border);
    font-size: 0.85rem;
  }

  li:last-child {
    border-bottom: none;
  }

  .empty {
    color: var(--text-dim);
    font-style: italic;
  }

  .row-action {
    padding: 0.1rem 0.6rem;
    font-size: 0.75rem;
  }
</style>
