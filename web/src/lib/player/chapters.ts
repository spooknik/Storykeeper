// Chapter helpers shared by the book page and the in-player chapter sheet.

import type { Chapter } from '$lib/api/types';

// Entries like "Disc 3", "Part 12", "Track 7" or "07" are file boundaries the
// scanner synthesised, not real chapters. Label them honestly.
const GENERIC_TITLE = /^(?:disc|disk|cd|part|track|chapter|file)?\s*\d+(?:\s*(?:of|\/)\s*\d+)?$/i;

/** True when every chapter title is a synthesised file/part marker. */
export function chaptersAreParts(chapters: Chapter[]): boolean {
	return chapters.length > 0 && chapters.every((c) => GENERIC_TITLE.test(c.title.trim()));
}

/** "Parts" or "Chapters", for headings and sheet titles. */
export function chapterLabel(chapters: Chapter[]): string {
	return chaptersAreParts(chapters) ? 'Parts' : 'Chapters';
}
