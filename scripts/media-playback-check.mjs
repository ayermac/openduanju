#!/usr/bin/env node
// Browser playback check for one local media file.
//
// It serves the file with Range support, then drives a real Chromium page:
// metadata must load, videoWidth/videoHeight must be positive, playback must
// advance, seeking must land and continue, and pause must hold. It does not
// prove object-storage delivery; that requires a real COS object.
import fs from 'node:fs'
import http from 'node:http'
import path from 'node:path'
import process from 'node:process'
import { chromium } from 'playwright'

function parseArgs(argv) {
  const args = { file: '', headed: false }
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === '--file') {
      args.file = argv[i + 1] ?? ''
      i += 1
    } else if (argv[i] === '--headed') {
      args.headed = true
    }
  }
  return args
}

function contentTypeFor(file) {
  switch (path.extname(file).toLowerCase()) {
    case '.mp4':
    case '.m4v':
      return 'video/mp4'
    case '.webm':
      return 'video/webm'
    default:
      return 'application/octet-stream'
  }
}

function startServer(file) {
  const stat = fs.statSync(file)
  const type = contentTypeFor(file)
  const server = http.createServer((req, res) => {
    if (req.url !== '/media') {
      res.writeHead(404)
      res.end('not found')
      return
    }
    res.setHeader('Accept-Ranges', 'bytes')
    res.setHeader('Content-Type', type)
    const range = req.headers.range
    if (range) {
      const match = /^bytes=(\d*)-(\d*)$/.exec(range)
      if (match) {
        const start = match[1] === '' ? 0 : Number(match[1])
        const end = match[2] === '' ? stat.size - 1 : Number(match[2])
        if (start <= end && end < stat.size) {
          res.writeHead(206, {
            'Content-Range': `bytes ${start}-${end}/${stat.size}`,
            'Content-Length': end - start + 1,
          })
          fs.createReadStream(file, { start, end }).pipe(res)
          return
        }
      }
      res.writeHead(416, { 'Content-Range': `bytes */${stat.size}` })
      res.end()
      return
    }
    res.writeHead(200, { 'Content-Length': stat.size })
    fs.createReadStream(file).pipe(res)
  })
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      resolve({ server, port: server.address().port })
    })
  })
}

async function main() {
  const args = parseArgs(process.argv.slice(2))
  if (!args.file || !fs.existsSync(args.file)) {
    console.error(JSON.stringify({ ok: false, error: 'missing --file' }))
    process.exit(2)
  }
  const file = path.resolve(args.file)
  const { server, port } = await startServer(file)
  const browser = await chromium.launch({ headless: !args.headed })
  const page = await browser.newPage()
  const failures = []
  try {
    await page.setContent(`
      <!doctype html>
      <html><body style="margin:0;background:#000">
        <video id="v" src="http://127.0.0.1:${port}/media" muted preload="auto" playsinline
               style="width:100%;height:100vh;object-fit:contain"></video>
      </body></html>`)
    await page.waitForFunction(
      () => {
        const video = document.querySelector('#v')
        return video && video.readyState >= 1
      },
      null,
      { timeout: 15000 },
    )
    const meta = await page.evaluate(() => {
      const video = document.querySelector('#v')
      return {
        videoWidth: video.videoWidth,
        videoHeight: video.videoHeight,
        duration: video.duration,
        readyState: video.readyState,
      }
    })
    if (!(meta.videoWidth > 0 && meta.videoHeight > 0)) {
      failures.push(`decoded frame size is ${meta.videoWidth}x${meta.videoHeight}`)
    }
    if (!(meta.duration > 0)) {
      failures.push(`duration is ${meta.duration}`)
    }

    await page.evaluate(async () => {
      const video = document.querySelector('#v')
      video.currentTime = 0
      await video.play()
    })
    await page.waitForTimeout(1200)
    const played = await page.evaluate(() => document.querySelector('#v').currentTime)
    if (!(played > 0.3)) {
      failures.push(`currentTime did not advance (${played})`)
    }

    await page.evaluate(async () => {
      const video = document.querySelector('#v')
      await new Promise((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error('seek timeout')), 5000)
        video.addEventListener(
          'seeked',
          () => {
            clearTimeout(timer)
            resolve()
          },
          { once: true },
        )
        video.currentTime = Math.min(1.5, video.duration / 2)
      })
    })
    const seekTarget = await page.evaluate(() => document.querySelector('#v').currentTime)
    await page.evaluate(async () => {
      await document.querySelector('#v').play()
    })
    await page.waitForTimeout(800)
    const afterSeek = await page.evaluate(() => document.querySelector('#v').currentTime)
    if (!(afterSeek > seekTarget + 0.1)) {
      failures.push(`playback did not continue after seeking (${seekTarget} -> ${afterSeek})`)
    }

    await page.evaluate(() => document.querySelector('#v').pause())
    const pausedAt = await page.evaluate(() => document.querySelector('#v').currentTime)
    await page.waitForTimeout(500)
    const stillPaused = await page.evaluate(() => document.querySelector('#v').currentTime)
    if (Math.abs(stillPaused - pausedAt) > 0.1) {
      failures.push(`currentTime moved while paused (${pausedAt} -> ${stillPaused})`)
    }

    const result = {
      ok: failures.length === 0,
      file,
      videoWidth: meta.videoWidth,
      videoHeight: meta.videoHeight,
      duration: Number(meta.duration.toFixed(2)),
      playedTo: Number(played.toFixed(2)),
      seekTarget: Number(seekTarget.toFixed(2)),
      afterSeek: Number(afterSeek.toFixed(2)),
      pausedAt: Number(pausedAt.toFixed(2)),
      failures,
    }
    console.log(JSON.stringify(result, null, 2))
    process.exitCode = failures.length === 0 ? 0 : 1
  } catch (error) {
    console.error(JSON.stringify({ ok: false, error: String(error), failures }))
    process.exitCode = 1
  } finally {
    await browser.close()
    server.close()
  }
}

await main()
