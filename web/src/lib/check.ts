// Whether the Check a domain dialog is open — opened from the side menu or
// with Ctrl+K / ⌘K on any page; CheckDialog (mounted once in App) renders it.
import { writable } from 'svelte/store';

export const checkOpen = writable(false);
