import { describe, expect, it } from 'vitest'

import { arrayField, boolField, compactInput, firstLine, numberField, parseInput, stringField } from './input'

describe('parseInput', () => {
  it('reads a JSON object and nothing else', () => {
    expect(parseInput('{"command":"ls"}')).toEqual({ command: 'ls' })
    expect(parseInput('')).toBeNull()
    expect(parseInput('[1]')).toBeNull()
    // A call still streaming its input has half a JSON document.
    expect(parseInput('{"command":"l')).toBeNull()
  })

  it('reads typed fields with safe defaults', () => {
    const input = parseInput('{"a":"x","n":2,"b":true,"l":[1]}')
    expect(stringField(input, 'a')).toBe('x')
    expect(stringField(input, 'n')).toBe('')
    expect(numberField(input, 'n')).toBe(2)
    expect(numberField(null, 'n')).toBeNull()
    expect(boolField(input, 'b')).toBe(true)
    expect(arrayField(input, 'l')).toEqual([1])
    expect(arrayField(input, 'a')).toEqual([])
  })
})

describe('firstLine and compactInput', () => {
  it('keeps one trimmed line, clipped', () => {
    expect(firstLine('  go test\n./...')).toBe('go test')
    expect(firstLine('x'.repeat(100), 10)).toBe('xxxxxxxxx…')
  })

  it('summarises an input by its first string field', () => {
    expect(compactInput({ query: 'parser bug', limit: 3 })).toBe('parser bug')
    expect(compactInput({ limit: 3 })).toBe('{"limit":3}')
    expect(compactInput(null)).toBe('')
  })
})
