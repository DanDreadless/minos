<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, type ConfigView, type ListStatus, type Status, type UpstreamHealth } from '../api';
  import { copy } from '../copy';
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
  let timer: ReturnType<typeof setInterval> | null = null;

  async function load(): Promise<void> {
    // Each source is optional: a failure just leaves its signals out.
    const [l, u, c] = await Promise.allSettled([api.lists(), api.upstreams(), api.getConfig()]);
    if (l.status === 'fulfilled') lists = l.value;
    if (u.status === 'fulfilled') upstreams = u.value;
    if (c.status === 'fulfilled') cfg = c.value;
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

{#if items.length}
  <section class="card attention" aria-label={copy.attention.title}>
    <h2>{copy.attention.title}</h2>
    <ul>
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
</style>
