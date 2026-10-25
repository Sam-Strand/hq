// Мост для кросс-проверки Python ↔ JS.
//
//   node js_bridge.mjs /path/to/mkon-js < requests.json > responses.json
//
// requests.json — массив операций:
//   { "op": "parse",     "text": "...", "rt": false }
//   { "op": "stringify", "value": <tagged>, "options": {...}, "rt": false }
//   { "op": "format",    "text": "...", "options": {...}, "rt": false }   // stringify(parse(text))
// Ответ — массив { "ok": true, "result": ... } | { "ok": false, "error": "..." }.
//
// Значения передаются в «tagged JSON», чтобы пережить JSON:
//   NaN / Infinity / -Infinity / -0  →  { "$number": "NaN" } и т.д.
//   Date                             →  { "$date": "2024-01-15T00:00:00.000Z" }
import fs from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const root = process.argv[2]
if (!root) throw new Error('usage: node js_bridge.mjs /path/to/mkon-js')
const { default: mkon } = await import(pathToFileURL(path.join(root, 'src', 'index.js')).href)

export function encode(v) {
    if (typeof v === 'number') {
        if (Number.isNaN(v)) return { $number: 'NaN' }
        if (v === Infinity) return { $number: 'Infinity' }
        if (v === -Infinity) return { $number: '-Infinity' }
        if (v === 0 && 1 / v < 0) return { $number: '-0' }
        return v
    }
    if (v instanceof Date) return { $date: Number.isNaN(v.getTime()) ? 'Invalid Date' : v.toISOString() }
    if (Array.isArray(v)) return v.map(encode)
    if (v && typeof v === 'object') {
        const o = {}
        for (const k of Object.keys(v)) o[k] = encode(v[k])
        return o
    }
    return v
}

export function decode(v) {
    if (Array.isArray(v)) return v.map(decode)
    if (v && typeof v === 'object') {
        const keys = Object.keys(v)
        if (keys.length === 1 && keys[0] === '$number') return Number(v.$number)
        if (keys.length === 1 && keys[0] === '$date') return new Date(v.$date)
        const o = {}
        for (const k of keys) o[k] = decode(v[k])
        return o
    }
    return v
}

const input = JSON.parse(fs.readFileSync(0, 'utf8'))
const out = input.map((req) => {
    try {
        mkon.setEndOfLine('\n')
        const p = req.rt ? mkon.rt.parse : mkon.parse
        const s = req.rt ? mkon.rt.stringify : mkon.stringify
        switch (req.op) {
            case 'parse': return { ok: true, result: encode(p(req.text)) }
            case 'stringify': return { ok: true, result: s(decode(req.value), { ...(req.options || {}) }) }
            case 'format': return { ok: true, result: s(p(req.text), { ...(req.options || {}) }) }
            default: throw new Error('unknown op ' + req.op)
        }
    } catch (e) {
        return { ok: false, error: String(e && e.message || e) }
    }
})
process.stdout.write(JSON.stringify(out))
