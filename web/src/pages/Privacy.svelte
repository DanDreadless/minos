<script lang="ts">
  // Blocking → Privacy: the tracking companies the whole network talks to.
  import PrivacyReport from '../lib/components/PrivacyReport.svelte';
  import { copy } from '../lib/copy';
</script>

<p class="intro">{copy.privacy.intro}</p>
<section class="card">
  <PrivacyReport />
</section>

<style>
  .intro {
    color: var(--text-dim);
    font-size: 0.9rem;
    margin: 0 0 1rem;
  }
</style>
