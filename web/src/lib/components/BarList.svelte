<script lang="ts">
  export let items: { label: string; count: number; sub?: string; href?: string }[];
  export let tone: 'accent' | 'blocked' = 'accent';
  export let empty: string;

  $: max = Math.max(1, ...items.map((i) => i.count));
</script>

{#if items.length === 0}
  <p class="empty">{empty}</p>
{:else}
  <ol>
    {#each items as item (item.label)}
      <li>
        <svelte:element
          this={item.href ? 'a' : 'div'}
          href={item.href ?? undefined}
          class="row"
          class:link={item.href}
        >
          <span class="label" title={item.label}>{item.label}</span>
          {#if item.sub}<span class="sub">{item.sub}</span>{/if}
          <span class="count">{item.count.toLocaleString()}</span>
        </svelte:element>
        <div class="track">
          <div
            class="bar"
            class:blocked={tone === 'blocked'}
            style="width: {(item.count / max) * 100}%"
          ></div>
        </div>
      </li>
    {/each}
  </ol>
{/if}

<style>
  ol {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  li {
    margin-bottom: 0.55rem;
  }

  .row {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
    font-size: 0.82rem;
  }

  a.row {
    text-decoration: none;
    color: inherit;
  }

  a.row.link:hover .label {
    color: var(--accent);
    text-decoration: underline;
  }

  .label {
    font-family: var(--font-mono);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .sub {
    color: var(--text-dim);
    font-size: 0.72rem;
  }

  .count {
    margin-left: auto;
    font-variant-numeric: tabular-nums;
    color: var(--text-dim);
  }

  .track {
    height: 3px;
    background: var(--bg-hover);
    border-radius: 2px;
    margin-top: 0.2rem;
  }

  .bar {
    height: 100%;
    background: var(--accent);
    border-radius: 2px;
  }

  .bar.blocked {
    background: var(--blocked);
  }

  .empty {
    color: var(--text-dim);
    font-style: italic;
  }
</style>
