// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors
//
// WittyTuner's screens in WittyMediaServer's web app (module API 5; integration.json says what it
// claims). The app calls register(host) once, when one of these is first needed: Settings, Live TV &
// tuners (with a tuner's channels and duplicate channels), the first-run step "tv", and the page
// behind "Encrypted: can't be played here". Every other app draws the SDUI pages in sdui.json
// (wittytuner, wittytuner-tuner, wittytuner-setup) instead.
import { lazy, Suspense, type ReactNode } from 'react'
import { Spinner } from '@wms/app/components/common'

const TunerSettings = lazy(() => import('./Settings'))
const SetupStep = lazy(() => import('./SetupStep'))
const EncryptedHelp = lazy(() => import('./EncryptedHelp'))

const wait = (el: ReactNode) => () => <Suspense fallback={<Spinner />}>{el}</Suspense>

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function register(_host: unknown): any {
  return {
    routes: {
      '/tv/encrypted': wait(<EncryptedHelp />),
    },
    settings: {
      tuners: () => <Suspense fallback={null}><TunerSettings /></Suspense>,
    },
    setup: {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      tv: (props: any) => <Suspense fallback={<Spinner />}><SetupStep {...props} /></Suspense>,
    },
  }
}
