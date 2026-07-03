<script lang="ts">
  import type { Status } from '../api';
  import { copy } from '../copy';
  import { hrefFor, route, type Route } from '../router';

  export let status: Status | null;

  const items: { key: Route; label: string; hint: string }[] = [
    { key: 'dashboard', ...copy.nav.dashboard },
    { key: 'querylog', ...copy.nav.querylog },
    { key: 'lists', ...copy.nav.lists },
    { key: 'domains', ...copy.nav.domains },
    { key: 'settings', ...copy.nav.settings },
  ];
</script>

<nav>
  <div class="meander" aria-hidden="true"></div>
  <a class="brand" href="#/">
    <span class="name">{copy.appName}</span>
    <span class="tagline">{copy.tagline}</span>
  </a>
  <ul>
    {#each items as item (item.key)}
      <li>
        <a href={hrefFor[item.key]} class:active={$route === item.key} title={item.hint}>
          <span class="label">{item.label}</span>
          <span class="hint">{item.hint}</span>
        </a>
      </li>
    {/each}
  </ul>
  <div class="foot">
    {#if status}
      {#if status.paused}
        <span class="pill paused" title={copy.recess.actionHint}>{copy.recess.headerPill}</span>
      {:else}
        <span class="pill active-pill">blocking active</span>
      {/if}
      <span class="version">v{status.version}</span>
    {/if}
  </div>
</nav>

<style>
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

  .brand .name {
    display: block;
    font-family: var(--font-display);
    font-size: 1.45rem;
    letter-spacing: 0.28em;
    text-transform: uppercase;
    color: var(--accent);
  }

  .brand .tagline {
    display: block;
    margin-top: 0.2rem;
    font-family: var(--font-display);
    font-style: italic;
    font-size: 0.78rem;
    color: var(--text-dim);
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
    align-items: center;
    gap: 0.6rem;
    font-size: 0.75rem;
  }

  .pill {
    padding: 0.1rem 0.55rem;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--text-dim);
  }

  .pill.paused {
    border-color: var(--accent);
    color: var(--accent);
  }

  .version {
    margin-left: auto;
    color: var(--text-dim);
  }

  @media (max-width: 800px) {
    nav {
      border-right: none;
      border-bottom: 1px solid var(--border);
    }

    ul {
      display: flex;
      flex-wrap: wrap;
      margin: 0;
    }

    li a {
      border-left: none;
      border-bottom: 3px solid transparent;
      padding: 0.5rem 0.8rem;
    }

    li a.active {
      border-bottom-color: var(--accent);
    }

    li a .hint {
      display: none;
    }

    .brand {
      border-bottom: none;
      padding-bottom: 0.3rem;
    }

    .brand .tagline {
      display: none;
    }

    .foot {
      display: none;
    }
  }
</style>
