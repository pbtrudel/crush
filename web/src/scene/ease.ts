// expoOut starts fast and settles slowly. The raw curve ends at 0.99902,
// so the end points are pinned to the exact values the boot and flight
// steps need.
export function expoOut(t: number): number {
  if (t <= 0) return 0;
  if (t >= 1) return 1;
  return 1 - 2 ** (-10 * t);
}
