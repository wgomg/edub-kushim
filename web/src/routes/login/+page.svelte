<script>
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { api } from '$lib/api.js';
	import * as authStore from '$lib/stores/authStore.js';

	let username = $state('');
	let password = $state('');
	let error = $state('');
	let loading = $state(false);

	async function handleSubmit(e) {
		e.preventDefault();
		error = '';
		if (!username.trim() || !password) {
			error = 'Username and password are required';
			return;
		}
		loading = true;
		try {
			const res = await api.auth.login(username.trim(), password);
			if (res.ok && res.data && res.data.token) {
				authStore.login(res.data.token, res.data.user);
				goto(resolve('/'));
			} else if (res.status === 401) {
				authStore.logout();
				error = 'Invalid username or password. Please check your credentials and try again.';
			} else {
				authStore.logout();
				error = 'Login failed. Please try again.';
			}
		} catch {
			error = 'An error occurred. Please try again.';
		} finally {
			loading = false;
		}
	}
</script>

<div class="w-full max-w-sm rounded-xl border border-clay-800 bg-clay-900 p-8 shadow-xl">
	<div class="mb-8 text-center">
		<h1 class="text-2xl font-bold tracking-tight text-balance text-parchment-200" translate="no">
			edub-kushim
		</h1>
		<p class="mt-1 text-sm text-parchment-500">Sign In to Your Account</p>
	</div>

	<form onsubmit={handleSubmit}>
		<div class="space-y-4">
			<div>
				<label for="login-username" class="mb-1 block text-sm font-medium text-parchment-200"
					>Username</label
				>
				<input
					id="login-username"
					name="username"
					type="text"
					bind:value={username}
					placeholder="Username"
					class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
					autocomplete="username"
					spellcheck={false}
				/>
			</div>
			<div>
				<label for="login-password" class="mb-1 block text-sm font-medium text-parchment-200"
					>Password</label
				>
				<input
					id="login-password"
					name="password"
					type="password"
					bind:value={password}
					placeholder="Password"
					class="w-full rounded-lg border border-clay-800 bg-clay-950 px-3 py-2 text-sm text-parchment-200 placeholder-parchment-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-gold-500"
					autocomplete="current-password"
				/>
			</div>
		</div>

		{#if error}
			<p class="mt-3 text-sm text-terracotta-500" aria-live="polite">{error}</p>
		{/if}

		<button
			type="submit"
			disabled={loading}
			class="mt-6 w-full rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 disabled:opacity-50"
		>
			{loading ? 'Signing In…' : 'Sign In'}
			{#if loading}
				<div
					class="ml-2 inline-block h-4 w-4 animate-spin rounded-full border-2 border-clay-950 border-t-gold-500"
				></div>
			{/if}
		</button>
	</form>
</div>
