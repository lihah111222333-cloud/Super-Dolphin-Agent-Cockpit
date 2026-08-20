<script>
	export let start = 0;
	let count = start;

	function increment() {
		count += 1;
	}
</script>

<button type="button" on:click={increment} aria-label="Increment">
	{count}
</button>
