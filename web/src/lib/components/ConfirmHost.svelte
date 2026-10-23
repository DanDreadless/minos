<script lang="ts">
  import { tick } from 'svelte';
  import { pendingConfirm } from '../confirm';
  import { copy } from '../copy';

  let okButton: HTMLButtonElement;

  // Focus the safe choice for destructive actions, the action otherwise.
  $: if ($pendingConfirm) void focusFirst($pendingConfirm.danger ?? false);

  async function focusFirst(danger: boolean): Promise<void> {
    await tick();
    const el = danger
      ? document.querySelector<HTMLButtonElement>('.confirm .cancel')
      : okButton;
    el?.focus();
  }

  function onKey(ev: KeyboardEvent): void {
    if ($pendingConfirm && ev.key === 'Escape') $pendingConfirm.resolve(false);
  }
</script>

<svelte:window on:keydown={onKey} />

{#if $pendingConfirm}
  <div class="backdrop" role="presentation" on:click={() => $pendingConfirm?.resolve(false)}></div>
  <div class="confirm card" role="alertdialog" aria-modal="true" aria-labelledby="confirm-msg">
    <p id="confirm-msg">{$pendingConfirm.message}</p>
    <div class="buttons">
      <button class="cancel" on:click={() => $pendingConfirm?.resolve(false)}
        >{$pendingConfirm.cancel ?? copy.confirm.cancel}</button
      >
      <button
        bind:this={okButton}
        class={$pendingConfirm.danger ? 'danger-solid' : 'primary'}
        on:click={() => $pendingConfirm?.resolve(true)}>{$pendingConfirm.confirm}</button
      >
    </div>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.55);
    z-index: 90;
  }

  .confirm {
    position: fixed;
    z-index: 91;
    top: 30%;
    left: 50%;
    transform: translate(-50%, -30%);
    width: min(28rem, calc(100vw - 2rem));
    box-shadow: 0 12px 40px rgba(0, 0, 0, 0.5);
  }

  p {
    margin: 0 0 1.1rem;
    line-height: 1.45;
  }

  .buttons {
    display: flex;
    justify-content: flex-end;
    gap: 0.6rem;
  }

  .danger-solid {
    border-color: var(--blocked);
    color: var(--blocked);
  }

  .danger-solid:hover,
  .danger-solid:focus-visible {
    background: var(--blocked);
    color: var(--bg);
  }
</style>
