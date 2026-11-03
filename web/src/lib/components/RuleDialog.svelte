<script lang="ts">
  import { tick } from 'svelte';
  import { api, type Device, type Group, type RuleScope } from '../api';
  import { copy } from '../copy';
  import { pendingRule, type RuleDialogRequest } from '../ruledialog';
  import { deviceKey, deviceLabel, findByKey, findDevice } from '../rules';
  import { notify, notifyError } from '../toast';

  // Where the rule goes: this device, its group, everyone, or one picked
  // from the list ("other").
  type Choice = 'device' | 'group' | 'everyone' | 'other';

  let devices: Device[] = [];
  let groups: Group[] = [];
  let loaded = false;
  let choice: Choice = 'everyone';
  let domain = '';
  // Other is "group:<name>" or "device:<key>".
  let other = '';
  let saving = false;
  let firstRadio: HTMLInputElement;
  let openFor: RuleDialogRequest | null = null;

  $: if ($pendingRule && $pendingRule !== openFor) void open($pendingRule);

  async function open(req: RuleDialogRequest): Promise<void> {
    openFor = req;
    loaded = false;
    domain = req.domains[0] ?? '';
    other = '';
    try {
      [devices, groups] = await Promise.all([api.clients(), api.groups()]);
    } catch {
      devices = [];
      groups = [];
    }
    const dev = req.client ? findDevice(devices, req.client) : undefined;
    const inGroup = !!dev && dev.group !== '' && dev.group !== 'default';
    // Start from the rule that decided the lookup, else the narrowest
    // choice: a mistake then affects one device, not the whole network.
    if (req.decidedBy?.startsWith('group:') && inGroup) choice = 'group';
    else if (req.client) choice = 'device';
    else choice = 'everyone';
    loaded = true;
    await tick();
    firstRadio?.focus();
  }

  $: req = $pendingRule;
  $: dev = req?.client ? findDevice(devices, req.client) : undefined;
  $: devName = deviceLabel(dev, req?.client ?? '');
  $: group = dev && dev.group && dev.group !== 'default' ? groups.find((g) => g.name === dev.group) : undefined;

  // The target the current choice resolves to.
  $: target = resolveTarget(choice, other, dev, req?.client ?? '', group);
  function resolveTarget(
    c: Choice,
    o: string,
    d: Device | undefined,
    client: string,
    g: Group | undefined,
  ): { scope: RuleScope; target?: string } | null {
    if (c === 'everyone') return { scope: 'everyone' };
    if (c === 'device') return { scope: 'device', target: d ? deviceKey(d) : client };
    if (c === 'group') return g ? { scope: 'group', target: g.name } : null;
    if (o.startsWith('group:')) return { scope: 'group', target: o.slice(6) };
    if (o.startsWith('device:')) return { scope: 'device', target: o.slice(7) };
    return null;
  }

  // Notes that keep the choice honest: a more specific rule would still
  // win, the rule cannot follow the device, or the group ignores rules.
  $: notes = notesFor(target, req, dev, devices, groups);
  function notesFor(
    t: { scope: RuleScope; target?: string } | null,
    r: RuleDialogRequest | null,
    d: Device | undefined,
    all: Device[],
    gs: Group[],
  ): string[] {
    if (!t || !r) return [];
    const out: string[] = [];
    const by = r.decidedBy ?? '';
    if (by.startsWith('device:') && t.scope !== 'device') out.push(copy.rules.dialogDeviceWins);
    else if (by.startsWith('group:') && t.scope === 'everyone') out.push(copy.rules.dialogGroupWins);
    if (t.scope === 'device') {
      const td = findByKey(all, t.target ?? '') ?? (t.target === r.client ? d : undefined);
      if (!td?.mac && t.target) out.push(copy.rules.dialogNoMAC(t.target));
      const g = td && gs.find((x) => x.name === td.group);
      if (g && g.mode !== 'filter') out.push(copy.rules.dialogGroupMode(g.name, g.mode));
    }
    if (t.scope === 'group') {
      const g = gs.find((x) => x.name === t.target);
      if (g && g.mode !== 'filter') out.push(copy.rules.dialogGroupMode(g.name, g.mode));
      else if (g?.schedule) out.push(copy.rules.dialogGroupSchedule(g.name));
    }
    return out;
  }

  // Devices for the "a different device" list, named, busiest first.
  $: pickable = [...devices].sort((a, b) => deviceLabel(a).localeCompare(deviceLabel(b)));

  async function save(): Promise<void> {
    if (!req || !target || !domain) return;
    saving = true;
    try {
      await api.addRule({ action: req.action, kind: 'domain', value: domain, ...target }, req.client);
      const who =
        target.scope === 'everyone'
          ? copy.rules.forEveryone
          : target.scope === 'group'
            ? copy.rules.forGroup(target.target ?? '')
            : copy.rules.forDevice(deviceLabel(findByKey(devices, target.target ?? ''), target.target ?? devName));
      notify(req.action === 'allow' ? copy.rules.allowedDone(domain, who) : copy.rules.blockedDone(domain, who));
      req.resolve(true);
    } catch (e) {
      notifyError(e);
    } finally {
      saving = false;
    }
  }

  function close(): void {
    req?.resolve(false);
  }

  function onKey(ev: KeyboardEvent): void {
    if ($pendingRule && ev.key === 'Escape') close();
  }
