// Curated catalog of known-good blocklists for the Codex page — the
// resolver-picker pattern applied to the harder decision.
//
// Every URL below was verified on 2026-07-11 by subscribing it in a dev
// instance: fetched, parsed, and compiled through the real list pipeline
// with zero skipped rules (except StevenBlack's long-known 7). The size
// hints are that run's rule counts, rounded. Each URL pins the exact raw
// variant the project publishes; prefer a project's plain-domains variant —
// it downloads and compiles smallest.
//
// To add an entry: subscribe the exact URL in a dev instance, confirm it
// parses with nothing skipped, and record the rounded rule count here.

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
    size: '≈390k domains',
    tier: 'balanced',
    list: {
      name: 'Hagezi Multi',
      url: 'https://raw.githubusercontent.com/hagezi/dns-blocklists/main/domains/multi.txt',
      format: 'plain',
    },
  },
  {
    id: 'oisd-small',
    label: 'OISD Small',
    note: 'the essentials, built to never break anything',
    size: '≈56k domains',
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
    size: '≈540k domains',
    tier: 'strict',
    list: {
      name: 'Hagezi Pro',
      url: 'https://raw.githubusercontent.com/hagezi/dns-blocklists/main/domains/pro.txt',
      format: 'plain',
    },
  },
  {
    id: 'oisd-big',
    label: 'OISD Big',
    note: 'the full OISD net — wide, still breakage-shy',
    size: '≈500k domains',
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
    size: '≈78k domains',
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
    note: 'malware, phishing & scam domains — big, but aimed at threats, not ads',
    size: '≈2M domains',
    tier: 'security',
    list: {
      name: 'Hagezi TIF',
      url: 'https://raw.githubusercontent.com/hagezi/dns-blocklists/main/domains/tif.txt',
      format: 'plain',
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
