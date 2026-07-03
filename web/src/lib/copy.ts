// All themed ("lore layer") copy lives here so it can be audited, localised,
// or toned down in one place. Rules (see CLAUDE.md): restraint — one themed
// word per screen; every themed label carries a plain explanation; error
// messages are always plain and never themed.
export const copy = {
  appName: 'Minos',
  tagline: 'Every query gets judged.',

  dashboard: {
    title: 'The Tribunal', // plain: dashboard
    subtitle: 'dashboard',
  },

  stats: {
    judged: 'Judged',
    judgedHint: 'total queries handled',
    condemned: 'Condemned',
    condemnedHint: 'blocked queries',
    blockRate: 'Block rate',
    blockRateHint: 'share of queries blocked',
    rules: 'Rules',
    rulesHint: 'compiled block rules',
  },

  docket: {
    title: 'The Docket', // plain: live query log
    subtitle: 'live query log',
    empty: 'No queries yet. Point a device at this resolver and its fate appears here.',
    verdictBlocked: 'condemned', // API value: "blocked"
    verdictAllowed: 'passed', //    API value: "allowed"
  },

  pardon: {
    action: 'Pardon',
    actionHint: 'always allow this domain',
    done: (domain: string) => `${domain} pardoned — always allowed from now on.`,
  },

  recess: {
    action: 'Recess',
    actionHint: 'pause blocking',
    resume: 'Resume blocking',
    active: (until: string) => `Blocking paused until ${until}.`,
    activeIndefinite: 'Blocking paused.',
  },

  token: {
    prompt: 'This instance requires an API token.',
    placeholder: 'API token',
    submit: 'Unlock',
  },
};
