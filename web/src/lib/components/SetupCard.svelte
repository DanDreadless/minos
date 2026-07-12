<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../api';
  import { copy } from '../copy';

  // Dismissal is permanent and local to the browser: once hidden, the card
  // never reappears, even if traffic later drops back to zero.
  const DISMISS_KEY = 'minos.setupDismissed';
  let dismissed = localStorage.getItem(DISMISS_KEY) === '1';
  let listen = '';

  function dismiss(): void {
    localStorage.setItem(DISMISS_KEY, '1');
    dismissed = true;
  }

  onMount(async () => {
    if (dismissed) return;
    try {
      listen = (await api.getConfig()).dns.listen;
    } catch {
      // The checklist still reads fine without the address.
    }
  });
</script>

{#if !dismissed}
  <section class="card setup">
    <div class="head">
      <h2>{copy.setup.title} <small>{copy.setup.hint}</small></h2>
      <button class="dismiss" title={copy.setup.dismissTitle} on:click={dismiss}>
        {copy.setup.dismiss}
      </button>
    </div>
    <ol>
      <li>
        {copy.setup.step1(listen)}
        <a
          href="https://github.com/DanDreadless/minos/blob/main/docs/getting-started.md"
          target="_blank"
          rel="noreferrer">{copy.setup.step1Link}</a
        >
      </li>
      <li>{copy.setup.step2} <a href="#/lists">{copy.setup.step2Link}</a></li>
      <li>{copy.setup.step3} <a href="#/settings">{copy.setup.step3Link}</a></li>
      <li>{copy.setup.step4} <a href="#/settings">{copy.setup.step4Link}</a></li>
    </ol>
  </section>
{/if}

<style>
  .setup {
    margin-bottom: 1.25rem;
    border-left: 3px solid var(--accent);
  }

  .head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.6rem;
  }

  .head h2 {
    margin: 0;
  }

  .head h2 small {
    color: var(--text-dim);
    font-size: 0.75rem;
    margin-left: 0.5rem;
  }

  .dismiss {
    font-size: 0.78rem;
    padding: 0.1rem 0.6rem;
    white-space: nowrap;
  }

  ol {
    margin: 0.8rem 0 0.2rem;
    padding-left: 1.4rem;
    display: grid;
    gap: 0.45rem;
    font-size: 0.9rem;
  }

  a {
    color: var(--accent);
  }
</style>
