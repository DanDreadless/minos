// Generates docs/user-guide.md from web/src/lib/docs.ts — the same source
// the in-app Help page renders — so the repository's guide and the app's
// can never drift. Runs as part of `npm run build`; commit the result
// (release.sh refuses to tag if the build changed it).
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { transformWithEsbuild } from 'vite';

const here = dirname(fileURLToPath(import.meta.url));
const src = resolve(here, '../src/lib/docs.ts');
const out = resolve(here, '../../docs/user-guide.md');

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
writeFileSync(out, lines.join('\n'));
console.log(`wrote ${out}`);
