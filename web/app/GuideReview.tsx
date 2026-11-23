// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '@wms/app/api'
import { toast } from '@wms/app/state'
import { IconRefresh } from '@wms/app/icons'
import { PageHead, Section } from '@wms/app/pages/settings/ui'
import { useTvMode } from '@wms/app/tvmode/mode'
import { T, forgetLineup, type GuideMatch, type GuideReview as Review, type ReviewChannel } from './tvApi'

// Settings → Live TV & tuners → Match to the guide: channels with no guide, or an unsure match, each
// with its likeliest guide entries (one press takes one), a search of the whole guide, and Not in
// the guide. A match made here is remembered by the channel's name, so a playlist added again keeps
// it. Matching is detailed work for a mouse or a finger: on a television this says to use a computer.

const HOME = '/settings/tuners'

const HOW: Record<string, string> = {
  source: 'named by the playlist or device', manual: 'chosen by you', id: 'by guide id', name: 'by name', callsign: 'by call sign',
  number: 'by name and number', similar: 'by a similar name', search: '',
}

export default function GuideReview() {
  const tv = useTvMode()
  const [data, setData] = useState<Review | null>(null)
  const [all, setAll] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const load = (everything = all) => {
    setErr('')
    api.get<Review>(`${T}/epg/review` + (everything ? '?all=1' : '')).then(setData).catch((e) => setErr(e.message))
  }
  useEffect(() => { load() }, [all]) // eslint-disable-line react-hooks/exhaustive-deps
  if (tv) {
    return (
      <>
        <PageHead title="Match to the guide" actions={<Link className="btn" to={HOME}>Back</Link>} />
        <p className="tvs-note" role="note">To match channels to the guide, use a desktop app or a web browser. What you choose there is used here too.</p>
      </>
    )
  }
  const auto = async () => {
    setBusy(true)
    try {
      const r = await api.post<{ matched: number }>(`${T}/epg/automatch`, {})
      toast(r.matched ? `${r.matched} ${r.matched === 1 ? 'channel' : 'channels'} matched` : 'Nothing more could be matched for sure')
      forgetLineup(); load()
    } catch (e: any) { toast(e.message) } finally { setBusy(false) }
  }
  const choose = async (c: ReviewChannel, epg: string) => {
    try {
      await api.put(`${T}/channels/${c.id}`, { epg })
      forgetLineup()
      setData((d) => d && { ...d, channels: d.channels.map((x) => (x.id === c.id ? { ...x, guideId: epg, state: epg ? 'matched' : 'none', how: 'manual', score: epg ? 100 : 0, suggestions: epg ? [] : x.suggestions } : x)) })
      toast(epg ? `${c.name} matched` : `${c.name} has no guide now`)
    } catch (e: any) { toast(e.message) }
  }
  return (
    <>
      <PageHead title="Match to the guide" desc="Which guide entry each channel's listings come from. Sure matches are made by themselves; here are the rest. A match you choose is remembered, also if the playlist is added again."
        actions={<Link className="btn" to={HOME}>Back</Link>} />
      {err && <p className="tvs-error" role="alert">{err} <button className="btn sm" onClick={() => load()}><IconRefresh /> Try again</button></p>}
      {data && data.guideChannels === 0 && <p className="tvs-note" role="status">There's no guide data yet. Add a guide under Guide data, or wait for the one your playlist names to be read.</p>}
      {data && (
        <Section title={all ? 'Every channel' : 'To check'}
          desc={`${data.total} channels: ${data.matched} matched, ${data.unsure} unsure, ${data.none} with no guide. The guide has ${data.guideChannels} channels.`}
          actions={<div className="tvs-actions">
            <label className="tvs-check"><input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} /> Show every channel</label>
            <button className="btn sm primary" disabled={busy || data.guideChannels === 0} onClick={() => void auto()}>{busy ? 'Matching…' : 'Match what is sure'}</button>
          </div>}>
          {data.channels.length === 0 && <p className="muted" role="status">Every channel has its guide.</p>}
          <ul className="tvs-review" aria-label="Channels to match">
            {data.channels.map((c) => <ReviewRow key={c.id} c={c} onChoose={(id) => void choose(c, id)} />)}
          </ul>
        </Section>
      )}
    </>
  )
}

function ReviewRow({ c, onChoose }: { c: ReviewChannel; onChoose: (id: string) => void }) {
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<GuideMatch[] | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout>>()
  const search = (v: string) => {
    setQ(v)
    clearTimeout(timer.current)
    if (v.trim().length < 2) { setHits(null); return }
    timer.current = setTimeout(() => { api.get<GuideMatch[]>(`${T}/epg/search?q=${encodeURIComponent(v.trim())}`).then(setHits).catch(() => setHits([])) }, 250)
  }
  const label = c.state === 'matched' ? 'Matched' : c.state === 'unsure' ? 'Unsure' : 'No guide'
  return (
    <li className={'tvs-review-row ' + c.state}>
      <div className="tvs-review-head">
        <b>{c.number} {c.name}</b>
        <span className={'tvs-review-state ' + c.state}>{label}{c.state !== 'none' && c.score < 100 ? ` · ${c.score}%` : ''}</span>
        <small className="tvs-dim">{[c.tuner, c.group].filter(Boolean).join(' · ')}</small>
      </div>
      {c.guideId && c.state !== 'none' && <p className="tvs-dim">Now: {c.guideName || c.guideId} <code>{c.guideId}</code>{HOW[c.how || ''] ? `, ${HOW[c.how || '']}` : ''}</p>}
      {c.guideId && c.state === 'none' && <p className="tvs-dim">Its guide id <code>{c.guideId}</code> isn't in the guide data.</p>}
      {!!c.suggestions?.length && (
        <div className="tvs-review-picks" role="group" aria-label={`Suggestions for ${c.name}`}>
          {c.suggestions.map((s) => (
            <button key={s.id} className="btn sm" onClick={() => onChoose(s.id)} title={`${s.id}, ${HOW[s.how] || s.how}`}>
              {s.name} <span className="tvs-dim">{s.score}%</span>
            </button>
          ))}
        </div>
      )}
      <div className="tvs-review-find">
        <input className="input" type="search" placeholder="Search the guide…" aria-label={`Search the guide for ${c.name}`} value={q} onChange={(e) => search(e.target.value)} />
        {c.guideId && <button className="btn sm ghost" onClick={() => onChoose('')}>No guide</button>}
      </div>
      {hits && (
        <div className="tvs-review-picks" role="group" aria-label="Search results">
          {hits.length === 0 && <span className="muted">Nothing in the guide by that name.</span>}
          {hits.map((s) => <button key={s.id} className="btn sm" onClick={() => { onChoose(s.id); setHits(null); setQ('') }} title={s.id}>{s.name} <code className="tvs-dim">{s.id}</code></button>)}
        </div>
      )}
    </li>
  )
}
