<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { copy } from '../copy';

  // A fold-out guide to rule syntax — plain domains, * wildcards and
  // /regex/ — under the forms that add domain rules. Pressing an example
  // puts it in the form's field, ready to edit.

  const dispatch = createEventDispatcher<{ pick: string }>();
</script>

<details class="syntax">
  <summary>{copy.rules.syntaxTitle}</summary>
  <dl>
    <div class="head" aria-hidden="true">
      <span>{copy.rules.syntaxWrite}</span><span>{copy.rules.syntaxMatches}</span>
    </div>
    {#each copy.rules.syntaxExamples as ex, i (i)}
      <div class="row">
        <dt>
          <button
            type="button"
            class="example"
            title={copy.rules.syntaxUse(ex.rule)}
            on:click={() => dispatch('pick', ex.rule)}><code>{ex.rule}</code></button
          >
        </dt>
        <dd>{ex.text}</dd>
      </div>
    {/each}
  </dl>
  <ul>
    {#each copy.rules.syntaxNotes as note, i (i)}
      <li>{note}</li>
    {/each}
  </ul>
</details>

<style>
  .syntax {
    margin: 0.8rem 0;
    font-size: 0.82rem;
    max-width: 46rem;
  }

  summary {
    cursor: pointer;
    color: var(--accent);
    width: fit-content;
  }

  dl {
    margin: 0.6rem 0 0.4rem;
  }

  .head,
  .row {
    display: grid;
    grid-template-columns: 11rem minmax(0, 1fr);
    gap: 0 0.8rem;
    padding: 0.4rem 0;
    border-bottom: 1px solid var(--border);
  }

  .head {
    color: var(--text-dim);
    font-size: 0.75rem;
    padding-top: 0;
  }

  dt,
  dd {
    margin: 0;
    line-height: 1.4;
  }

  button.example {
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    text-align: left;
  }

  button.example code {
    color: var(--accent);
  }

  button.example:hover code {
    text-decoration: underline;
  }

  ul {
    margin: 0.5rem 0 0;
    padding-left: 1.1rem;
    color: var(--text-dim);
    line-height: 1.45;
  }

  li + li {
    margin-top: 0.25rem;
  }

  @media (max-width: 640px) {
    .head {
      display: none;
    }

    .row {
      grid-template-columns: minmax(0, 1fr);
      gap: 0.15rem;
    }
  }
</style>
