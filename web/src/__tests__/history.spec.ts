import { describe, it, expect, beforeEach } from 'vitest'
import {
  chooseEpisode,
  loadProgress,
  progressKey,
  saveProgress,
  seriesProgress,
  type EpisodeChoice,
} from '@/stores/history'

const episodes = [
  { episode_id: '11', number: 1 },
  { episode_id: '12', number: 2 },
  { episode_id: '15', number: 5 },
]

// pick fails loudly instead of hiding a missing choice behind an assertion.
function pick(choice: EpisodeChoice<(typeof episodes)[number]> | null) {
  if (!choice) throw new Error('the history offered no episode')
  return choice
}

function store(seriesId: string, episodeId: string, position: number, updatedAt: number) {
  saveProgress({
    seriesId,
    seriesTitle: '测试剧',
    episodeId,
    episodeNumber: Number(episodeId) - 10,
    position,
    paused: true,
    rate: 1.5,
    updatedAt,
  })
}

describe('watch history', () => {
  beforeEach(() => {
    window.localStorage.clear()
  })

  it('keeps one record per episode', () => {
    store('1', '11', 12, 1000)
    store('1', '15', 60, 2000)
    expect(loadProgress('1', '11')?.position).toBe(12)
    expect(loadProgress('1', '15')?.position).toBe(60)
    expect(loadProgress('1', '99')).toBeNull()
    expect(window.localStorage.getItem('openduanju.watch-progress')).toContain(
      progressKey('1', '15'),
    )
  })

  it('returns the newest record of one series only', () => {
    store('1', '11', 12, 1000)
    store('1', '15', 60, 3000)
    store('2', '21', 5, 2000)
    expect(seriesProgress('1')?.episodeId).toBe('15')
    expect(seriesProgress('2')?.episodeId).toBe('21')
    expect(seriesProgress('3')).toBeNull()
  })

  it('opens the episode the link named', () => {
    const choice = pick(chooseEpisode(episodes, { requestedId: '15', storedId: '11' }))
    expect(choice.episode.episode_id).toBe('15')
    expect(choice.resumed).toBe(true)
    expect(choice.missing).toBeNull()
  })

  it('continues the episode this device watched last', () => {
    const choice = pick(chooseEpisode(episodes, { storedId: '12' }))
    expect(choice.episode.episode_id).toBe('12')
    expect(choice.resumed).toBe(true)
    expect(choice.missing).toBeNull()
  })

  it('never applies a missing episode to another one', () => {
    // The stored episode is no longer published: the page opens the first
    // episode without resuming, and reports which episode is gone.
    const choice = pick(chooseEpisode(episodes, { requestedId: '99', storedId: '98' }))
    expect(choice.episode.episode_id).toBe('11')
    expect(choice.resumed).toBe(false)
    expect(choice.missing).toBe('99')
    expect(loadProgress('1', choice.episode.episode_id)).toBeNull()
  })

  it('answers nothing for an empty episode list', () => {
    expect(chooseEpisode([], {})).toBeNull()
  })

  it('starts at the first episode without any record', () => {
    const choice = pick(chooseEpisode(episodes, {}))
    expect(choice.episode.episode_id).toBe('11')
    expect(choice.resumed).toBe(false)
    expect(choice.missing).toBeNull()
  })
})
