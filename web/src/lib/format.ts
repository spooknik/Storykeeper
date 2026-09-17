/** 3725000 → "1:02:05"; 65000 → "1:05". */
export function fmtTime(ms: number): string {
	const total = Math.max(0, Math.floor(ms / 1000));
	const h = Math.floor(total / 3600);
	const m = Math.floor((total % 3600) / 60);
	const s = total % 60;
	const mm = h > 0 ? String(m).padStart(2, '0') : String(m);
	return (h > 0 ? `${h}:` : '') + `${mm}:${String(s).padStart(2, '0')}`;
}

/** 3725000 → "1h 2m"; 65000 → "1m". */
export function fmtDuration(ms: number): string {
	const total = Math.max(0, Math.round(ms / 60000));
	const h = Math.floor(total / 60);
	const m = total % 60;
	if (h === 0) return `${m}m`;
	return m === 0 ? `${h}h` : `${h}h ${m}m`;
}

export function joinNames(names: string[]): string {
	return names.filter(Boolean).join(', ');
}
