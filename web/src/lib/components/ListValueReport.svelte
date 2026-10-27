<script lang="ts">
  // Blocking → Blocklists: what each blocklist adds that the others don't —
  // overlap, memory, and lookups only it would have blocked. Loaded on
  // request, since it reads a month of blocked lookups.
  import { api, type ListValue, type ListValueReport } from '../api';
  import { copy } from '../copy';
  import { fmtBytes } from '../format';
  import { docketHref } from '../router';
  import { notifyError } from '../toast';

  const c = copy.lists.value;
  let days = 30;
  let report: ListValueReport | null = null;
  let loading = false;

  async function load(): Promise<void> {
    loading = true;
    try {
      report = await api.listValue(days);
    } catch (e) {
      notifyError(e);
    } finally {
      loading = false;
    }
  }

  function pct(part: number, whole: number): number {
    return whole > 0 ? Math.round((100 * part) / whole) : 0;
  }

  // Share is pct for display, never rounding a real part to 0% or a
  // shortfall to 100%.
  function share(part: number, whole: number): string {
    const p = pct(part, whole);
    if (part > 0 && p === 0) return c.under1;
    if (part < whole && p === 100) return c.over99;
    return `${p}%`;
  }

  // The list that covers the most of this one, as a share of its rules.
  function topCover(v: ListValue): { list: string; pct: number } | null {
    const top = v.covered_by[0];
    return top ? { list: top.list, pct: pct(top.rules, v.rules) } : null;
  }

  // Verdict is the one-line judgement, from the lookups first: a list that
  // caught things no other would have is earning its keep whatever its
  // overlap.
  function verdict(v: ListValue, d: number): { text: string; tone: 'good' | 'warn' | 'dim' } {
    const cover = topCover(v);
    if (v.only_blocks > 0) return { text: c.earns(v.only_blocks, d), tone: 'good' };
    if (v.blocks === 0) {
      return { text: c.unused(d) + (cover && cover.pct >= 80 ? ' ' + c.consider(fmtBytes(v.freed_bytes)) : ''), tone: 'dim' };
    }
    return {
      text: c.redundant(d) + (cover && cover.pct >= 80 ? ' ' + c.consider(fmtBytes(v.freed_bytes)) : ''),
      tone: 'warn',
    };
  }
</script>

<section class="card value">
  <h2>{c.title} <small>{c.hint}</small></h2>
  <p class="intro">{c.intro}</p>
  <div class="controls">
    <label>
      {c.period}
      <select bind:value={days} on:change={() => report && load()}>
        <option value={7}>{c.days(7)}</option>
        <option value={30}>{c.days(30)}</option>
        <option value={90}>{c.days(90)}</option>
      </select>
    </label>
    <button on:click={load} disabled={loading}>{loading ? c.loading : report ? c.refresh : c.run}</button>
  </div>

  {#if report}
    {#if report.lists.length === 0}
      <p class="note">{c.needTwo}</p>
    {:else}
      <p class="note">{c.summary(report.blocked_lookups, report.days)}</p>
      {#each report.lists as v, i (i)}
        {@const cover = topCover(v)}
        {@const judged = verdict(v, report.days)}
        <div class="row">
          <div class="name">{v.list}</div>
          <p class={'verdict ' + judged.tone}>{judged.text}</p>
          <ul class="facts">
            <li>
              {c.rules(v.rules, share(v.unique_rules, v.rules))}
              {#if cover}{c.coveredBy(cover.list, share(v.covered_by[0].rules, v.rules))}{/if}
            </li>
            <li>
              {c.blocks(v.blocks)}
              {#if v.only_blocks > 0}
                {c.onlyBlocks(v.only_blocks)}
                {#each v.only_examples as ex, j (j)}{j > 0 ? ', ' : ' '}<a
                    href={docketHref({ qname: ex.qname, verdict: 'blocked' })}>{ex.qname}</a
                  >{/each}
              {/if}
            </li>
            <li>{c.freed(fmtBytes(v.freed_bytes))}</li>
          </ul>
        </div>
      {/each}
      <p class="note">{c.footnote}{report.names_truncated ? ' ' + c.truncated : ''}</p>
    {/if}
  {/if}
</section>

<style>
  .intro,
  .note {
    color: var(--text-dim);
    font-size: 0.85rem;
  }

  .controls {
    display: flex;
    gap: 0.75rem;
    align-items: center;
    flex-wrap: wrap;
    margin-bottom: 0.6rem;
  }

  .controls label {
    display: inline-flex;
    gap: 0.4rem;
    align-items: center;
    font-size: 0.85rem;
    color: var(--text-dim);
  }

  .row {
    border-top: 1px solid var(--border);
    padding: 0.7rem 0;
  }

  .name {
    font-weight: 600;
  }

  .verdict {
    margin: 0.2rem 0 0.3rem;
  }

  .verdict.good {
    color: var(--allowed);
  }

  .verdict.warn {
    color: var(--accent);
  }

  .verdict.dim {
    color: var(--text-dim);
  }

  .facts {
    margin: 0;
    padding-left: 1.1rem;
    font-size: 0.85rem;
    color: var(--text-dim);
  }

  .facts a {
    color: var(--accent);
    overflow-wrap: anywhere;
  }
</style>
