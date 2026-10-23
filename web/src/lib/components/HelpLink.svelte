<script lang="ts">
  import { copy } from '../copy';

  // A small "?" that opens the Help section for whatever it sits beside.
  export let section: string;
</script>

<a class="help" href="#/help?s={section}" title={copy.help.linkTitle} aria-label={copy.help.linkTitle}>?</a>

<style>
  .help {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 1.15rem;
    height: 1.15rem;
    margin-left: 0.5rem;
    border: 1px solid var(--border);
    border-radius: 50%;
    font-size: 0.72rem;
    font-weight: normal;
    color: var(--text-dim);
    text-decoration: none;
    vertical-align: middle;
    letter-spacing: 0;
  }

  .help:hover,
  .help:focus-visible {
    border-color: var(--accent);
    color: var(--accent);
  }
</style>
