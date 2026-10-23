<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, openStream, type LogEntry } from '../lib/api';
  import HelpLink from '../lib/components/HelpLink.svelte';
  import { copy } from '../lib/copy';
  import { fmtLogTime, fmtLogTimeFull } from '../lib/format';
  import { currentParams } from '../lib/router';
  import { notify, notifyError } from '../lib/toast';
  import {
    RANGES,
    chips,
    emptyFilter,
    fromParams,
    hasFilter,
    matches,
    parseInput,
    removeChip,
    sinceMs,
    toParams,
    type QueryFilter,
    type RangeKey,
    type Verdict,
  } from '../lib/querysyntax';

  const MAX_ROWS = 500;
  const HISTORY_PAGE = 200;

  // The page has two modes. Live: no filter at all — the ring buffer plus
  // the WebSocket stream, newest first. Results: any filter (including a
  // time range) — server-side history across the whole retained log, "load
  // older" pages, and new matching queries joining at the top.
  let filter: QueryFilter = emptyFilter();
  let text = ''; // the search box: free text, plus any key:value still being typed
  let searchEl: HTMLInputElement;

  let live: LogEntry[] = [];
  let paused = false;
  let held: LogEntry[] = []; // live arrivals while paused

  let results: LogEntry[] = [];
  let resultsDone = false;
  // resultsStatus says what the rows actually are, so a partial view is
  // never presented as a complete one. 'failed' (timeout or error) shows
  // the ring's matches with the reason.
  let resultsStatus: 'idle' | 'loading' | 'done' | 'failed' = 'idle';
  let resultsError = '';
  let loadingOlder = false;
  // One controller per search: a new search aborts the one in flight, so
  // the newest query always wins.
  let ctrl: AbortController | null = null;

  let listNames: string[] = [];
  let ws: WebSocket | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let debounce: ReturnType<typeof setTimeout> | null = null;
  let destroyed = false;
  let connected = false;
  let exporting = '';

  $: searching = hasFilter(filter);
  // Results mode falls back to filtering the live ring when the server has
  // nothing (ephemeral mode, or the search failed).
  $: displayed = !searching
    ? live
    : results.length
      ? results
      : resultsStatus === 'loading'
        ? []
        : live.filter((e) => matches(filter, e));
  $: recentOnly = searching && results.length === 0 && displayed.length > 0;
  // Verdict and list have their own controls; everything else is a chip.
  $: activeChips = chips(filter, verdictLabel).filter((c) => c.key !== 'verdict' && c.key !== 'list');
  $: rangeLabel = RANGES.find((r) => r.key === filter.range)?.label ?? '';

  function verdictLabel(v: Verdict): string {
    return v === 'blocked'
      ? copy.querylog.filterBlocked
      : v === 'allowed'
        ? copy.querylog.filterAllowed
        : v === 'would_block'
          ? copy.querylog.filterWouldBlock
          : copy.querylog.filterAll;
  }

  // requestParams is the filter as the server takes it: `range` becomes a
  // `since` timestamp at request time.
  function requestParams(f: QueryFilter): URLSearchParams {
    const sp = toParams(f);
    sp.delete('range');
    const since = sinceMs(f);
    if (since !== undefined) sp.set('since', String(Math.floor(since)));
    return sp;
  }

  // The hash mirrors the filter, so a search can be bookmarked, shared or
  // reloaded. replaceState: filtering is not navigation, and it does not
  // fire hashchange.
  function hashFor(f: QueryFilter): string {
    const qs = toParams(f).toString();
    return qs ? `#/querylog?${qs}` : '#/querylog';
  }

  function syncHash(): void {
    const h = hashFor(filter);
    if (location.hash !== h) window.history.replaceState(null, '', h);
  }

  function setFilter(f: QueryFilter): void {
    filter = f;
    if (debounce) clearTimeout(debounce);
    syncHash();
    refresh();
  }

  async function fetchResults(reset: boolean): Promise<void> {
    if (reset) {
      ctrl?.abort();
    } else if (loadingOlder || resultsStatus === 'loading') {
      return;
    }
    const c = new AbortController();
    ctrl = c;
    if (reset) resultsStatus = 'loading';
    else loadingOlder = true;
    resultsError = '';
    try {
      const sp = requestParams(filter);
      if (!reset && results.length) {
        sp.set('before', String(new Date(results[results.length - 1].time).getTime()));
      }
      sp.set('limit', String(HISTORY_PAGE));
      const rows = await api.querylogSearch(sp, c.signal);
      results = reset ? rows : [...results, ...rows];
      resultsDone = rows.length < HISTORY_PAGE;
      resultsStatus = 'done';
    } catch (e) {
      if (c.signal.aborted) return; // superseded; the newer search owns the state
      resultsStatus = 'failed';
      resultsError = e instanceof Error ? e.message : String(e);
    } finally {
      if (ctrl === c) {
        ctrl = null;
        loadingOlder = false;
      }
    }
  }

  function refresh(): void {
    results = [];
    resultsDone = false;
    if (hasFilter(filter)) {
      void fetchResults(true);
    } else {
      ctrl?.abort();
      ctrl = null;
      loadingOlder = false;
      resultsStatus = 'idle';
    }
  }

  // Typing: a finished key:value token (followed by a space) becomes a chip
  // straight away; free text searches after a short pause.
  function onInput(): void {
    const { filter: f, rest } = parseInput(text, filter, false);
    const chipsChanged =
      JSON.stringify({ ...f, q: '' }) !== JSON.stringify({ ...filter, q: '' });
    if (chipsChanged) text = rest;
    const qChanged = f.q !== filter.q;
    filter = f;
    if (!chipsChanged && !qChanged) return;
    if (debounce) clearTimeout(debounce);
    debounce = setTimeout(
      () => {
        syncHash();
        refresh();
      },
      chipsChanged ? 0 : 250,
    );
  }

  function onSearchKey(ev: KeyboardEvent): void {
    if (ev.key === 'Enter') {
      const { filter: f, rest } = parseInput(text, filter, true);
      text = rest;
      setFilter(f);
    } else if (ev.key === 'Escape') {
      if (text) {
        text = '';
        setFilter({ ...filter, q: '' });
      } else {
        searchEl.blur();
      }
    } else if (ev.key === 'Backspace' && text === '' && activeChips.length) {
      setFilter(removeChip(filter, activeChips[activeChips.length - 1].key));
    }
  }

  // "/" focuses the search from anywhere on the page, as on most log viewers.
  function onWindowKey(ev: KeyboardEvent): void {
    if (ev.key !== '/' || ev.ctrlKey || ev.metaKey || ev.altKey) return;
    const t = ev.target as HTMLElement | null;
    if (t && (t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName))) return;
    ev.preventDefault();
    searchEl?.focus();
  }

  // A link from elsewhere (a dashboard drill-down) while this page is open.
  function onHashChange(): void {
    if (!location.hash.startsWith('#/querylog')) return;
    if (location.hash === hashFor(filter)) return;
    filter = fromParams(currentParams());
    text = filter.q;
    refresh();
  }

  function narrow(change: Partial<QueryFilter>): void {
    setFilter({ ...filter, ...change });
  }

  function setRange(r: RangeKey): void {
    narrow({ range: r });
  }

  function setVerdict(v: Verdict): void {
    narrow({ verdict: v });
  }

  function clearAll(): void {
    text = '';
    setFilter(emptyFilter());
  }

  function togglePause(): void {
    paused = !paused;
    if (!paused) {
      if (held.length) live = [...held, ...live].slice(0, MAX_ROWS);
      held = [];
      if (hasFilter(filter)) refresh(); // pick up what matched meanwhile
    }
  }

  function connect(): void {
    if (destroyed) return;
    ws = openStream(
      (e) => {
        connected = true;
        if (paused) {
          held = [e, ...held].slice(0, MAX_ROWS);
          return;
        }
        live = [e, ...live].slice(0, MAX_ROWS);
        // Keep a results view live: new matches join at the top.
        if (hasFilter(filter) && results.length && matches(filter, e)) results = [e, ...results];
      },
      () => {
        ws = null;
        connected = false;
        if (!destroyed) reconnectTimer = setTimeout(connect, 2000);
      },
    );
    ws.onopen = () => (connected = true);
  }

  async function exportAs(format: 'csv' | 'json'): Promise<void> {
    exporting = format;
    try {
      const sp = requestParams(filter);
      sp.set('format', format);
      const { blob, filename } = await api.querylogExport(sp);
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 10_000);
    } catch (e) {
      notifyError(e);
    } finally {
      exporting = '';
    }
  }

  async function pardon(domain: string): Promise<void> {
    try {
      await api.pardon(domain);
      notify(copy.allow.done(domain));
    } catch (e) {
      notifyError(e);
    }
  }

  async function sentence(domain: string): Promise<void> {
    try {
      await api.sentence(domain);
      notify(copy.block.done(domain));
    } catch (e) {
      notifyError(e);
    }
  }

  async function copyDomain(domain: string): Promise<void> {
    try {
      await navigator.clipboard.writeText(domain);
      notify(copy.querylog.copied(domain));
    } catch (e) {
      notifyError(e);
    }
  }

  async function check(domain: string): Promise<void> {
    try {
      const r = await api.check(domain);
      notify(copy.querylog.checkResult(domain, r.verdict === 'blocked', r.list, r.rule));
    } catch (e) {
      notifyError(e);
    }
  }

  onMount(async () => {
    // Honour a deep link, e.g. #/querylog?verdict=blocked&client=192.168.1.5
    filter = fromParams(currentParams());
    text = filter.q;
    window.addEventListener('keydown', onWindowKey);
    window.addEventListener('hashchange', onHashChange);

    // Dropdown options are cosmetic — degrade silently if they fail to load.
    void api
      .querylogLists()
      .then((names) => {
        // A deep-linked name outside the window still shows as the active option.
        if (filter.list && !names.includes(filter.list)) names = [filter.list, ...names];
        listNames = names;
      })
      .catch(() => {});

    if (hasFilter(filter)) void fetchResults(true);
    try {
      live = await api.querylog(MAX_ROWS);
    } catch (e) {
      notifyError(e);
    }
    connect();
  });

  onDestroy(() => {
    destroyed = true;
    window.removeEventListener('keydown', onWindowKey);
    window.removeEventListener('hashchange', onHashChange);
    if (reconnectTimer) clearTimeout(reconnectTimer);
    if (debounce) clearTimeout(debounce);
    ctrl?.abort();
    ws?.close();
  });
