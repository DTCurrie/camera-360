<script lang="ts">
	import '../app.css';
	import '@viamrobotics/tailwind-config/fonts';
	import favicon from '$lib/assets/favicon.svg';
	import { ViamProvider } from '@viamrobotics/svelte-sdk';
	import { dialConfigs } from '$lib/viam';

	const { children } = $props();

</script>

<svelte:head>
	<link rel="icon" href={favicon} />
</svelte:head>

<ViamProvider {dialConfigs}>
	{@render children()}
</ViamProvider>
