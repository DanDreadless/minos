// Generates docs/user-guide.md from web/src/lib/docs.ts — the same source
// the in-app Help page renders — so the repository's guide and the app's
// can never drift. Runs as part of `npm run build`; commit the result
// (release.sh refuses to tag if the build changed it).
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { transformWithEsbuild } from 'vite';

const here = dirname(fileURLToPath(import.meta.url));
const src = resolve(here, '../src/lib/docs.ts');
const out = resolve(here, '../../docs/user-guide.md');

// Builds that see only web/ (the Docker image's frontend stage) have no
// docs/ folder to write to. The guide is committed, so there is nothing to
// do there; failing would stop the image building (it did, v0.27.0–0.28.0).
if (!existsSync(dirname(out))) {
  console.log('docs/ is not here (a web-only build); skipping the user guide');
  process.exit(0);
}

const { code } = await transformWithEsbuild(readFileSync(src, 'utf8'), src, { loader: 'ts', format: 'esm' });
const dir = mkdtempSync(join(tmpdir(), 'minos-docs-'));
const mod = join(dir, 'docs.mjs');
writeFileSync(mod, code);
const { docs } = await import(pathToFileURL(mod).href);
rmSync(dir, { recursive: true, force: true });

const applies = { live: 'at once', file: 'after restart (minos.yaml)' };
const cell = (s) => String(s ?? '—').replace(/\|/g, '\\|').replace(/\n/g, ' ');

const lines = [
  '# Minos user guide',
  '',
  '<!-- Generated from web/src/lib/docs.ts by web/scripts/gen-user-guide.mjs — edit that file, not this one. -->',
  '',
  'The same guide is built into Minos: open **Help** in the menu, or the **?** beside any page title.',
  '',
  '"Applies at once" means the setting can be changed from the web interface (or the API) and takes effect',
  'immediately. "After restart" settings live only in `minos.yaml`; restart Minos after editing the file.',
  '',
  '## Contents',
  '',
  ...docs.map((s) => `- [${s.title}](#${s.id})`),
  '',
];
for (const s of docs) {
  lines.push(`<a id="${s.id}"></a>`, '', `## ${s.title}`, '', s.summary, '');
  for (const h of s.howto ?? []) lines.push(`- ${h}`);
  if (s.howto?.length) lines.push('');
  for (const t of s.tasks ?? []) lines.push(`**${t.q}** ${t.a}`, '');
  if (s.settings?.length) {
    lines.push('| Setting | Config key | Default | What it does | Applies |', '|---|---|---|---|---|');
    for (const st of s.settings) {
      let effect = cell(st.effect);
      if (st.fields?.length) {
        effect += '<br>' + st.fields.map((f) => `\`${f.key.slice(st.key.length + 1)}\`: ${cell(f.effect)}`).join('<br>');
      }
      lines.push(
        `| ${cell(st.label)}<br><small>${cell(st.where)}</small> | \`${st.key}\` | ${cell(st.default)} | ${effect} | ${applies[st.applies]} |`,
      );
    }
    lines.push('');
  }
}
// Leave the file untouched when nothing changed, and keep its existing line
// endings when something did: a Windows checkout holds CRLF, and rewriting
// it as LF would show as modified (and stop a release) with no real change.
const text = lines.join('\n');
let existing = null;
try {
  existing = readFileSync(out, 'utf8');
} catch {
  // First generation
}
if (existing !== null && existing.replace(/\r\n/g, '\n') === text) {
  console.log(`${out} is up to date`);
} else {
  writeFileSync(out, existing?.includes('\r\n') ? text.replace(/\n/g, '\r\n') : text);
  console.log(`wrote ${out}`);
}
