// Device names by address, so pages that list queries can say "Kitchen
// iPad" instead of 192.168.1.21. Loaded on first use and refreshed once a
// minute while something uses it; a name is the user's label, else the
// discovered hostname.
import { writable, type Readable } from 'svelte/store';
import { api } from './api';

const names = writable<Record<string, string>>({});
// Search text per address: label and hostname, lowercased — what the
// server's device-name search matches.
const search = writable<Record<string, string>>({});
let users = 0;
let timer: ReturnType<typeof setInterval> | null = null;

async function load(): Promise<void> {
  try {
    const shown: Record<string, string> = {};
    const text: Record<string, string> = {};
    for (const d of await api.clients()) {
      const label = (d.name ?? '').trim();
      const host = (d.hostname ?? '').trim();
      if (!label && !host) continue;
      for (const ip of d.ips?.length ? d.ips : [d.ip]) {
        shown[ip] = label || host;
        text[ip] = `${label}\n${host}`.toLowerCase();
      }
    }
    names.set(shown);
    search.set(text);
  } catch {
    // Names are a nicety; addresses still show.
  }
}

// Refreshing runs while anything subscribes to either store.
function shared<T>(store: Readable<T>): Readable<T> {
  return {
    subscribe(run) {
      if (users++ === 0) {
        void load();
        timer = setInterval(load, 60_000);
      }
      const unsub = store.subscribe(run);
      return () => {
        unsub();
        if (--users === 0 && timer) {
          clearInterval(timer);
          timer = null;
        }
      };
    },
  };
}

// DeviceNames is the address → name map.
export const deviceNames = shared(names);

// DeviceSearch is the address → searchable text map, for matching the
// search box against device names the way the server does.
export const deviceSearch = shared(search);
