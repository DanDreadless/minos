<script lang="ts">
  // Privacy: the tracking companies the whole network talks to.
  import HelpLink from '../lib/components/HelpLink.svelte';
  import PrivacyReport from '../lib/components/PrivacyReport.svelte';
  import { copy } from '../lib/copy';
</script>

<h1>{copy.privacy.title}<HelpLink section="privacy" /></h1>
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
