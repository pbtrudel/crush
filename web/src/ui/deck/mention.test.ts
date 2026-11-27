import { describe, expect, it } from 'vitest';
import { activeMention, applyMention } from './mention';

describe('mentions', () => {
  it('finds an @ token at the caret', () => {
    expect(activeMention('look at @inter', 14)).toEqual({ start: 8, query: 'inter' });
    expect(activeMention('email a@b.c', 11)).toBeNull();
    expect(activeMention('done @x ', 8)).toBeNull();
  });
  it('replaces the token with the path', () => {
    expect(applyMention('look at @inter now', 8, 14, 'internal/web/api.go')).toEqual({
      text: 'look at @internal/web/api.go now',
      caret: 29,
    });
  });
});
