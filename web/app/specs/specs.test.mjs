// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors
// The page specs of WittyTuner's screens, checked against the app's own spec model: from a
// WittyMediaServer checkout (WMS, or one beside this one), bundled with esbuild. Skipped without one.
import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, rmSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { build } from 'esbuild'

const here = fileURLToPath(new URL('.', import.meta.url))
const root = resolve(here, '../../..')
const wms = [process.env.WMS, join(root, '../WittyMediaServer'), join(root, '../s14-m6-WittyMediaServer'), join(root, '../s14-WittyMediaServer')]
  .filter(Boolean).find((d) => existsSync(join(d, 'web/src/spec/model.ts')))

async function load() {
  const out = join(here, '.specs.tmp.mjs')
  const alias = { name: 'app', setup(b) { b.onResolve({ filter: /^@wms\/app\// }, (a) => ({ path: join(wms, 'web/src', a.path.slice('@wms/app/'.length) + '.ts') })) } }
  await build({ stdin: { contents: "export * from './duplicates.ts'; export * from './tuners.ts'; export * from './tunerSetup.ts'; export { problems, withIds } from '@wms/app/spec/model'", resolveDir: here, loader: 'ts' },
    outfile: out, bundle: true, format: 'esm', platform: 'node', logLevel: 'warning', plugins: [alias] })
  try { return await import(out) } finally { rmSync(out, { force: true }) }
}

const g = [{ reason: 'same name', suggested: 2, channels: [{ id: 1, number: '4', name: 'Kids', tuner: 'A', health: 'failing' }, { id: 2, number: '104', name: 'Kids', tuner: 'B', health: 'ok' }] }]

test('duplicates: the suggested one is kept until another is chosen', { skip: !wms && 'no WittyMediaServer checkout' }, async () => {
  const { duplicatesSpec, problems, withIds } = await load()
  const p = duplicatesSpec(g, {}, -1)
  assert.equal(p.sections[0].h, 'Kids · Same name')
  assert.deepEqual(p.sections[0].items.map((r) => [r.id, r.on, r.sub]), [['keep:0:1', false, 'A · failing'], ['keep:0:2', true, 'B · opens']])
  assert.equal(p.sections[0].items[1].value, 'Keep in the guide')
  assert.equal(duplicatesSpec(g, { 0: 1 }, -1).sections[0].items[0].on, true)
  assert.equal(p.sections[1].items[0].id, 'merge:0')
  assert.equal(duplicatesSpec(g, {}, 0).sections[1].items[0].disabled, true)
  assert.deepEqual(problems(withIds(p)), [])
  assert.equal(duplicatesSpec(null, {}, -1).sections[0].text, 'Looking…')
  assert.match(duplicatesSpec([], {}, -1).sections[0].text, /No duplicates/)
  assert.equal(duplicatesSpec(null, {}, -1, 'No')?.sections[0].text, 'No')
})

test('tuners and guide sources: rows go to channels, buttons remove', { skip: !wms && 'no WittyMediaServer checkout' }, async () => {
  const { tunersSpec, guideSourcesSpec, problems, withIds } = await load()
  const p = tunersSpec([{ id: 3, name: 'Den', driverName: 'IPTV', channels: 1, clear: 1, encrypted: 0, lastError: 'late' }], [{ name: 'Box', detail: 'x', canSetUp: false }])
  assert.deepEqual(problems(withIds(p)), [])
  assert.match(tunersSpec([], null).sections[0].text, /None yet/)
  assert.match(tunersSpec([], []).sections[0].text, /No new tuners/)
  assert.deepEqual(problems(withIds(guideSourcesSpec([{ id: 1, name: 'G', programmes: 3, read: 'now', lastError: '' }], ''))), [])
})
