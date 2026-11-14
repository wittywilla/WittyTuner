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
  await build({ stdin: { contents: "export * from './duplicates.ts'; export * from './tuners.ts'; export * from './tunerSetup.ts'; export { problems, withIds, handledIds, reachable } from '@wms/app/spec/model'", resolveDir: here, loader: 'ts' },
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

test('tuner setup: the kinds are a choice, unusable ones are disabled', { skip: !wms && 'no WittyMediaServer checkout' }, async () => {
  const { kindSpec, toField, detailsSpec, channelsSpec, trySpec, guideSpec, tunersSpec, guideSourcesSpec, problems, withIds, handledIds, reachable } = await load()
  const ok = (p) => assert.deepEqual(problems(withIds(p)), [], p.title)
  const p = kindSpec([{ id: 'playlist', label: 'A playlist', hint: 'M3U', usable: true }, { id: 'card', label: 'A card', hint: 'DVB', usable: false, dim: 'Not here' }], 'playlist')
  ok(p)
  const s = p.sections[0]
  assert.equal(s.choice, true)
  assert.deepEqual(s.items.map((r) => [r.id, r.on, !!r.disabled]), [['kind:playlist', true, false], ['kind:card', false, true]])
  assert.match(s.items[1].sub, /Not here/)
})

test('tuner setup: driver fields become the matching form fields', { skip: !wms && 'no WittyMediaServer checkout' }, async () => {
  const { kindSpec, toField, detailsSpec, channelsSpec, trySpec, guideSpec, tunersSpec, guideSourcesSpec, problems, withIds, handledIds, reachable } = await load()
  const ok = (p) => assert.deepEqual(problems(withIds(p)), [], p.title)
  assert.equal(toField({ key: 'a', label: 'A', kind: 'toggle' }, '1').value, true)
  assert.equal(toField({ key: 'a', label: 'A', kind: 'toggle', default: '1' }, '').value, true)
  assert.equal(toField({ key: 'p', label: 'P', kind: 'password', required: true }, 'x').label, 'P (needed)')
  assert.deepEqual(toField({ key: 's', label: 'S', kind: 'select', options: ['', 'a'] }, '').options, [{ value: '', label: 'None' }, { value: 'a', label: 'a' }])
  assert.equal(toField({ key: 'u', label: 'U', kind: 'url' }, 'http://x').kind, 'text')
})

test('tuner setup: details with found devices, a check and an error', { skip: !wms && 'no WittyMediaServer checkout' }, async () => {
  const { kindSpec, toField, detailsSpec, channelsSpec, trySpec, guideSpec, tunersSpec, guideSourcesSpec, problems, withIds, handledIds, reachable } = await load()
  const ok = (p) => assert.deepEqual(problems(withIds(p)), [], p.title)
  const d = { id: 'iptv', name: 'IPTV', available: true, description: 'A list.' }
  const p = detailsSpec({ choices: [d, { ...d, id: 'x', name: 'X', available: false, note: 'Not here' }], driver: d, fields: [{ key: 'url', label: 'Address', kind: 'url', required: true }], name: 'N', opts: { url: 'u' },
    canSearch: true, searching: false, found: [{ name: 'Box', detail: '1.2.3.4' }], missing: [], err: 'boom', check: { ok: true, text: '10 channels listed.', sample: 'A, B' } })
  ok(p)
  const handled = handledIds(p)
  assert.ok(handled.has('search'))
  assert.equal(p.sections.find((s) => s.h === 'Found').items[0].actions[0].id, 'use:0')
  assert.ok(p.sections.some((s) => s.kind === 'note' && /Such as A, B/.test(s.text)))
  assert.ok(p.sections.some((s) => s.kind === 'note' && s.text === 'boom'))
  const bad = detailsSpec({ choices: [d], driver: d, fields: [], name: '', opts: {}, canSearch: false, searching: false, found: [], missing: ['Address'], err: '', check: { ok: false, problem: { message: 'No answer', hint: 'Check it' } } })
  ok(bad)
  assert.ok(bad.sections.some((s) => s.kind === 'note' && /Still needed: Address/.test(s.text)))
  assert.ok(bad.sections.some((s) => s.kind === 'note' && /No answer Check it/.test(s.text)))
})

test('tuner setup: channels, try and guide steps', { skip: !wms && 'no WittyMediaServer checkout' }, async () => {
  const { kindSpec, toField, detailsSpec, channelsSpec, trySpec, guideSpec, tunersSpec, guideSourcesSpec, problems, withIds, handledIds, reachable } = await load()
  const ok = (p) => assert.deepEqual(problems(withIds(p)), [], p.title)
  const c = channelsSpec({ error: '', found: '12 channels found.', encrypted: true, scanNote: 'A scan', canScan: true, scanning: false, percent: 0, scanText: '', hasError: false, busy: false, zero: true })
  ok(c)
  assert.deepEqual(c.sections.at(-1).items.map((r) => r.id), ['scan', 'again'])
  assert.equal(c.sections.at(-1).items[0].on, true)
  const e = channelsSpec({ error: 'no', found: '', encrypted: false, scanNote: '', canScan: false, scanning: false, percent: 0, scanText: '', hasError: true, busy: false, zero: true })
  assert.ok(e.sections.at(-1).items.some((r) => r.id === 'change'))
  ok(trySpec({ options: [{ value: '1', label: '1 One' }], pick: '', trying: false, answer: { ok: true, title: 'One played', text: 'fast' } }))
  ok(trySpec({ options: null, pick: '', trying: true, answer: { ok: false, title: 'x', problem: { message: 'm' } } }))
  const g = guideSpec('http://g', 'err')
  ok(g)
  assert.equal(g.sections[1].items[0].id, 'guide')
})

test('tuners and guide sources: rows go to channels, buttons remove', { skip: !wms && 'no WittyMediaServer checkout' }, async () => {
  const { kindSpec, toField, detailsSpec, channelsSpec, trySpec, guideSpec, tunersSpec, guideSourcesSpec, problems, withIds, handledIds, reachable } = await load()
  const ok = (p) => assert.deepEqual(problems(withIds(p)), [], p.title)
  const p = tunersSpec([{ id: 3, name: 'Den', driverName: 'IPTV', channels: 1, clear: 1, encrypted: 0, lastError: 'late' }], [{ name: 'Box', detail: 'x', canSetUp: false }])
  ok(p)
  const row = p.sections.find((s) => s.h === 'Your tuners').items[0]
  assert.equal(row.to, 'tuner/3')
  assert.match(row.sub, /1 channel · 1 clear · late/)
  assert.equal(row.actions[0].id, 'remove:3')
  assert.equal(p.sections[0].items[0].actions, undefined)
  assert.match(tunersSpec([], null).sections[0].text, /None yet/)
  assert.match(tunersSpec([], []).sections[0].text, /No new tuners/)
  const g = guideSourcesSpec([{ id: 2, name: 'EPG', programmes: 40, read: 'today', lastError: '' }], '')
  ok(g)
  assert.deepEqual(g.sections[0].items[0].actions.map((a) => a.id), ['again:2', 'drop:2'])
  assert.equal(g.sections.at(-1).items[0].disabled, true)
  assert.ok(!reachable(withIds(g), new Set(['add'])).includes('add'))
})

