// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors
import { useEffect, useState } from 'react'
import { Link, Navigate, useLocation } from 'react-router-dom'
import { fmtDateTime, api } from '@wms/app/api'
import { T } from './tvApi'
import { IconEdit, IconPlus, IconRefresh, IconTv } from '@wms/app/icons'
import { toast } from '@wms/app/state'
import { ask } from '@wms/app/ui'
import { PageHead, Section } from '@wms/app/pages/settings/ui'
import TunerLogo from './TunerLogo'
import { LogoConsentHint } from '@wms/app/components/edit/LogoChooser'
import { forgetLineup, type Driver, type EPGSource, type Found, type ScanState, type Tuner, type TunerChannel } from './tvApi'
import { BulkBar, ChannelHealth, CheckAll, Duplicates, Merged } from './TunerTools'
import { Field, STATUS } from './tunerFields'
import SpecPage from '@wms/app/spec/SpecPage'
import { guideSourcesSpec, tunersSpec } from './specs/tuners'
import TunerSetup from './TunerSetup'
import GuideReview from './GuideReview'
import { ModuleMissing } from '@wms/app/components/ModuleMissing'
import './tvsettings.css'
import LegalNote from '@wms/app/components/LegalNote'

// Settings → Channels & tuners: tuners and IPTV, guide data and WittyTV's options.
// Channels from the library are made and changed on their own page, /tv/channels.

// Settings → Live TV & tuners (WittyTuner's own section, after Channels & tuners): the tuners and
// IPTV, one tuner's channels, duplicate channels and the guide sources. Library channels and live
// TV's options stay the server's, under Channels & tuners. Every app draws the same from sdui.json
// (wittytuner, wittytuner-tuner); this is the browser's richer page.

export default function TunerSettings() {
  const loc = useLocation()
  const parts = loc.pathname.split('/').filter(Boolean) // settings, tuners, …
  if (parts[2] === 'tuner' && parts[3]) return <TunerChannels id={parts[3]} />
  if (parts[2] === 'duplicates') return <Duplicates />
  if (parts[2] === 'guide') return <GuideReview />
  if (parts.length > 2) return <Navigate to={HOME} replace />
  return <Overview />
}

const HOME = '/settings/tuners'

function Overview() {
  return (
    <>
      <PageHead title="Live TV & tuners" desc="Tuners, IPTV playlists and streams, and guide data for their channels. Channels from your library, and live TV's options, are under Channels & tuners."
        actions={<Link className="btn" to="/tv"><IconTv /> Open the guide</Link>} />
      <Tuners />
      <GuideSources />
      <LegalNote kind="tv" className="set-legal" />
      <LegalNote kind="logos" className="set-legal" />
    </>
  )
}

// -------------------------------------------------------------------- tuners

