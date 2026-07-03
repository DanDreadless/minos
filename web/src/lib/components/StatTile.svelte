<script lang="ts">
  export let value: string;
  export let label: string;
  export let hint: string;
  export let tone: 'default' | 'blocked' = 'default';
</script>

<div class="stat">
  <span class="stat-value" class:blocked={tone === 'blocked'}>{value}</span>
  <span class="stat-label" title={hint}>{label}</span>
  <span class="stat-hint">{hint}</span>
</div>

<style>
  .stat {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 1rem 1.25rem;
    display: flex;
    flex-direction: column;
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
