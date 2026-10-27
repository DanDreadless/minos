<script lang="ts">
  import HelpLink from '../lib/components/HelpLink.svelte';
  import { copy } from '../lib/copy';
  import { route } from '../lib/router';
  import AllowBlock from './AllowBlock.svelte';
  import Blocklists from './Blocklists.svelte';
  import Privacy from './Privacy.svelte';

  // Everything that decides what is blocked, in one place: the subscribed
  // lists, what you always allow, what you always block, and the tracking
  // companies your devices talk to (with a block by company). Each tab has
  // its own address (#/lists, #/allowed, #/blocked, #/privacy; the old
  // #/domains opens Allowed), so links and notification taps land on the
  // right one.
</script>

<h1>
  {copy.nav.lists.label} <small>{copy.nav.lists.hint}</small><HelpLink
    section={$route === 'lists' ? 'blocklists' : $route === 'privacy' ? 'privacy' : 'allowblock'}
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
  <a role="tab" href="#/privacy" class:active={$route === 'privacy'} aria-selected={$route === 'privacy'}
    >{copy.blocking.tabPrivacy}</a
  >
</div>

{#if $route === 'allowed'}
  <AllowBlock mode="allow" />
{:else if $route === 'blocked'}
  <AllowBlock mode="block" />
{:else if $route === 'privacy'}
  <Privacy />
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