function Tuners() {
  const [list, setList] = useState<Tuner[] | null>(null)
  const [drivers, setDrivers] = useState<Driver[] | null>(null)
  const [noModule, setNoModule] = useState(false)
  const [driversErr, setDriversErr] = useState('')
  const [found, setFound] = useState<Found[] | null>(null)
  const [searching, setSearching] = useState(false)
  // The setup, open: from the start, at a device that was found, or at one kind by hand.
  const [setup, setSetup] = useState<{ start?: Found } | null>(null)
  const dupes = true
  const load = () => api.get<Tuner[]>(T + '/tuners').then(setList).catch((e) => { if (e.status !== 424) toast(e.message) })
  const loadDrivers = () => { setDriversErr(''); return api.get<Driver[]>(T + '/drivers').then((d) => { setDrivers(d); setNoModule(false) }).catch((e) => { if (e.status === 424) setNoModule(true); else setDriversErr(e.message) }) }
  useEffect(() => { void load(); void loadDrivers() }, [])
  const search = async () => {
    setSearching(true)
    // A search that doesn't finish found nothing: Add live TV takes it by hand.
    try { setFound(await api.get<Found[]>(T + '/discover')) } catch { setFound([]) } finally { setSearching(false) }
  }
  // The demo tuner: one press, and live TV works with no hardware and no internet.
  const [addingDemo, setAddingDemo] = useState(false)
  const hasDemo = !!list?.some((t) => t.driver === 'demo')
  const canDemo = !!drivers?.some((d) => d.id === 'demo' && d.available)
  const addDemo = async () => {
    setAddingDemo(true)
    try {
      await api.post<Tuner>(T + '/tuners', { driver: 'demo', name: 'Demo tuner', options: {}, enabled: true })
      forgetLineup()
      toast('Demo tuner added: its channels are in the guide')
      void load()
    } catch (e: any) { toast(e.message) } finally { setAddingDemo(false) }
  }
  const remove = async (t: Tuner) => {
    if (!await ask({ title: `Remove “${t.name}” from this server?`, body: t.channels === 1 ? 'Its channel goes with it.' : `Its ${t.channels} channels go with it.`, confirm: 'Remove tuner', danger: true })) return
    try { await api.del(`${T}/tuners/${t.id}`); forgetLineup(); void load() } catch (e: any) { toast(e.message) }
  }
  const desc = <><Link to="/help/channels#live-tv-playlists">How tuners and playlists work.</Link> Add as many as you like, including streams a licensed device offers to home media servers. Encrypted channels are shown as encrypted and never decrypted (<Link to="/tv/encrypted">why, and what you can do</Link>). No channel lists or stream addresses come with the server: bring your own.</>
  if (noModule) {
    return (
      <Section title="Tuners and IPTV" desc={desc}>
        <p className="muted">Live TV from a tuner, a provider's playlist or a capture card needs the WittyTuner module. Channels from your library don't.</p>
        <ModuleMissing ids={['wittytuner']} what="live TV" onReady={() => { void loadDrivers(); void load() }} />
      </Section>
    )
  }
  return (
    <Section title="Tuners and IPTV" desc={desc}
      actions={<div className="tvs-actions">
        {dupes && list && list.length > 0 && <Link className="btn sm" to={HOME + '/duplicates'}>Duplicates</Link>}
        {canDemo && !hasDemo && <button className="btn sm" disabled={addingDemo} onClick={() => void addDemo()} title="Pretend channels made on this server, with a guide, for trying live TV without a tuner or the internet">{addingDemo ? 'Adding…' : 'Add a demo tuner'}</button>}
        <button className="btn sm" disabled={searching || !drivers} onClick={() => void search()}><IconRefresh /> {searching ? 'Searching…' : 'Search for tuners'}</button>
        <button className="btn sm primary" disabled={!drivers} onClick={() => setSetup({})}><IconPlus /> Add live TV</button>
      </div>}>
      {!drivers && !driversErr && <p className="muted" role="status">Getting WittyTuner ready…</p>}
      {list && list.length === 0 && canDemo && <p className="tvs-note">No tuner or playlist yet? <b>Add a demo tuner</b> to try the guide, recording and WittyTV with pretend channels made on this server. Remove it any time.</p>}
      {driversErr && <p className="tvs-error" role="alert">WittyTuner didn't answer: {driversErr} <button className="btn sm" onClick={() => void loadDrivers()}><IconRefresh /> Try again</button></p>}
      {drivers && (list || found) && <TunersList list={list} found={found} drivers={drivers} onSetup={(f) => setSetup({ start: f })} onRemove={(t) => void remove(t)} />}
      {drivers && drivers.length > 0 && (
        <details className="tvs-details">
          <summary>What WittyTuner can use, and how well each has been tried</summary>
          <div className="tvs-drivers">
            {drivers.map((d) => (
              <div key={d.id} className={'tvs-driver' + (d.available ? '' : ' off')}>
                <div className="tvs-driver-head"><b>{d.name}</b><span className={'tvs-status ' + d.status} title={STATUS[d.status]?.desc}>{STATUS[d.status]?.label}</span></div>
                <p>{d.description}</p>
                {d.note && <p className="tvs-dim">{d.note}</p>}
                {d.available && !d.planned && <button className="btn sm" onClick={() => setSetup({ start: { driver: d.id, name: '', options: {} } })} aria-label={`Add ${d.name} by hand`}><IconPlus /> Add by hand</button>}
              </div>
            ))}
          </div>
        </details>
      )}
      {setup && drivers && <TunerSetup drivers={drivers} start={setup.start} onClose={() => setSetup(null)} onAdded={() => { setFound(null); void load() }} />}
    </Section>
  )
}

// The tuners found and the tuners added, as rows.
function TunersList({ list, found, drivers, onSetup, onRemove }: { list: Tuner[] | null; found: Found[] | null; drivers: Driver[]; onSetup: (f: Found) => void; onRemove: (t: Tuner) => void }) {
  const page = tunersSpec(list && list.map((t) => ({ id: t.id, name: t.name, driverName: drivers.find((d) => d.id === t.driver)?.name || t.driver, channels: t.channels, clear: t.clear, encrypted: t.encrypted, lastError: t.lastError })),
    found && found.map((f) => ({ name: f.name, detail: f.detail || drivers.find((x) => x.id === f.driver)?.name || '', canSetUp: drivers.some((x) => x.id === f.driver) })))
  const handled = new Set<string>()
  for (const s of page.sections) if (s.kind === 'rows') for (const r of s.items) for (const a of r.actions || []) if (a.id) handled.add(a.id)
  const onAction = (id: string) => {
    if (id.startsWith('set:')) { const f = found?.[Number(id.slice(4))]; if (f) onSetup(f) } else if (id.startsWith('remove:')) { const t = list?.find((x) => x.id === Number(id.slice(7))); if (t) onRemove(t) }
  }
  return <SpecPage page={page} embedded bare handled={handled} onAction={onAction} />
}

function TunerChannels({ id }: { id: string }) {
  const [t, setT] = useState<Tuner | null>(null)
  const [list, setList] = useState<TunerChannel[] | null>(null)
  const [driver, setDriver] = useState<Driver | null>(null)
  const [scan, setScan] = useState<ScanState | null>(null)
  const [q, setQ] = useState('')
  // Narrowing a long playlist: by its groups, countries and languages, and what's shown or missing.
  const [group, setGroup] = useState('')
  const [country, setCountry] = useState('')
  const [lang, setLang] = useState('')
  const [only, setOnly] = useState<'' | 'shown' | 'hidden' | 'noguide' | 'missing'>('')
  const [opts, setOpts] = useState<Record<string, string> | null>(null)
  const [limit, setLimit] = useState('0')
  const health = true
  const dupes = true
  // Bulk changes arrived with health checks and merging; either says the server has them.
  const bulk = health || dupes
  const [picked, setPicked] = useState<Set<number>>(() => new Set())
  const load = () => {
    api.get<Tuner[]>(T + '/tuners').then((l) => {
      const me = l.find((x) => String(x.id) === id) || null
      setT(me)
      if (me) { setOpts(me.options); setLimit(String(me.maxStreams)); api.get<Driver[]>(T + '/drivers').then((d) => setDriver(d.find((x) => x.id === me.driver) || null)).catch(() => {}) }
    }).catch((e) => toast(e.message))
    api.get<TunerChannel[]>(`${T}/tuners/${id}/channels`).then(setList).catch((e) => toast(e.message))
  }
  useEffect(load, [id])
  useEffect(() => {
    if (!scan?.running) return
    const t = setTimeout(() => api.get<ScanState>(`${T}/tuners/${id}/scan`).then((s) => { setScan(s); if (!s.running) { load(); forgetLineup(); toast(s.error || `Scan finished: ${s.clear} clear, ${s.encrypted} encrypted${s.unknown ? `, ${s.unknown} not checked yet` : ''}`) } }).catch(() => {}), 1500)
    return () => clearTimeout(t)
  }, [scan]) // eslint-disable-line react-hooks/exhaustive-deps
  const edit = async (c: TunerChannel, patch: Record<string, unknown>) => {
    try {
      const n = await api.put<TunerChannel>(`${T}/channels/${c.id}`, patch)
      setList((l) => l?.map((x) => (x.id === c.id ? n : x)) || null)
      forgetLineup()
    } catch (e: any) { toast(e.message) }
  }
  const startScan = async (source: string) => {
    try { setScan(await api.post<ScanState>(`${T}/tuners/${id}/scan`, { source })) } catch (e: any) { toast(e.message) }
  }
  const all = async (on: boolean) => {
    try { await api.post(`${T}/tuners/${id}/channels/enable`, { enabled: on }); forgetLineup(); load() } catch (e: any) { toast(e.message) }
  }
  const saveSettings = async () => {
    try { await api.put(`${T}/tuners/${id}`, { name: t?.name, options: opts, enabled: t?.enabled, maxStreams: Number(limit) || 0 }); toast('Saved'); load() } catch (e: any) { toast(e.message) }
  }
  const refresh = async () => {
    try { await api.post(`${T}/tuners/${id}/refresh`); toast('Channel list read'); forgetLineup(); load() } catch (e: any) { toast(e.message) }
  }
  const values = (k: 'group' | 'country' | 'language') => [...new Set((list || []).map((c) => c[k] || '').filter(Boolean))].sort((a, b) => a.localeCompare(b))
  const groups = values('group'), countries = values('country'), langs = values('language')
  const matching = (list || []).filter((c) => (!q || c.name.toLowerCase().includes(q.toLowerCase()) || c.number.includes(q) || (c.group || '').toLowerCase().includes(q.toLowerCase()))
    && (!group || c.group === group) && (!country || c.country === country) && (!lang || c.language === lang)
    && (!only || (only === 'shown' && c.enabled) || (only === 'hidden' && !c.enabled) || (only === 'noguide' && !c.epgSet && !c.epgId) || (only === 'missing' && !!c.missingSince)))
  const shown = matching.slice(0, 500)
  // Show or hide every channel the filters pick: a group of a big playlist at once.
  const setMatching = async (on: boolean) => {
    const ids = matching.filter((c) => c.enabled !== on).map((c) => c.id)
    if (!ids.length) return
    try { await api.post(`${T}/channels/bulk`, { ids, action: on ? 'enable' : 'disable' }); forgetLineup(); load() } catch (e: any) { toast(e.message) }
  }
  const filtered = !!(q || group || country || lang || only)
  const pick = (c: TunerChannel, on: boolean) => setPicked((p) => { const n = new Set(p); if (on) n.add(c.id); else n.delete(c.id); return n })
  const allShownPicked = shown.length > 0 && shown.every((c) => picked.has(c.id))
  // What is picked, in the order shown: renumbering and merging go by it.
  const pickedInOrder = shown.filter((c) => picked.has(c.id)).concat((list || []).filter((c) => picked.has(c.id) && !shown.includes(c)))
  return (
    <>
      <PageHead title={t?.name || 'Tuner'} desc={t?.detail || driver?.name} actions={<Link className="btn" to={HOME}>Back</Link>} />
      {t?.lastError && <p className="tvs-error" role="alert">{t.lastError}</p>}
      <Section title="Channels" desc={list ? `${list.length} channels, ${list.filter((c) => c.enabled).length} shown in the guide. What you change here stays when the list is read again.` : ''}
        actions={<div className="tvs-actions">
          <button className="btn sm" onClick={() => void refresh()}><IconRefresh /> Read the list</button>
          {driver?.scans && <button className="btn sm primary" disabled={scan?.running} onClick={() => void startScan(opts?.source || '')}>{scan?.running ? `Scanning… ${scan.percent}%` : 'Scan for channels'}</button>}
        </div>}>
        {health && <CheckAll tunerId={id} onDone={load} />}
        {scan?.running && <div className="tvs-progress" role="progressbar" aria-valuenow={scan.percent} aria-valuemin={0} aria-valuemax={100} aria-label="Scan progress"><i style={{ width: scan.percent + '%' }} /><span>{scan.found} found{scan.message ? ` · ${scan.message}` : ''}</span></div>}
        {scan?.error && <p className="tvs-error" role="alert">{scan.error}</p>}
        {scan && !scan.running && !scan.error && <p className="tvs-note" role="status">The scan found {scan.clear} clear and {scan.encrypted} encrypted {scan.clear + scan.encrypted === 1 ? 'channel' : 'channels'}{scan.unknown ? `, and ${scan.unknown} not checked yet` : ''}.{scan.encrypted > 0 && <> Encrypted channels can't be played here, which isn't a fault in your setup. <Link to="/tv/encrypted">What can I do?</Link></>}</p>}
        <div className="tvs-toolbar">
          <input className="input" type="search" aria-label="Find a channel" placeholder="Find a channel…" value={q} onChange={(e) => setQ(e.target.value)} />
          {groups.length > 1 && <select className="input" aria-label="Group" value={group} onChange={(e) => setGroup(e.target.value)}><option value="">Every group</option>{groups.map((g) => <option key={g}>{g}</option>)}</select>}
          {countries.length > 1 && <select className="input" aria-label="Country" value={country} onChange={(e) => setCountry(e.target.value)}><option value="">Every country</option>{countries.map((g) => <option key={g}>{g}</option>)}</select>}
          {langs.length > 1 && <select className="input" aria-label="Language" value={lang} onChange={(e) => setLang(e.target.value)}><option value="">Every language</option>{langs.map((g) => <option key={g}>{g}</option>)}</select>}
          <select className="input" aria-label="Which channels" value={only} onChange={(e) => setOnly(e.target.value as typeof only)}>
            <option value="">Shown and hidden</option><option value="shown">Shown in the guide</option><option value="hidden">Hidden</option><option value="noguide">With no guide</option><option value="missing">Missing from the playlist</option>
          </select>
          {filtered
            ? <><button className="btn sm ghost" onClick={() => void setMatching(true)}>Show these {matching.length}</button><button className="btn sm ghost" onClick={() => void setMatching(false)}>Hide these {matching.length}</button></>
            : <><button className="btn sm ghost" onClick={() => void all(true)}>Show all</button><button className="btn sm ghost" onClick={() => void all(false)}>Hide all</button></>}
        </div>
        {bulk && picked.size > 0 && <BulkBar picked={pickedInOrder} all={list || []} health={health} merging={dupes} onDone={() => { setPicked(new Set()); forgetLineup(); load() }} onClear={() => setPicked(new Set())} />}
        <div className="tvs-table-wrap">
          <table className="tvs-table">
            <thead><tr>
              {bulk && <th scope="col"><input type="checkbox" aria-label="Select every channel shown" checked={allShownPicked} onChange={(e) => setPicked(e.target.checked ? new Set([...picked, ...shown.map((c) => c.id)]) : new Set([...picked].filter((x) => !shown.some((c) => c.id === x))))} /></th>}
              <th scope="col">In guide</th><th scope="col">Number</th><th scope="col">Name</th><th scope="col">Logo</th><th scope="col">Status</th>{health && <th scope="col">Health</th>}<th scope="col">Can record</th><th scope="col">Guide id</th>
            </tr></thead>
            <tbody>
              {shown.map((c) => (
                <tr key={c.id} className={(c.encrypted ? 'locked' : '') + (c.mergedInto ? ' tvs-backup' : '')}>
                  {bulk && <td><input type="checkbox" aria-label={`Select ${c.number} ${c.name}`} checked={picked.has(c.id)} onChange={(e) => pick(c, e.target.checked)} /></td>}
                  <td><input type="checkbox" aria-label={`${c.name} shows in the guide`} checked={c.enabled} onChange={(e) => void edit(c, { enabled: e.target.checked })} /></td>
                  <td><input className="input tvs-num-in" aria-label={`Number of ${c.name}`} defaultValue={c.number} onBlur={(e) => { if (e.target.value !== c.number) void edit(c, { number: e.target.value }) }} /></td>
                  <td>
                    <input className="input" aria-label={`Name of ${c.name}`} defaultValue={c.name} onBlur={(e) => { if (e.target.value !== c.name) void edit(c, { name: e.target.value }) }} />
                    {(c.group || c.country || c.language) && <small className="tvs-dim">{[c.group, c.country && !(c.group || '').includes(c.country) ? c.country : '', c.language].filter(Boolean).join(' · ')}</small>}
                    {!!c.missingSince && <small className="tvs-lock">Missing from the playlist since {fmtDateTime(c.missingSince * 1000)}. It's kept for a week in case it comes back, with everything you chose for it.</small>}
                    <Merged c={c} all={list || []} />
                    {!!c.catchupDays && <small className="tvs-dim">Catch-up: {c.catchupDays === 1 ? '1 day' : `${c.catchupDays} days`}</small>}
                  </td>
                  <td><TunerLogo channel={c} onChange={(n) => setList((l) => l?.map((x) => (x.id === n.id ? n : x)) || null)} /></td>
                  <td>
                    {c.security === 'encrypted' && <small className="tvs-lock">Encrypted: can't be played here. {c.encrypted}. <Link to="/tv/encrypted">What can I do?</Link> <button className="btn sm ghost" onClick={() => void edit(c, { checkAgain: true })} title="If the station has stopped encrypting: it is checked again next time it is tuned">Check again</button></small>}
                    {c.security === 'clear' && <small className="tvs-clear">Clear</small>}
                    {c.security === 'unknown' && <small className="tvs-dim" title="This source doesn't say. It is checked when you tune in.">Not checked yet</small>}
                    {c.atsc3 && <small className="tvs-dim">ATSC 3.0{c.audioCodec ? ` · ${c.videoCodec || ''} ${c.audioCodec}` : ''}</small>}
                  </td>
                  {health && <td><ChannelHealth c={c} /></td>}
                  <td><input type="checkbox" aria-label={`${c.name} can be recorded`} disabled={!!c.encrypted} checked={!c.noRecord && !c.encrypted} title={c.noRecord || 'Untick if the device says this may not be recorded'} onChange={(e) => void edit(c, { noRecord: !e.target.checked })} /></td>
                  <td>
                    <input className="input mono" aria-label={`Guide id of ${c.name}`} defaultValue={c.epgSet || c.epgId || ''} onBlur={(e) => { if (e.target.value !== (c.epgSet || c.epgId || '')) void edit(c, { epg: e.target.value }) }} />
                    {!c.epgSet && !!c.epgId && !!c.epgScore && c.epgScore < 100 && <small className="tvs-dim">Matched {c.epgHow === 'similar' ? 'by a similar name' : 'by ' + (c.epgHow || 'name')}, {c.epgScore}% sure</small>}
                    {!!c.guideShift && <small className="tvs-dim">Guide moved {c.guideShift > 0 ? '+' : ''}{c.guideShift / 60} h</small>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {matching.length > 500 && <p className="muted">Showing the first 500 of {matching.length}. Use the search box or the filters to find others.</p>}
        <LogoConsentHint compact />
        <LegalNote kind="logos" className="set-legal" />
      </Section>
      {driver && opts && (
        <Section title="Settings" actions={<button className="btn sm primary" onClick={() => void saveSettings()}>Save</button>}>
          <div className="form" style={{ maxWidth: 'none' }}>
            {driver.fields.map((f) => <Field key={f.key} f={f} value={opts[f.key] || ''} onChange={(v) => setOpts({ ...opts, [f.key]: v })} />)}
            <label htmlFor="tf-limit">Streams at once (0 = as the device says)
              <input className="input" id="tf-limit" type="number" min={0} value={limit} onChange={(e) => setLimit(e.target.value)} />
            </label>
          </div>
        </Section>
      )}
    </>
  )
}

// --------------------------------------------------------------- guide sources

function GuideSources() {
  const [list, setList] = useState<EPGSource[] | null>(null)
  const [url, setUrl] = useState('')
  const load = () => api.get<EPGSource[]>(T + '/epg/sources').then(setList).catch((e) => toast(e.message))
  useEffect(() => { void load() }, [])
  const add = async () => {
    try { await api.post(T + '/epg/sources', { url }); setUrl(''); toast('Reading the guide…'); setTimeout(() => void load(), 2500) } catch (e: any) { toast(e.message) }
  }
  const remove = async (s: EPGSource) => { try { await api.del(`${T}/epg/sources/${s.id}`); void load() } catch (e: any) { toast(e.message) } }
  const refresh = async (s: EPGSource) => { try { await api.post(`${T}/epg/sources/${s.id}/refresh`); toast('Reading the guide…'); setTimeout(() => void load(), 2500) } catch (e: any) { toast(e.message) } }
  return (
    <Section title="Guide data" desc="Listings for tuner and IPTV channels, in XMLTV format (also .gz), refreshed twice a day by themselves. Broadcasts also carry their own, read while a channel is on. Library channels make their own schedule. Channels are matched by guide id, name, number and call sign."
      actions={<Link className="btn sm" to={HOME + '/guide'}>Match channels to the guide</Link>}>
      {(list || []).filter((s) => s.warning).map((s) => <p key={s.id} className="tvs-error" role="alert"><b>{s.name}:</b> {s.warning}</p>)}
      <form onSubmit={(e) => { e.preventDefault(); if (url.trim()) void add() }}>
      <SpecPage embedded bare page={guideSourcesSpec(list && list.map((s) => ({ id: s.id, name: s.name, programmes: s.programmes, read: s.lastFetch ? fmtDateTime(s.lastFetch * 1000) : '', lastError: s.lastError, channels: s.channels, until: s.until ? fmtDateTime(s.until * 1000) : '' })), url)}
        handled={new Set([...(list || []).flatMap((s) => ['again:' + s.id, 'drop:' + s.id]), 'add'])}
        onField={(id, v) => { if (id === 'url') setUrl(String(v)) }}
        onAction={(id) => {
          if (id === 'add') void add()
          else if (id.startsWith('again:')) { const s = list?.find((x) => x.id === Number(id.slice(6))); if (s) void refresh(s) }
          else if (id.startsWith('drop:')) { const s = list?.find((x) => x.id === Number(id.slice(5))); if (s) void remove(s) }
        }} />
      <button type="submit" hidden />
      </form>
    </Section>
  )
}
