// Tiny global notice/error toasts so any page can report an outcome.
import { writable } from 'svelte/store';

export interface Toast {
  id: number;
  kind: 'notice' | 'error';
  text: string;
}

export const toasts = writable<Toast[]>([]);
let nextId = 1;

function push(kind: Toast['kind'], text: string): void {
  const id = nextId++;
  toasts.update((t) => [...t, { id, kind, text }]);
  setTimeout(() => toasts.update((t) => t.filter((x) => x.id !== id)), 6000);
}

export const notify = (text: string): void => push('notice', text);
export const notifyError = (e: unknown): void =>
  push('error', e instanceof Error ? e.message : String(e));
