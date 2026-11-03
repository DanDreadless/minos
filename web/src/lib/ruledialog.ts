// The Allow / Block dialog: asks where a rule should apply — this device,
// its group, everyone, or another device or group — before making it.
// RuleDialog (mounted once in App) renders it; every Allow and Block button
// that knows the device a lookup came from goes through here.
import { writable } from 'svelte/store';

export interface RuleDialogRequest {
  action: 'allow' | 'block';
  // Domains offers a choice of name: the one looked up first, then the
  // wider rule that matched it, when that differs.
  domains: string[];
  // Client is the address the lookup came from; absent when there is none
  // (Check a domain).
  client?: string;
  // DecidedBy is the query-log list that decided the lookup, so the dialog
  // can say when a more specific rule would still win.
  decidedBy?: string;
  resolve: (done: boolean) => void;
}

export const pendingRule = writable<RuleDialogRequest | null>(null);

// RuleDialog opens the dialog and resolves true once a rule was made.
export function ruleDialog(opts: Omit<RuleDialogRequest, 'resolve'>): Promise<boolean> {
  return new Promise((resolve) => {
    pendingRule.set({
      ...opts,
      domains: [...new Set(opts.domains.filter(Boolean))],
      resolve: (done) => {
        pendingRule.set(null);
        resolve(done);
      },
    });
  });
}
