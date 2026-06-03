<script lang="ts">
	import { CameraWidget, AudioInputWidget, providePip } from '@viamrobotics/test-widgets';
	import { PART_ID, resources } from '$lib/viam';

	providePip(() => PART_ID)
</script>

<main class="mx-auto max-w-4xl space-y-8 p-6 font-public-sans">
	<header>
		<h1 class="text-2xl font-bold text-heading">camera-360 playground</h1>
		<p class="text-subtle-1">
			Test widgets wired to the companion <code>viam-server</code> (part
			<code>{PART_ID}</code>).
		</p>
	</header>

	{#each resources as resource (resource.name)}
		<section class="space-y-2">
			<h2 class="text-lg font-semibold text-heading">{resource.label}</h2>
			{#if resource.kind === 'camera'}
				<CameraWidget partID={PART_ID} resourceName={resource.name} />
			{:else if resource.kind === 'audio_in'}
				<AudioInputWidget partID={PART_ID} resourceName={resource.name} />
			{/if}
		</section>
	{/each}
</main>
