// Helpers shared by everything that shows or makes rules: the DNS Filter
// page, the device page and the Allow / Block dialog.
import type { Device, Rule } from './api';

// DeviceKey is how rules address a device: its MAC when Minos has seen one
// (so the rules follow it to a new address), else its IP.
export function deviceKey(d: Device): string {
  return d.mac || d.ip;
}

// DeviceLabel is the name to show: your label, else the name the device
// gave itself, else its address.
export function deviceLabel(d: Device | undefined, fallback = ''): string {
  return d ? (d.name || d.hostname || d.ip).trim() : fallback;
}

// FindDevice finds the device that used ip (any of its addresses).
export function findDevice(devices: Device[], ip: string): Device | undefined {
  return devices.find((d) => d.ip === ip || d.ips?.includes(ip));
}

// FindByKey finds the device a rule's target names.
export function findByKey(devices: Device[], key: string): Device | undefined {
  const k = key.toLowerCase();
  return devices.find((d) => (d.mac ?? '').toLowerCase() === k || (!d.mac && d.ip === key));
}

// ScopeText says who a rule applies to, in words.
export function scopeText(r: Pick<Rule, 'scope' | 'target'>, devices: Device[]): string {
  if (r.scope === 'everyone') return 'Everyone';
  if (r.scope === 'group') return `Group: ${r.target}`;
  return deviceLabel(findByKey(devices, r.target ?? ''), r.target ?? '');
}

// ListText turns a query-log list name into words where it names a device
// rule ("device:aa:bb:…" → "device: Kitchen iPad"); names is the
// deviceKeyNames map. Other lists keep their literal name, which is what
// the list filter matches.
export function listText(list: string, names: Record<string, string>): string {
  if (list.startsWith('device:')) {
    const key = list.slice('device:'.length);
    return `device: ${names[key.toLowerCase()] ?? key}`;
  }
  return list;
}

// SameRule reports whether two rules are the same decision.
export function sameRule(a: Rule, b: Rule): boolean {
  return (
    a.action === b.action &&
    a.kind === b.kind &&
    a.value === b.value &&
    a.scope === b.scope &&
    (a.target ?? '') === (b.target ?? '')
  );
}

// DomainChoices is what Allow / Block offers for a query-log row: the name
// looked up, then the wider domain rule that matched it when there is one.
export function domainChoices(qname: string, rule?: string): string[] {
  const out = [qname];
  const r = (rule ?? '').trim();
  if (r && r !== qname && /^[a-z0-9.-]+\.[a-z0-9-]+$/i.test(r) && qname.endsWith('.' + r)) out.push(r);
  return out;
}
