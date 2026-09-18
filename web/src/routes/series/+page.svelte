<script lang="ts">
	import type { SeriesFacet } from '$lib/api/types';
	import FacetIndex from '$lib/library/FacetIndex.svelte';
</script>

<FacetIndex title="Series" endpoint="/api/v1/series" base="series" icon="layers" noun="series">
	{#snippet detail(f: SeriesFacet)}
		{f.finished_count} of {f.book_count} finished{#if f.in_progress_count > 0}
			· {f.in_progress_count} in progress{/if}
	{/snippet}
</FacetIndex>