</script>

<!-- Following a link (or the back button) leaves the dialog behind: close it. -->
<svelte:window on:keydown={onKey} on:hashchange={close} />

{#if req}
  <div class="backdrop" role="presentation" on:click={close}></div>
  <div class="rule card" role="dialog" aria-modal="true" aria-labelledby="rule-title">
    <form on:submit|preventDefault={save}>
      <h2 id="rule-title">{req.action === 'allow' ? copy.rules.dialogAllowTitle : copy.rules.dialogBlockTitle}</h2>

      {#if req.domains.length > 1}
        <fieldset>
          <legend>{copy.rules.dialogWhichName}</legend>
          {#each req.domains as d, i (d)}
            <label class="choice">
              <input type="radio" bind:group={domain} value={d} />
              <span class="mono">{d}</span>
              {#if i > 0}<small>{copy.rules.dialogMatchedRule}</small>{/if}
            </label>
          {/each}
        </fieldset>
      {:else}
        <p class="mono name">{domain}</p>
      {/if}
      <p class="note">{copy.rules.dialogSubdomains}</p>

      <fieldset>
        <legend>{copy.rules.dialogWhere}</legend>
        {#if !loaded}
          <p class="note">{copy.rules.loading}</p>
        {:else}
          {#if req.client}
            <label class="choice">
              <input type="radio" bind:this={firstRadio} bind:group={choice} value="device" />
              <span>{copy.rules.onlyDevice(devName)}</span>
            </label>
          {/if}
          {#if group}
            <label class="choice">
              <input type="radio" bind:group={choice} value="group" />
              <span>{copy.rules.everyoneInGroup(group.name)}</span>
            </label>
          {/if}
          <label class="choice">
            <input type="radio" bind:group={choice} value="everyone" />
            <span>{copy.rules.everyoneOnNetwork}</span>
          </label>
          <label class="choice">
            <input type="radio" bind:group={choice} value="other" />
            <span>{copy.rules.somewhereElse}</span>
          </label>
          {#if choice === 'other'}
            <select bind:value={other} required aria-label={copy.rules.somewhereElse}>
              <option value="" disabled>{copy.rules.pickOne}</option>
              {#if groups.length}
                <optgroup label={copy.rules.groupsHeading}>
                  {#each groups as g (g.name)}
                    <option value="group:{g.name}">{g.name}</option>
                  {/each}
                </optgroup>
              {/if}
              <optgroup label={copy.rules.devicesHeading}>
                {#each pickable as d, i (i)}
                  <option value="device:{deviceKey(d)}">{deviceLabel(d)}{deviceLabel(d) !== d.ip ? ` (${d.ip})` : ''}</option>
                {/each}
              </optgroup>
            </select>
          {/if}
        {/if}
      </fieldset>

      {#each notes as n, i (i)}
        <p class="warn">{n}</p>
      {/each}

      <div class="buttons">
        <button type="button" class="cancel" on:click={close}>{copy.confirm.cancel}</button>
        <button type="submit" class={req.action === 'allow' ? 'primary' : 'danger-solid'} disabled={saving || !target || !loaded}
          >{req.action === 'allow' ? copy.allow.action : copy.block.action}</button
        >
      </div>
    </form>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.55);
    z-index: 90;
  }

  .rule {
    position: fixed;
    z-index: 91;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: min(30rem, calc(100vw - 2rem));
    max-height: calc(100vh - 2rem);
    overflow-y: auto;
    box-shadow: 0 12px 40px rgba(0, 0, 0, 0.5);
  }

  h2 {
    margin: 0 0 0.8rem;
  }

  fieldset {
    border: none;
    margin: 0 0 0.9rem;
    padding: 0;
  }

  legend {
    font-size: 0.8rem;
    color: var(--text-dim);
    margin-bottom: 0.4rem;
  }

  .choice {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
    padding: 0.3rem 0;
    cursor: pointer;
  }

  .choice small {
    color: var(--text-dim);
  }

  .mono {
    font-family: var(--font-mono);
    overflow-wrap: anywhere;
  }

  .name {
    margin: 0 0 0.3rem;
  }

  select {
    width: 100%;
    margin-top: 0.3rem;
  }

  .note {
    color: var(--text-dim);
    font-size: 0.8rem;
    margin: 0 0 0.9rem;
  }

  .warn {
    font-size: 0.82rem;
    color: var(--accent);
    margin: 0 0 0.7rem;
    line-height: 1.4;
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
