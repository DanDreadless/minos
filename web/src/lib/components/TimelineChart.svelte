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
      title: `${new Date(b.time).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })} — ${b.total} queries, ${b.blocked} blocked`,
    };
  });
  // A few x-axis time labels, roughly every quarter of the window. The first
  // and last are anchored start/end so they don't get clipped at the edges.
  $: ticks =
    data.length > 4
      ? [0, 0.25, 0.5, 0.75, 1].map((f, idx, arr) => {
          const i = Math.min(data.length - 1, Math.round(f * (data.length - 1)));
          return {
            x: i * barW + barW / 2,
            anchor: idx === 0 ? 'start' : idx === arr.length - 1 ? 'end' : 'middle',
            label: new Date(data[i].time).toLocaleTimeString([], {
              hour: '2-digit',
              minute: '2-digit',
            }),
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
  {#each ticks as t (t.x)}
    <text x={t.x} y={H - 4} text-anchor={t.anchor} class="tick">{t.label}</text>
  {/each}
</svg>

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

  text.tick {
    fill: var(--text-dim);
    font-size: 10px;
    font-family: inherit;
  }
</style>
