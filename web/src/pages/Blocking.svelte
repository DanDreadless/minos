<script lang="ts">
  import HelpLink from '../lib/components/HelpLink.svelte';
  import { copy } from '../lib/copy';
  import { route } from '../lib/router';
  import AllowBlock from './AllowBlock.svelte';
  import Blocklists from './Blocklists.svelte';

  // Everything that decides what is blocked, in one place: the subscribed
  // lists, and your own allowed/blocked domains and services. Each tab keeps
  // its own address (#/lists, #/domains), so existing links and
  // notification taps still land on the right one.
</script>

<h1>
  {copy.nav.lists.label} <small>{copy.nav.lists.hint}</small><HelpLink
    section={$route === 'domains' ? 'allowblock' : 'blocklists'}
  />
</h1>

<div class="tabs" role="tablist">
  <a role="tab" href="#/lists" class:active={$route === 'lists'} aria-selected={$route === 'lists'}
    >{copy.blocking.tabLists}</a
  >
  <a role="tab" href="#/domains" class:active={$route === 'domains'} aria-selected={$route === 'domains'}
    >{copy.blocking.tabRules}</a
  >
</div>

{#if $route === 'domains'}
  <AllowBlock />
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
