<script lang="ts">
  export let value: string;
  export let label: string;
  export let hint: string;
  export let tone: 'default' | 'blocked' = 'default';
  // When set, the tile becomes a link (used for dashboard drill-downs).
  export let href: string | null = null;
</script>

<svelte:element
  this={href ? 'a' : 'div'}
  href={href ?? undefined}
  class="stat"
  class:link={href}
>
  <span class="stat-value" class:blocked={tone === 'blocked'}>{value}</span>
  <span class="stat-label" title={hint}>{label}</span>
  <span class="stat-hint">{hint}</span>
</svelte:element>

<style>
  .stat {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 1rem 1.25rem;
    display: flex;
    flex-direction: column;
  }

  a.stat {
    text-decoration: none;
    color: inherit;
    transition:
      border-color 0.12s,
      background 0.12s;
  }

  a.stat.link:hover {
    border-color: var(--accent);
    background: var(--bg-hover);
  }

  .stat-value {
    font-size: 1.8rem;
    font-variant-numeric: tabular-nums;
    font-family: var(--font-display);
  }

  .stat-value.blocked {
    color: var(--blocked);
  }

  .stat-label {
    letter-spacing: 0.08em;
    margin-top: 0.3rem;
    font-family: var(--font-display);
  }

  .stat-hint {
    color: var(--text-dim);
    font-size: 0.76rem;
  }
</style>
