import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';

const target = document.getElementById('app');
if (!target) throw new Error('missing #app mount point');

// Svelte 5: components are no longer classes — mount() replaces `new App()`.
const app = mount(App, { target });

export default app;
