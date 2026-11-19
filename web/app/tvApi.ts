// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors
import type { LogoKind } from '@wms/app/components/BrandLogo'

// WittyTuner's routes, under the app's API: /api/modules/wittytuner/…
export const T = '/modules/wittytuner'

// The app keeps the guide's channel list for a while; it reads it again when it opens. Nothing to
// forget here: the module's changes reach the guide when it's next opened.
export const forgetLineup = () => {}

export type EncryptedHelpPage = {
  title: string; sections: { heading: string; paragraphs?: string[]; bullets?: string[] }[]
  devicesIntro: string; devices: { name: string; facts: string; source: string; url: string }[]; checked: string; note: string
}
type SecurityState = 'clear' | 'encrypted' | 'unknown'
export type DriverField = { key: string; label: string; kind: 'text' | 'url' | 'path' | 'password' | 'select' | 'number' | 'textarea' | 'toggle'; help?: string; default?: string; required?: boolean; options?: string[] }
export type Driver = { id: string; name: string; description: string; status: 'tested' | 'mock' | 'untested' | 'planned'; platforms?: string[]; fields: DriverField[]; scans: boolean; planned?: boolean; available: boolean; note?: string }
export type Tuner = { id: number; driver: string; name: string; options: Record<string, string>; enabled: boolean; maxStreams: number; lastScan: number | null; lastError: string; detail: string; channels: number; encrypted: number; clear: number; inUse: number }
export type TunerChannel = {
  logoSet?: string; logoUrl?: string; brand?: { kind: LogoKind; name: string }; id: number; tunerId: number; sourceId: string; number: string; name: string; group?: string; hd?: boolean; radio?: boolean; encrypted?: string; security: SecurityState; atsc3?: boolean; videoCodec?: string; audioCodec?: string; noRecord?: string; enabled: boolean; epgId?: string; numberSet?: string; nameSet?: string; epgSet?: string
  // Whether it last opened (tv-channel-health), and the channel it is a backup of, or how many backups it has.
  health?: '' | 'ok' | 'failing'; healthAt?: number; healthMsg?: string; mergedInto?: number; backups?: number; catchupDays?: number
  // What the playlist says (tvg-language, tvg-country, tvg-shift in minutes); when its source stopped listing it; how sure its guide match is.
  language?: string; country?: string; guideShift?: number; missingSince?: number; epgScore?: number; epgHow?: string
}
export type HealthRun = { running: boolean; done: number; total: number; ok: number; failing: number }
export type DuplicateGroup = { reason: string; channels: { id: number; tunerId: number; tuner: string; number: string; name: string; health?: string }[]; suggested: number }
export type BulkAction = 'enable' | 'disable' | 'renumber' | 'merge' | 'unmerge' | 'check'
export type ScanState = { running: boolean; percent: number; found: number; message?: string; error?: string; added: number; clear: number; encrypted: number; unknown: number }
export type Found = { driver: string; name: string; detail?: string; options: Record<string, string> }
// The guided setup's questions: what a source says before it's saved, and what one channel did when tried.
export type TunerProblem = { kind: 'settings' | 'unreachable' | 'not-a-tuner' | 'unreadable' | 'empty' | 'encrypted' | 'busy' | 'no-signal' | 'no-picture' | 'other'; message: string; hint?: string; helpUrl?: string }
export type TunerCheck = { ok: true; result: { detail?: string; maxStreams?: number; channels: number; encrypted: number; sample?: string[]; ownGuide?: boolean } } | { ok: false; problem: TunerProblem }
export type TunerTest = { ok: boolean; channel?: { id: number; number: string; name: string }; problem?: TunerProblem; seconds: number; video?: string; audio?: string; clear?: boolean }
export type EPGSource = { id: number; name: string; url: string; lastFetch: number | null; lastError: string; programmes: number; tunerId?: number; channels?: number; until?: number; warning?: string }
// Matching channels to the guide (Settings → Match to the guide).
export type GuideMatch = { id: string; name: string; score: number; how: string }
export type ReviewChannel = { id: number; name: string; number: string; group?: string; tuner: string; guideId?: string; guideName?: string; score: number; how?: string; state: 'matched' | 'unsure' | 'none'; suggestions?: GuideMatch[] }
export type GuideReview = { channels: ReviewChannel[]; total: number; matched: number; unsure: number; none: number; guideChannels: number }
