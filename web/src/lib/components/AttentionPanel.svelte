<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, type ConfigView, type ListStatus, type Status, type Suggestion, type Threat, type UpstreamHealth } from '../api';
  import { copy } from '../copy';
  import { fmtClock } from '../format';
  import { docketHref } from '../router';
  import { notify, notifyError } from '../toast';
  import { bootOf, hasUnseen, logSeen } from '../logseen';

  // Every health signal Minos has, in one place, each with the link that
  // fixes it. Shows nothing at all when nothing needs attention — a calm
  // dashboard is the normal state, not an empty card.
  export let status: Status | null;
  export let diskLow = false;
  export let dnssecBlind = false;

  interface Item {
    severity: 'bad' | 'warn' | 'info';
    text: string;
    href: string;
    action: string;
  }

  let lists: ListStatus[] = [];
  let upstreams: UpstreamHealth[] = [];
  let cfg: ConfigView | null = null;
  let suggestions: Suggestion[] = [];
  let threats: Threat[] = [];
  let timer: ReturnType<typeof setInterval> | null = null;

  async function load(): Promise<void> {
    // Each source is optional: a failure just leaves its signals out.
    const [l, u, c, sg, th] = await Promise.allSettled([
      api.lists(),
      api.upstreams(),
      api.getConfig(),
      api.suggestions(),
      api.threats(),
    ]);
    if (th.status === 'fulfilled') threats = th.value;
    if (l.status === 'fulfilled') lists = l.value;
    if (u.status === 'fulfilled') upstreams = u.value;
    if (c.status === 'fulfilled') cfg = c.value;
    if (sg.status === 'fulfilled') suggestions = sg.value;
  }

  // The three answers to "something may be broken". Each removes the line.
  async function allow(s: Suggestion, minutes: number): Promise<void> {
    try {
      if (minutes) {
        const r = await api.allowFor(s.domain, minutes);
        notify(r.until ? copy.allow.doneFor(r.domain, fmtClock(r.until)) : copy.allow.alreadyAlways(r.domain));
      } else {
        await api.pardon(s.domain);
        notify(copy.allow.done(s.domain));
      }
      suggestions = suggestions.filter((x) => x.domain !== s.domain);
    } catch (e) {
      notifyError(e);
    }
  }

  async function threatFine(t: Threat): Promise<void> {
    try {
      await api.dismissThreat(t);
      threats = threats.filter((x) => x !== t);
    } catch (e) {
      notifyError(e);
    }
  }

  async function threatNever(t: Threat): Promise<void> {
    if (!t.domain) return;
    try {
      await api.ignoreThreatDomain(t.domain);
      threats = threats.filter((x) => x.domain !== t.domain);
    } catch (e) {
      notifyError(e);
    }
  }

  async function dismiss(s: Suggestion): Promise<void> {
    try {
      await api.dismissSuggestion(s.client, s.domain);
      suggestions = suggestions.filter((x) => x !== s);
    } catch (e) {
      notifyError(e);
    }
  }

  const BAD_LISTS = ['unavailable', 'retired', 'unauthorized'];
  const WARN_LISTS = ['stale', 'failing', 'suspicious'];

  $: items = build(status, lists, upstreams, cfg, diskLow, dnssecBlind, $logSeen);

  function build(
    st: Status | null,
    ls: ListStatus[],
    ups: UpstreamHealth[],
    c: ConfigView | null,
    disk: boolean,
    blind: boolean,
    seen: { seq: number; boot: number },
  ): Item[] {
    const out: Item[] = [];
    const a = copy.attention;
    if (st && !st.paused && st.rules === 0) out.push({ severity: 'bad', text: a.noRules, href: '#/lists', action: a.openLists });
    for (const l of ls.filter((x) => x.enabled && BAD_LISTS.includes(x.state)))
      out.push({ severity: 'bad', text: a.list(l.name, l.state), href: '#/lists', action: a.openLists });
    const warnLists = ls.filter((x) => x.enabled && WARN_LISTS.includes(x.state));
    if (warnLists.length)
      out.push({ severity: 'warn', text: a.listsFailing(warnLists.map((x) => x.name)), href: '#/lists', action: a.openLists });
    if (c) {
      const configured = new Set(c.dns.upstreams.map((u) => u.address));
      const mine = ups.filter((u) => configured.has(u.address));
      const failing = mine.filter((u) => u.state === 'failing');
      if (mine.length && failing.length === mine.length)
        out.push({ severity: 'bad', text: a.dnsDown, href: '#/settings?tab=dns', action: a.openResolvers });
      else if (failing.length)
        out.push({ severity: 'warn', text: a.upstreamsFailing(failing.map((u) => u.address)), href: '#/settings?tab=dns', action: a.openResolvers });
      if (!c.api.token_set && !c.api.password_set)
        out.push({ severity: 'info', text: a.noToken, href: '#/settings?tab=access', action: a.openAccess });
    }
    if (disk) out.push({ severity: 'bad', text: a.disk, href: '#/settings?tab=system', action: a.openSystem });
    if (blind) out.push({ severity: 'warn', text: a.dnssecBlind, href: '#/settings?tab=dns', action: a.openResolvers });
    if (st && hasUnseen(st.log_alert_seq ?? 0, bootOf(st.uptime_seconds), seen))
      out.push({ severity: 'warn', text: a.logs, href: '#/logs', action: a.openLogs });
    const rank = { bad: 0, warn: 1, info: 2 };
    return out.sort((x, y) => rank[x.severity] - rank[y.severity]);
  }

  onMount(() => {
    void load();
    timer = setInterval(load, 30000);
  });
  onDestroy(() => {
    if (timer) clearInterval(timer);
  });