</script>

<h1>
  {copy.querylog.title} <small>{copy.querylog.subtitle}</small><HelpLink section="querylog" />
</h1>

<div class="toolbar">
  <div class="searchbox">
    {#each activeChips as c (c.key)}
      <span class="chip" title={c.title}>
        <span class="chip-label">{c.label}</span>
        {c.value}
        <button
          class="chip-x"
          on:click={() => setFilter(removeChip(filter, c.key))}
          aria-label={copy.querylog.removeFilter(c.label)}>×</button
        >
      </span>
    {/each}
    <input
      type="search"
      bind:this={searchEl}
      bind:value={text}
      on:input={onInput}
      on:keydown={onSearchKey}
      placeholder={activeChips.length ? '' : copy.querylog.searchBoxPlaceholder}
      aria-label={copy.querylog.searchBoxLabel}
      spellcheck="false"
      autocomplete="off"
    />
  </div>

  <div class="controls">
    <div class="segmented" role="group" aria-label="Result">
      {#each ['all', 'blocked', 'allowed', 'would_block'] as v (v)}
        <button
          class:active={filter.verdict === v}
          aria-pressed={filter.verdict === v}
          on:click={() => setVerdict(v as Verdict)}>{verdictLabel(v as Verdict)}</button
        >
      {/each}
    </div>
    <select
      value={filter.list}
      on:change={(e) => narrow({ list: e.currentTarget.value })}
      title={copy.querylog.filterListTitle}
    >
      <option value="">{copy.querylog.filterListAll}</option>
      {#each listNames as name (name)}
        <option value={name}>{name}</option>
      {/each}
    </select>
    <select
      value={filter.range}
      on:change={(e) => setRange(e.currentTarget.value as RangeKey)}
      aria-label={copy.querylog.rangeLabel}
      title={copy.querylog.rangeLabel}
    >
      {#each RANGES as r (r.key)}
        <option value={r.key}>{r.label}</option>
      {/each}
    </select>
  </div>
</div>

<div class="status">
  {#if !searching}
    {#if paused}
      <span class="mode paused">{copy.querylog.modePaused(held.length)}</span>
    {:else if connected}
      <span class="mode live">{copy.querylog.modeLive}</span>
      <span class="detail">{copy.querylog.modeLiveDetail(live.length)}</span>
    {:else}
      <span class="detail">{copy.querylog.modeReconnecting}</span>
    {/if}
  {:else}
    <span class="mode results">{copy.querylog.modeResults}</span>
    <span class="detail">
      {#if resultsStatus === 'loading'}
        {copy.querylog.searching}
      {:else if recentOnly}
        {displayed.length} · {copy.querylog.scopeRecent}
      {:else}
        {copy.querylog.resultsDetail(displayed.length, !resultsDone && results.length > 0, rangeLabel)}
      {/if}
    </span>
    <button class="link" on:click={clearAll}>{copy.querylog.clearAll}</button>
  {/if}
  <span class="spacer"></span>
  <button
    class="small"
    class:active={paused}
    on:click={togglePause}
    title={copy.querylog.pauseHint}>{paused ? copy.querylog.resume : copy.querylog.pause}</button
  >
  <span class="export">
    {copy.querylog.exportLabel}
    {#each ['csv', 'json'] as fmt (fmt)}
      <button
        class="small"
        disabled={exporting !== ''}
        title={copy.querylog.exportHint(fmt)}
        on:click={() => exportAs(fmt === 'csv' ? 'csv' : 'json')}
        >{exporting === fmt ? copy.querylog.exporting : fmt.toUpperCase()}</button
      >
    {/each}
  </span>
</div>

<details class="syntax">
  <summary>{copy.querylog.syntaxTitle}</summary>
  <dl>
    {#each copy.querylog.syntax as [term, meaning] (term)}
      <dt><code>{term}</code></dt>
      <dd>{meaning}</dd>
    {/each}
  </dl>
  <p>{copy.querylog.syntaxKeys}</p>
</details>

{#if searching && resultsStatus === 'failed'}
  <p class="search-failed" role="alert">
    {copy.querylog.searchFailed(resultsError, recentOnly)}
  </p>
{/if}

{#if displayed.length === 0}
  <p class="empty">
    {searching
      ? resultsStatus === 'loading'
        ? copy.querylog.searching
        : resultsStatus === 'failed'
          ? copy.querylog.noRecentMatches
          : copy.querylog.noMatches
      : copy.querylog.empty}
  </p>
{:else}
  <div class="table-wrap">
    <table class="stack">
      <thead>
        <tr>
          <th>Time</th>
          <th>Device</th>
          <th>Domain</th>
          <th>Type</th>
          <th>Result</th>
          <th title={copy.querylog.columnSourceHint}>{copy.querylog.columnSource}</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <!-- Deliberately unkeyed: entries have no unique id, and a client
             retrying a blocked query can produce two rows identical in
             time+qname+client+qtype — a keyed each then throws at runtime
             and freezes the whole page mid-render (field bug). Rows are
             stateless text, so positional patching is correct and cheap. -->
        {#each displayed as e}
          <tr>
            <td class="when" title={fmtLogTimeFull(e.time)}>{fmtLogTime(e.time)}</td>
            <td>
              <button class="cell" title={copy.querylog.filterHint} on:click={() => narrow({ clients: [e.client] })}
                >{e.client}</button
              >
            </td>
            <td class="domain primary" title={e.qname}>
              <button class="cell" title={copy.querylog.filterHint} on:click={() => narrow({ domain: e.qname })}
                >{e.qname}</button
              >
            </td>
            <td>
              <button class="cell" title={copy.querylog.filterHint} on:click={() => narrow({ qtype: e.qtype })}
                >{e.qtype}</button
              >
            </td>
            {#if e.verdict === 'blocked'}
              <td><span class="badge blocked">{copy.querylog.verdictBlocked}</span></td>
              <td title="rule: {e.rule}">
                {#if e.list}
                  <button class="cell" on:click={() => narrow({ list: e.list })}>{e.list}</button>
                {/if}
              </td>
            {:else}
              <td>
                <span class="badge allowed">{copy.querylog.verdictAllowed}</span>
                {#if e.audit_list}
                  <span
                    class="would-badge"
                    title={copy.querylog.wouldBlockTitle(e.audit_list, e.audit_rule ?? '')}
                  >
                    {copy.querylog.wouldBlockBadge}
                  </span>
                {/if}
              </td>
              <td>
                {#if e.list}
                  <button class="cell" title="rule: {e.rule}" on:click={() => narrow({ list: e.list })}
                    >{e.list}</button
                  >
                {:else if e.upstream}
                  <button class="cell" title={copy.querylog.filterHint} on:click={() => narrow({ upstream: e.upstream })}
                    >{e.upstream}</button
                  >
                {/if}
              </td>
            {/if}
            <td class="actions end">
              {#if e.verdict === 'blocked'}
                <button class="row-action" title={copy.allow.actionHint} on:click={() => pardon(e.rule ?? e.qname)}
                  >{copy.allow.action}</button
                >
              {:else}
                <button class="row-action subtle" title={copy.block.actionHint} on:click={() => sentence(e.qname)}
                  >{copy.block.action}</button
                >
              {/if}
              <button class="row-action subtle" title={copy.querylog.checkHint} on:click={() => check(e.qname)}
                >{copy.querylog.checkAction}</button
              >
              <button class="row-action subtle" title={copy.querylog.copyHint} on:click={() => copyDomain(e.qname)}
                >{copy.querylog.copyAction}</button
              >
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if searching && results.length > 0 && !resultsDone}
    <div class="load-older">
      <button on:click={() => fetchResults(false)} disabled={loadingOlder}>
        {loadingOlder ? copy.querylog.loadingOlder : copy.querylog.loadOlder}
      </button>
    </div>
  {/if}
{/if}

<style>
  h1 small {
    color: var(--text-dim);
    font-size: 0.85rem;
    margin-left: 0.5rem;
  }

  .toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.6rem;
    margin-bottom: 0.5rem;
    flex: none;
  }

  /* A token field: chips sit inside the box, before the text. */
  .searchbox {
    flex: 1 1 22rem;
    min-width: 0;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.3rem;
    padding: 0.2rem 0.4rem;
    border: 1px solid var(--border);
    border-radius: 4px;
    background: var(--bg-raised);
  }

  .searchbox:focus-within {
    border-color: var(--accent);
  }

  .searchbox input {
    flex: 1;
    min-width: 10rem;
    border: none;
    background: transparent;
    outline: none;
    padding: 0.2rem 0.2rem;
  }

  .controls {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
  }

  .segmented {
    display: inline-flex;
  }

  .segmented button {
    border-radius: 0;
    margin-left: -1px;
    padding: 0.3rem 0.7rem;
    font-size: 0.8rem;
  }

  .segmented button:first-child {
    border-radius: 4px 0 0 4px;
    margin-left: 0;
  }

  .segmented button:last-child {
    border-radius: 0 4px 4px 0;
  }

  .segmented button.active,
  button.small.active {
    border-color: var(--accent);
    color: var(--accent);
    position: relative;
  }

  .chip {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    font-size: 0.78rem;
    color: var(--text);
    background: var(--surface-2, rgba(127, 127, 127, 0.15));
    border: 1px solid var(--border, rgba(127, 127, 127, 0.3));
    border-radius: 999px;
    padding: 0.1rem 0.3rem 0.1rem 0.6rem;
    white-space: nowrap;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .chip-label {
    color: var(--text-dim);
  }

  .chip-x {
    all: unset;
    cursor: pointer;
    line-height: 1;
    font-size: 1rem;
    padding: 0 0.25rem;
    color: var(--text-dim);
  }

  .chip-x:hover {
    color: var(--accent);
  }

  .status {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.8rem;
    color: var(--text-dim);
    margin-bottom: 0.4rem;
    flex: none;
  }

  .mode {
    font-size: 0.65rem;
    letter-spacing: 0.15em;
    text-transform: uppercase;
    border: 1px solid;
    border-radius: 999px;
    padding: 0.05rem 0.5rem;
  }

  .mode.live {
    color: var(--allowed);
  }

  .mode.paused,
  .mode.results {
    color: var(--accent);
  }

  .spacer {
    flex: 1;
  }

  .export {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
  }

  button.small {
    padding: 0.1rem 0.55rem;
    font-size: 0.75rem;
  }

  button.link {
    all: unset;
    cursor: pointer;
    color: var(--accent);
    font-size: 0.8rem;
  }

  button.link:hover {
    text-decoration: underline;
  }

  .syntax {
    flex: none;
    font-size: 0.8rem;
    color: var(--text-dim);
    margin-bottom: 0.6rem;
  }

  .syntax summary {
    cursor: pointer;
    width: fit-content;
  }

  .syntax dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.2rem 1rem;
    margin: 0.5rem 0 0.3rem;
  }

  .syntax dd {
    margin: 0;
  }

  .syntax p {
    margin: 0.3rem 0 0;
  }

  /* Click-to-filter: cell values look like text until hovered. */
  button.cell {
    all: unset;
    cursor: pointer;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  button.cell:hover,
  button.cell:focus-visible {
    color: var(--accent);
    text-decoration: underline dotted;
  }

  .load-older {
    text-align: center;
    padding: 0.6rem 0;
    flex: none;
  }

  .load-older button {
    font-size: 0.8rem;
  }

  .empty {
    color: var(--text-dim);
    font-style: italic;
  }

  .search-failed {
    flex: none;
    margin: 0 0 0.75rem;
    padding: 0.5rem 0.75rem;
    border: 1px solid var(--blocked);
    border-radius: 4px;
    color: var(--blocked);
    font-size: 0.85rem;
  }

  td.when {
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }

  td.actions {
    text-align: right;
  }

  /* Fill the height main.fill hands us and scroll the rows internally, so
     the page itself never grows as queries stream in. */
  .table-wrap {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }

  /* Keep the column headers visible while the body scrolls. th already
     carries an opaque background in app.css. */
  thead th {
    position: sticky;
    top: 0;
    z-index: 1;
  }

  .would-badge {
    display: inline-block;
    margin-left: 0.35rem;
    padding: 0 0.4rem;
    border: 1px solid var(--audit, #c9962e);
    border-radius: 0.6rem;
    color: var(--audit, #c9962e);
    font-size: 0.68rem;
    line-height: 1.4;
    white-space: nowrap;
    vertical-align: middle;
    cursor: help;
  }

  .row-action {
    padding: 0.1rem 0.6rem;
    font-size: 0.78rem;
  }

  .row-action.subtle {
    opacity: 0;
  }

  tr:hover .row-action.subtle,
  .row-action.subtle:focus-visible {
    opacity: 1;
  }

  @media (hover: none) {
    .row-action.subtle {
      opacity: 1;
    }
  }
</style>
