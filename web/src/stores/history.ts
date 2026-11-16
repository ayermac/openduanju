// Device-local watch history. It stays in this browser: no cloud sync and no
// analytics service.

export interface WatchProgress {
  seriesId: string
  seriesTitle: string
  episodeId: string
  episodeNumber: number
  // HTMLMediaElement.currentTime uses seconds; keep that unit at every boundary.
  position: number
  paused: boolean
  rate: number
  updatedAt: number
}

const KEY = 'openduanju.watch-progress'

function readAll(): Record<string, WatchProgress> {
  try {
    const raw = window.localStorage.getItem(KEY)
    return raw ? (JSON.parse(raw) as Record<string, WatchProgress>) : {}
  } catch {
    return {}
  }
}

function writeAll(value: Record<string, WatchProgress>) {
  try {
    window.localStorage.setItem(KEY, JSON.stringify(value))
  } catch {
    // A private window without storage simply loses the progress.
  }
}

// progressKey identifies one episode of one series.
export function progressKey(seriesId: string, episodeId: string): string {
  return `${seriesId}:${episodeId}`
}

// saveProgress records the position of one episode on this device.
export function saveProgress(progress: WatchProgress): void {
  const all = readAll()
  all[progressKey(progress.seriesId, progress.episodeId)] = progress
  writeAll(all)
}

// loadProgress returns the stored progress of one episode.
export function loadProgress(seriesId: string, episodeId: string): WatchProgress | null {
  return readAll()[progressKey(seriesId, episodeId)] ?? null
}

// recentSeries returns the newest progress per series, newest first.
export function recentSeries(limit = 10): WatchProgress[] {
  const all = readAll()
  const newest = new Map<string, WatchProgress>()
  for (const progress of Object.values(all)) {
    const current = newest.get(progress.seriesId)
    if (!current || current.updatedAt < progress.updatedAt) {
      newest.set(progress.seriesId, progress)
    }
  }
  return [...newest.values()].sort((a, b) => b.updatedAt - a.updatedAt).slice(0, limit)
}

// seriesProgress returns the newest progress of one series, so entering the
// series can continue the episode this device watched last.
export function seriesProgress(seriesId: string): WatchProgress | null {
  let newest: WatchProgress | null = null
  for (const progress of Object.values(readAll())) {
    if (progress.seriesId !== seriesId) continue
    if (!newest || newest.updatedAt < progress.updatedAt) newest = progress
  }
  return newest
}

// Deleting a series card removes all episode records of that series, not just
// the newest one. Otherwise an old episode record would put the card back.
export function removeSeriesProgress(seriesId: string): void {
  const all = readAll()
  for (const key of Object.keys(all)) {
    if (all[key]?.seriesId === seriesId) delete all[key]
  }
  writeAll(all)
}

// Clearing history removes only this project's watch-progress storage. It never
// calls localStorage.clear, because other sites may share the same browser.
export function clearWatchHistory(): void {
  writeAll({})
}

// EpisodeChoice is the episode a series page opens and whether that episode
// continues stored progress.
export interface EpisodeChoice<T> {
  episode: T
  resumed: boolean
  // missing names the episode that was asked for or stored but is no longer
  // in the published list. Its position is never applied to another episode.
  missing: string | null
}

// chooseEpisode picks the episode to open: the one the link asked for, then
// the one this device watched last, then the first episode. A stored episode
// that is no longer published is reported instead of being mapped onto
// another episode.
export function chooseEpisode<T extends { episode_id: string }>(
  episodes: T[],
  candidates: { requestedId?: string; storedId?: string },
): EpisodeChoice<T> | null {
  const [first] = episodes
  if (!first) return null
  const find = (id?: string) =>
    id ? (episodes.find((item) => item.episode_id === id) ?? null) : null
  const requested = find(candidates.requestedId)
  if (requested) return { episode: requested, resumed: true, missing: null }
  const stored = find(candidates.storedId)
  if (stored) return { episode: stored, resumed: true, missing: null }
  const missing = candidates.requestedId ?? candidates.storedId ?? null
  return { episode: first, resumed: false, missing }
}
