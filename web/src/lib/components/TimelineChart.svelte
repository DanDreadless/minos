<script lang="ts">
  import type { TimelineBucket } from '../api';

  export let data: TimelineBucket[];

  const W = 720;
  const H = 150;
  const PAD_BOTTOM = 18;

  $: maxTotal = Math.max(1, ...data.map((b) => b.total));
  $: barW = data.length > 0 ? W / data.length : W;
  $: bars = data.map((b, i) => {
    const totalH = (b.total / maxTotal) * (H - PAD_BOTTOM);
    const blockedH = (b.blocked / maxTotal) * (H - PAD_BOTTOM);
    return {
      x: i * barW,
      totalY: H - PAD_BOTTOM - totalH,
      totalH,
      blockedY: H - PAD_BOTTOM - blockedH,
      blockedH,
      title: `${label(b.time, daily)} — ${b.total} queries, ${b.blocked} blocked`,
    };
  });
  // Day-sized buckets (windows over a week) are labelled by date, the rest
  // by time of day.
  $: daily = data.length > 1 && new Date(data[1].time).getTime() - new Date(data[0].time).getTime() >= 86400e3;
  function label(iso: string, byDay: boolean): string {
    const d = new Date(iso);
    return byDay
      ? d.toLocaleDateString([], { day: 'numeric', month: 'short' })
      : d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }
  // A few x-axis time labels, roughly every quarter of the window. The first
  // and last are anchored start/end so they don't get clipped at the edges.
  $: ticks =
    data.length > 4
      ? [0, 0.25, 0.5, 0.75, 1].map((f, idx, arr) => {
          const i = Math.min(data.length - 1, Math.round(f * (data.length - 1)));
          return {
            x: i * barW + barW / 2,
            anchor: idx === 0 ? 'start' : idx === arr.length - 1 ? 'end' : 'middle',
            label: label(data[i].time, daily),
          };
        })
      : [];
</script>

<svg viewBox="0 0 {W} {H}" preserveAspectRatio="none" role="img" aria-label="query volume chart">
  {#each bars as b, i (i)}
    <g>
      <title>{b.title}</title>
      <rect x={b.x + 0.5} y={b.totalY} width={Math.max(0.5, barW - 1)} height={b.totalH} class="total" />
      <rect
        x={b.x + 0.5}
        y={b.blockedY}
        width={Math.max(0.5, barW - 1)}
        height={b.blockedH}
        class="blocked"
      />
    </g>
  {/each}
  <line x1="0" y1={H - PAD_BOTTOM} x2={W} y2={H - PAD_BOTTOM} class="axis" />
</svg>
<!-- Time labels are HTML under the chart, not SVG text: the chart stretches
     to any width (preserveAspectRatio none), which squashed SVG text to an
     unreadable sliver on a phone. -->
<div class="ticks" aria-hidden="true">
  {#each ticks as t (t.x)}
    <span class={t.anchor} style="left: {(t.x / W) * 100}%">{t.label}</span>
  {/each}
</div>

<style>
  svg {
    display: block;
    width: 100%;
    height: 170px;
  }

  rect.total {
    fill: var(--chart-total);
  }

  rect.blocked {
    fill: var(--blocked);
  }

  line.axis {
    stroke: var(--border);
    stroke-width: 1;
  }

  .ticks {
    position: relative;
    height: 1.1rem;
    margin-top: -16px;
    font-size: 0.7rem;
    color: var(--text-dim);
  }

  .ticks span {
    position: absolute;
    top: 0;
    white-space: nowrap;
    transform: translateX(-50%);
  }

  .ticks span.start {
    transform: none;
  }

  .ticks span.end {
    transform: translateX(-100%);
  }

</style>
