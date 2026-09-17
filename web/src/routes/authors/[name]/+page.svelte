<script lang="ts">
	import { page } from '$app/state';
	import FacetBooks from '$lib/library/FacetBooks.svelte';
	import { decodeFacetName } from '$lib/library/group';

	const name = $derived(decodeFacetName(page.params.name));
</script>

<FacetBooks kind="author" {name} />
