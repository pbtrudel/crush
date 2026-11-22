import { digits } from './series';

const TENS = ['0', '1', '2', '3', '4', '5', '6', '7', '8', '9'];

// Numeral rolls each digit through a column of zero to nine, so a changed
// value slides instead of jumping. The readable text is visually hidden and
// the rolling row is decorative.
export function Numeral({ text, label }: { text: string; label: string }) {
  return (
    <span class="numeral">
      <span class="visually-hidden">{label} {text}</span>
      <span class="numeral__row" aria-hidden="true">
        {digits(text).map((c, i) => (
          c >= '0' && c <= '9'
            ? <span key={i} class="roll" data-d={c}><span class="roll__strip">{TENS.map((d) => <span key={d}>{d}</span>)}</span></span>
            : <span key={i} class="numeral__char">{c}</span>
        ))}
      </span>
    </span>
  );
}
