// Auto-rewind on resume: after a pause, back up a little so the listener hears
// the tail of what they last heard instead of landing mid-sentence. The longer
// the pause, the more context is needed. Pure, so it can be reasoned about (and
// tested) without an audio element.
//
// Thresholds (pause duration -> rewind):
//   < 10 s   -> 0      (a mis-tap or a quick interruption; do not move)
//   < 2 min  -> 5 s
//   < 10 min -> 10 s
//   >= 10 min-> 15 s
export function autoRewindMs(pausedForMs: number): number {
	if (!(pausedForMs > 0)) return 0;
	if (pausedForMs < 10_000) return 0;
	if (pausedForMs < 120_000) return 5_000;
	if (pausedForMs < 600_000) return 10_000;
	return 15_000;
}
