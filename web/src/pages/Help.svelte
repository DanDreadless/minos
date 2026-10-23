<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { copy } from '../lib/copy';
  import { docs } from '../lib/docs';
  import { currentParams } from '../lib/router';

  // The user guide, rendered from lib/docs.ts (the same source that
  // generates docs/user-guide.md). #/help?s=<section> opens at a section.
  let filter = '';

  $: shown = filterDocs(filter);

  function filterDocs(q: string): typeof docs {
    const needle = q.trim().toLowerCase();
    if (!needle) return docs;
    return docs.filter((s) => JSON.stringify(s).toLowerCase().includes(needle));
  }

  async function scrollToSection(): Promise<void> {
    const id = currentParams().s;
    if (!id) return;
    await tick();
    document.getElementById(`help-${id}`)?.scrollIntoView({ block: 'start' });
  }

  function onHashChange(): void {
    if (location.hash.startsWith('#/help')) void scrollToSection();
  }

  onMount(() => {
    void scrollToSection();
    window.addEventListener('hashchange', onHashChange);
  });
  onDestroy(() => window.removeEventListener('hashchange', onHashChange));
</script>

<h1>{copy.help.title} <small>{copy.help.subtitle}</small></h1>

<div class="layout">
  <nav class="toc" aria-label={copy.help.contents}>
    <input type="search" placeholder={copy.help.searchPlaceholder} bind:value={filter} aria-label={copy.help.searchPlaceholder} />
    <ul>
      {#each shown as s (s.id)}
        <li><a href="#/help?s={s.id}">{s.title}</a></li>
      {/each}
    </ul>
  </nav>

  <div class="content">
    {#if shown.length === 0}
      <p class="empty">{copy.help.noMatches}</p>
    {/if}
    {#each shown as s (s.id)}
      <section class="card" id="help-{s.id}">
        <h2>
          {s.title}
          {#if s.page}<a class="open" href={s.page}>{copy.help.openPage}</a>{/if}
        </h2>
        <p>{s.summary}</p>
        {#if s.howto?.length}
          <ul class="howto">
            {#each s.howto as h (h)}<li>{h}</li>{/each}
          </ul>
        {/if}
        {#if s.tasks?.length}
          {#each s.tasks as t (t.q)}
            <p class="q">{t.q}</p>
            <p class="a">{t.a}</p>
          {/each}
        {/if}
        {#if s.settings?.length}
          <h3>{copy.help.settings}</h3>
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>{copy.help.colSetting}</th>
                  <th>{copy.help.colDefault}</th>
                  <th>{copy.help.colEffect}</th>
                  <th>{copy.help.colApplies}</th>
                </tr>
              </thead>
              <tbody>
                {#each s.settings as st (st.key)}
                  <tr>
                    <td>
                      <strong>{st.label}</strong>
                      <code>{st.key}</code>
                      <span class="where">{st.where}</span>
                    </td>
                    <td>{st.default ?? '—'}</td>
                    <td>
                      {st.effect}
                      {#if st.fields?.length}
                        <dl>
                          {#each st.fields as f (f.key)}
                            <dt><code>{f.key.slice(st.key.length + 1)}</code></dt>
                            <dd>{f.effect}</dd>
                          {/each}
                        </dl>
                      {/if}
                    </td>
                    <td>
                      <span class="applies {st.applies}" title={copy.help.appliesHint[st.applies]}
                        >{copy.help.applies[st.applies]}</span
                      >
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </section>
    {/each}
  </div>
</div>

<style>
  h1 small {
    color: var(--text-dim);
    font-size: 0.85rem;
    margin-left: 0.5rem;
  }

  .layout {
    display: grid;
    grid-template-columns: 14rem 1fr;
    gap: 1.25rem;
    align-items: start;
  }

  .toc {
    position: sticky;
    top: 0;
  }

  .toc input {
    width: 100%;
    margin-bottom: 0.6rem;
  }

  .toc ul {
    list-style: none;
    margin: 0;
    padding: 0;
    font-size: 0.85rem;
  }

  .toc li {
    padding: 0.2rem 0;
  }

  .content {
    min-width: 0;
  }

  section + section {
    margin-top: 1.25rem;
  }

  section {
    scroll-margin-top: 1rem;
  }

  h2 {
    display: flex;
    align-items: baseline;
    gap: 0.75rem;
  }

  .open {
    font-size: 0.78rem;
    font-weight: normal;
  }

  p {
    line-height: 1.5;
  }

  .howto li {
    margin-bottom: 0.35rem;
    line-height: 1.45;
  }

  .q {
    font-weight: 600;
    margin-bottom: 0.2rem;
  }

  .a {
    margin-top: 0;
  }

  h3 {
    margin-top: 1.1rem;
    font-size: 0.95rem;
  }

  table {
    font-family: inherit;
    font-size: 0.82rem;
  }

  td {
    white-space: normal;
    vertical-align: top;
    max-width: none;
  }

  td strong {
    display: block;
  }

  td code {
    display: block;
    font-size: 0.75rem;
    color: var(--text-dim);
  }

  .where {
    display: block;
    font-size: 0.72rem;
    color: var(--text-dim);
  }

  dl {
    margin: 0.4rem 0 0;
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.15rem 0.75rem;
  }

  dd {
    margin: 0;
  }

  dt code {
    display: inline;
  }

  .applies {
    white-space: nowrap;
    font-size: 0.72rem;
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 0 0.45rem;
  }

  .applies.live {
    color: var(--allowed);
  }

  .applies.file {
    color: var(--text-dim);
  }

  .empty {
    color: var(--text-dim);
    font-style: italic;
  }

  @media (max-width: 760px) {
    .layout {
      grid-template-columns: 1fr;
    }

    .toc {
      position: static;
    }
  }
</style>
