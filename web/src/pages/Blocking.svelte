<script lang="ts">
  import HelpLink from '../lib/components/HelpLink.svelte';
  import { copy } from '../lib/copy';
  import { route } from '../lib/router';
  import AllowBlock from './AllowBlock.svelte';
  import Blocklists from './Blocklists.svelte';

  // Everything that decides what is blocked, in one place: the subscribed
  // lists, what you always allow, and what you always block. Each tab has
  // its own address (#/lists, #/allowed, #/blocked; the old #/domains opens
  // Allowed), so links and notification taps land on the right one.
</script>

<h1>
  {copy.nav.lists.label} <small>{copy.nav.lists.hint}</small><HelpLink
    section={$route === 'lists' ? 'blocklists' : 'allowblock'}
  />
</h1>

<div class="tabs" role="tablist">
  <a role="tab" href="#/lists" class:active={$route === 'lists'} aria-selected={$route === 'lists'}
    >{copy.blocking.tabLists}</a
  >
  <a role="tab" href="#/allowed" class:active={$route === 'allowed'} aria-selected={$route === 'allowed'}
    >{copy.blocking.tabAllowed}</a
  >
  <a role="tab" href="#/blocked" class:active={$route === 'blocked'} aria-selected={$route === 'blocked'}
    >{copy.blocking.tabBlocked}</a
  >
</div>

{#if $route === 'allowed'}
  <AllowBlock mode="allow" />
{:else if $route === 'blocked'}
  <AllowBlock mode="block" />
{:else}
  <Blocklists />
{/if}

<style>
  h1 small {
    color: var(--text-dim);
    font-size: 0.85rem;
    margin-left: 0.5rem;
  }

  .tabs {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
    margin-bottom: 1.1rem;
    border-bottom: 1px solid var(--border);
  }

  .tabs a {
    padding: 0.45rem 0.8rem;
    color: var(--text-dim);
    text-decoration: none;
    border-bottom: 2px solid transparent;
  }

  .tabs a:hover {
    color: var(--text);
  }

  .tabs a.active {
    color: var(--accent);
    border-bottom-color: var(--accent);
  }
</style>
