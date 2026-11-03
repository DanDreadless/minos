<script lang="ts">
  import HelpLink from '../lib/components/HelpLink.svelte';
  import { copy } from '../lib/copy';
  import { route } from '../lib/router';
  import Blocklists from './Blocklists.svelte';
  import Privacy from './Privacy.svelte';
  import Rules from './Rules.svelte';
  import Services from './Services.svelte';

  // DNS Filter: everything that decides what is blocked, in one place — the
  // subscribed lists, your own rules (for everyone, a group or a device),
  // whole services, and the tracking companies your devices talk to. Each
  // tab has its own address (#/lists, #/rules, #/services, #/privacy; the
  // old #/allowed, #/blocked and #/domains open Rules), so links and
  // notification taps land on the right one.

  const tabs: { route: string; href: string; label: string }[] = [
    { route: 'lists', href: '#/lists', label: copy.blocking.tabLists },
    { route: 'rules', href: '#/rules', label: copy.blocking.tabRules },
    { route: 'services', href: '#/services', label: copy.blocking.tabServices },
    { route: 'privacy', href: '#/privacy', label: copy.blocking.tabPrivacy },
  ];
</script>

<h1>
  {copy.nav.lists.label} <small>{copy.nav.lists.hint}</small><HelpLink
    section={$route === 'lists' ? 'blocklists' : $route === 'privacy' ? 'privacy' : $route === 'services' ? 'services' : 'rules'}
  />
</h1>

<div class="tabs" role="tablist">
  {#each tabs as t (t.route)}
    <a role="tab" href={t.href} class:active={$route === t.route} aria-selected={$route === t.route}>{t.label}</a>
  {/each}
</div>

{#if $route === 'rules'}
  <Rules />
{:else if $route === 'services'}
  <Services />
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