</script>

{#if items.length || suggestions.length || threats.length}
  <section class="card attention" aria-label={copy.attention.title}>
    <h2>{copy.attention.title}</h2>
    <ul>
      {#each threats as t (t.kind + ' ' + t.client + ' ' + (t.domain ?? ''))}
        <li class="bad broken">
          <span class="mark" aria-hidden="true"></span>
          <span class="text">
            {copy.attention.threat(t.kind, t.device || t.client, t)}
            <a href={docketHref({ client: t.client, domain: t.kind === 'known_threat' ? undefined : t.domain, verdict: t.kind === 'known_threat' ? 'blocked' : undefined })}
              >{copy.attention.brokenLookups}</a
            >
          </span>
          <span class="fix">
            <button title={copy.attention.threatFineHint} on:click={() => threatFine(t)}>{copy.attention.threatFine}</button>
            {#if t.kind === 'tunnel' && t.domain}
              <button class="link" title={copy.attention.threatNeverHint} on:click={() => threatNever(t)}
                >{copy.attention.threatNever(t.domain)}</button
              >
            {/if}
          </span>
        </li>
      {/each}
      {#each suggestions as s (s.client + ' ' + s.domain)}
        <li class="warn broken">
          <span class="mark" aria-hidden="true"></span>
          <span class="text">
            {copy.attention.broken(s.device || s.client, s.domain, s.count, s.list)}
            <a href={docketHref({ client: s.client, domain: s.domain })}>{copy.attention.brokenLookups}</a>
          </span>
          <span class="fix">
            <button class="primary" title={copy.attention.brokenAllowHint} on:click={() => allow(s, 0)}
              >{copy.attention.brokenAllow}</button
            >
            <button title={copy.attention.brokenHourHint} on:click={() => allow(s, 60)}>{copy.attention.brokenHour}</button>
            <button class="link" title={copy.attention.brokenDismissHint} on:click={() => dismiss(s)}
              >{copy.attention.brokenDismiss}</button
            >
          </span>
        </li>
      {/each}
      {#each items as it (it.text)}
        <li class={it.severity}>
          <span class="mark" aria-hidden="true"></span>
          <span class="text">{it.text}</span>
          <a href={it.href}>{it.action}</a>
        </li>
      {/each}
    </ul>
  </section>
{/if}

<style>
  .attention {
    margin-bottom: 1.25rem;
  }

  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  li {
    display: flex;
    align-items: baseline;
    gap: 0.6rem;
    padding: 0.4rem 0;
    border-bottom: 1px solid var(--border);
    font-size: 0.88rem;
  }

  li:last-child {
    border-bottom: none;
  }

  .mark {
    flex: none;
    width: 0.55rem;
    height: 0.55rem;
    border-radius: 50%;
    background: var(--text-dim);
    transform: translateY(-0.05rem);
  }

  li.bad .mark {
    background: var(--blocked);
  }

  li.warn .mark {
    background: var(--audit, #c9962e);
  }

  .text {
    flex: 1;
  }

  li a {
    flex: none;
    white-space: nowrap;
    font-size: 0.82rem;
  }

  /* Dot, sentence, buttons; on a phone the buttons drop under the sentence
     and the dot stays beside it. */
  li.broken {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    align-items: center;
    column-gap: 0.6rem;
  }

  li.broken .text a {
    margin-left: 0.4rem;
  }

  .fix {
    display: inline-flex;
    gap: 0.4rem;
    align-items: center;
    flex: none;
  }

  .fix button {
    font-size: 0.8rem;
    padding: 0.25rem 0.6rem;
  }

  .fix .link {
    background: none;
    border: none;
    color: var(--text-dim);
    cursor: pointer;
  }

  @media (max-width: 640px) {
    li.broken {
      grid-template-columns: auto minmax(0, 1fr);
      row-gap: 0.45rem;
    }

    li.broken .fix {
      grid-column: 2;
    }
  }
</style>
