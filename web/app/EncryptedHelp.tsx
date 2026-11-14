// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '@wms/app/api'
import { T } from './tvApi'
import type { EncryptedHelpPage } from './tvApi'
import { WebLink } from '@wms/app/components/WebLink'
import './tvsettings.css'

// The page behind "Encrypted: can't be played here" (/tv/encrypted). The words come from
// WittyTuner's own route, so every client says the same thing.
export default function EncryptedHelp() {
  const [help, setHelp] = useState<EncryptedHelpPage | null>(null)
  const [err, setErr] = useState('')
  useEffect(() => { api.get<EncryptedHelpPage>(T + '/encrypted-help').then(setHelp).catch((e) => setErr(e.message)) }, [])
  return (
    <div className="tvs-help">
      <header className="tvs-help-head">
        <h1>{help?.title || 'Encrypted channels'}</h1>
        <Link className="btn sm" to="/help/channels">Help with TV</Link>
        <Link className="btn sm" to="/tv">Back to the guide</Link>
      </header>
      {err && <div className="tvs-error" role="alert">{err}</div>}
      {help && (
        <>
          {help.sections.map((s) => (
            <section key={s.heading}>
              <h2 className="tvs-help-h2">{s.heading}</h2>
              {s.paragraphs?.map((p) => <p key={p}>{p}</p>)}
              {s.bullets && <ul>{s.bullets.map((b) => <li key={b}>{b}</li>)}</ul>}
            </section>
          ))}
          <section>
            <h2 className="tvs-help-h2">Devices checked on {help.checked}</h2>
            <p>{help.devicesIntro}</p>
            <ul className="tvs-help-devices">
              {help.devices.map((d) => (
                <li key={d.name}>
                  <div>
                    <b>{d.name}</b>
                    <span>{d.facts}</span>
                    <span>Source: <WebLink href={d.url}>{d.source}</WebLink></span>
                  </div>
                </li>
              ))}
            </ul>
            <p className="tvs-dim">{help.note}</p>
          </section>
        </>
      )}
    </div>
  )
}
