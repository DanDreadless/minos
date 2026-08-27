// Curated catalog of known-good blocklists for the Codex page — the
// resolver-picker pattern applied to the harder decision.
//
// Every URL below is verified by fetching it and compiling it through the
// real list pipeline; the size hints are those runs' rule counts, rounded.
// Each URL pins the exact raw variant the project publishes. Prefer a
// project's plain-domains variant where one exists — it downloads and
// compiles smallest — but follow the publisher: Hagezi deleted its
// `domains/` tree in August 2026, so those entries now use the AdBlock
// variants (verified 2026-08-14, zero skipped rules).
//
// A dead URL here is a broken one-click subscribe for every user, and a
// list that still answers 200 while its content or format changed is
// worse — the subscription looks healthy and protects nothing. So
// internal/lists/catalog_test.go (build tag `catalog`) compiles every
// entry below through the real parser weekly in CI, asserting it parses,
// skips nothing, and lands near the recorded size.
//
// Sizes are compiled rule counts, not the publisher's marketing figure,
// and were re-measured 2026-08-27.
//
// To add or change an entry: add it here, then run
//   go test -tags catalog -timeout 20m -v ./internal/lists
// and record the rule count it reports.

export type BlocklistTier = 'balanced' | 'strict' | 'security';

export interface BlocklistPreset {
  id: string;
  label: string;
  note: string;
  size: string;
  tier: BlocklistTier;
  list: { name: string; url: string; format: 'hosts' | 'plain' | 'adblock' };
}

export const blocklistTiers: BlocklistTier[] = ['balanced', 'strict', 'security'];

export const blocklistPresets: BlocklistPreset[] = [
  {
    id: 'hagezi-multi',
    label: 'Hagezi Multi Normal',
    note: 'ads, tracking & telemetry — the sweet spot, very low breakage',
    size: '≈190k domains',
    tier: 'balanced',
    list: {
      name: 'Hagezi Multi',
      url: 'https://raw.githubusercontent.com/hagezi/dns-blocklists/main/adblock/multi.txt',
      format: 'adblock',
    },
  },
  {
    id: 'oisd-small',
    label: 'OISD Small',
    note: 'the essentials, built to never break anything',
    size: '≈59k domains',
    tier: 'balanced',
    list: {
      name: 'OISD Small',
      url: 'https://small.oisd.nl/domainswild2',
      format: 'plain',
    },
  },
  {
    id: 'hagezi-pro',
    label: 'Hagezi Multi Pro',
    note: 'broader coverage than Normal; expect the occasional pardon',
    size: '≈226k domains',
    tier: 'strict',
    list: {
      name: 'Hagezi Pro',
      url: 'https://raw.githubusercontent.com/hagezi/dns-blocklists/main/adblock/pro.txt',
      format: 'adblock',
    },
  },
  {
    id: 'oisd-big',
    label: 'OISD Big',
    note: 'the full OISD net — wide, still breakage-shy',
    size: '≈267k domains',
    tier: 'strict',
    list: {
      name: 'OISD Big',
      url: 'https://big.oisd.nl/domainswild2',
      format: 'plain',
    },
  },
  {
    id: 'stevenblack',
    label: 'StevenBlack',
    note: 'the classic unified hosts file — the default on a fresh install',
    size: '≈81k domains',
    tier: 'strict',
    list: {
      name: 'StevenBlack',
      url: 'https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts',
      format: 'hosts',
    },
  },
  {
    id: 'hagezi-tif',
    label: 'Hagezi Threat Intelligence',
    note: 'malware, phishing & scam domains — the medium cut; the full feed is ~2M domains and would fill a Pi\'s memory budget on its own',
    size: '≈328k domains',
    tier: 'security',
    list: {
      name: 'Hagezi TIF',
      url: 'https://raw.githubusercontent.com/hagezi/dns-blocklists/main/adblock/tif.medium.txt',
      format: 'adblock',
    },
  },
  {
    id: 'urlhaus',
    label: 'URLhaus',
    note: 'active malware-distribution hosts from abuse.ch — small and sharp',
    size: '<1k domains',
    tier: 'security',
    list: {
      name: 'URLhaus',
      url: 'https://urlhaus.abuse.ch/downloads/hostfile/',
      format: 'hosts',
    },
  },
];
