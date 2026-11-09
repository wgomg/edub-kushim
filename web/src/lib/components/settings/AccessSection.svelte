<script>
	import { api } from '#lib/api.js';
	import Icon from '#lib/components/Icon.svelte';
	import ListTable from '#lib/components/ListTable.svelte';
	import Modal from '#lib/components/Modal.svelte';
	import { createListQuery, toggleSortState } from '#lib/list/listQuery.svelte.js';
	import { formatDate } from '#lib/utils/html.js';
	import { confirmStore } from '#lib/stores/confirmStore.svelte.js';
	import { toastStore } from '#lib/stores/toastStore.svelte.js';
	import * as authStore from '#lib/stores/authStore.svelte.js';
	import Card from './Card.svelte';
	import GroupHead from './GroupHead.svelte';
	import Switch from './Switch.svelte';
	import { usersQuery } from './usersQuery.js';

	let { cfg, showHead = true } = $props();

	let showUserModal = $state(false);
	let editingUser = $state(null);
	let formUsername = $state('');
	let formPassword = $state('');
	let formRole = $state('viewer');
	let userError = $state('');
	let savingUser = $state(false);

	async function fetchUsers({ limit, offset, signal }) {
		const data = await api.users.list(limit, offset, signal);
		if (!data) throw new Error('Failed to load users');
		return { items: data.users ?? [], total: data.total ?? null };
	}

	const users = createListQuery({
		fetcher: fetchUsers,
		codec: usersQuery,
		sortKeys: ['username', 'role', 'created_at'],
		keyField: 'id'
	});

	const userColumns = [
		{ key: 'username', label: 'Username', sortable: true, width: '100%' },
		{ key: 'role', label: 'Role', sortable: true },
		{ key: 'created_at', label: 'Created At', sortable: true },
		{ key: 'actions', label: 'Actions', sortable: false, cellClass: 'whitespace-nowrap' }
	];

	function sortByColumn(key) {
		users.update(toggleSortState(users.query, key));
	}

	function openNewUser() {
		editingUser = null;
		formUsername = '';
		formPassword = '';
		formRole = 'viewer';
		userError = '';
		showUserModal = true;
	}

	function openEditUser(userId, userName, userRole) {
		editingUser = { id: userId, username: userName };
		formUsername = userName;
		formPassword = '';
		formRole = userRole || 'viewer';
		userError = '';
		showUserModal = true;
	}

	async function saveUser() {
		userError = '';
		const username = formUsername.trim();
		if (!username) {
			userError = 'Username is required';
			return;
		}
		const password = formPassword.trim();

		savingUser = true;
		let result;
		if (editingUser) {
			const body = { username, role: formRole };
			if (password) body.password = password;
			result = await api.users.update(editingUser.id, body);
		} else {
			if (!password) {
				savingUser = false;
				userError = 'Password is required';
				return;
			}
			result = await api.users.create({ username, password, role: formRole });
		}
		savingUser = false;

		if (result.ok) {
			showUserModal = false;
			users.reload();
		} else if (result.status === 409) {
			userError = 'Username already exists';
		} else {
			toastStore.error('Failed to save user. Check the server connection and try again.');
		}
	}

	async function handleDeleteUser(userId, userName) {
		const ok = await confirmStore.confirm({
			title: 'Delete user',
			message: `Delete user "${userName}"? This action cannot be undone.`,
			danger: true
		});
		if (!ok) return;
		await api.users.delete(userId);
		users.reload();
	}
</script>

<section id="access" class="group scroll-mt-6 pb-12">
	{#if showHead}
		<GroupHead
			eyebrow="Operations"
			variant="op"
			title="Access"
			description="Who can sign in and what they can do."
		/>
	{/if}

	<div class="space-y-4">
		<Card
			title="Sign-in"
			name="sign in authentication login auth"
			description="Authentication for the whole library."
		>
			<Switch
				id="server-auth-enabled"
				bind:checked={cfg.server.auth_enabled}
				label="Require sign-in"
				description="When off, anyone who can reach the server can read and manage the library."
			/>
		</Card>

		<Card
			title="Users"
			name="users accounts roles admin editor viewer password"
			description="Accounts and roles. Changes apply immediately."
		>
			{#snippet actions()}
				<button
					onclick={openNewUser}
					class="rounded-lg bg-gold-500 px-4 py-2 text-sm font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
				>
					Create user
				</button>
			{/snippet}

			{#snippet cell(col, row)}
				{#if col.key === 'role'}
					<span
						class="inline-block rounded-full px-2 py-0.5 text-xs font-medium {authStore.roleBadgeClass(
							row.role
						)}">{row.role}</span
					>
				{:else if col.key === 'created_at'}
					{formatDate(row.created_at)}
				{:else if col.key === 'actions'}
					<div class="flex items-center">
						<button
							onclick={() => openEditUser(row.id, row.username, row.role || 'viewer')}
							title="Edit"
							aria-label="Edit"
							class="rounded-md p-1.5 text-parchment-400 hover:bg-clay-800 hover:text-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							><Icon name="edit" /></button
						>
						<button
							onclick={() => handleDeleteUser(row.id, row.username)}
							title="Delete"
							aria-label="Delete"
							class="rounded-md p-1.5 text-parchment-400 hover:bg-clay-800 hover:text-terracotta-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
							><Icon name="delete" /></button
						>
					</div>
				{:else}
					{row[col.key] ?? ''}
				{/if}
			{/snippet}

			<ListTable
				columns={userColumns}
				{cell}
				rows={users.rows}
				total={users.total}
				loading={users.loading}
				error={users.error}
				page={users.query.page}
				pageSize={users.query.pageSize}
				pageSizes={[10, 25, 50, 100]}
				sortBy={users.query.sortBy}
				sortOrder={users.query.sortOrder}
				keyField="id"
				title=""
				onSort={sortByColumn}
				onPage={(next) => users.update({ page: next })}
				onPageSize={(size) => users.update({ pageSize: size })}
			/>
		</Card>
	</div>

	<Modal
		open={showUserModal}
		title={editingUser ? 'Edit User' : 'Create User'}
		onClose={() => (showUserModal = false)}
	>
		<form
			onsubmit={(e) => {
				e.preventDefault();
				saveUser();
			}}
		>
			<div class="space-y-4">
				<div>
					<label for="user-username" class="mb-1 block text-xs font-medium text-parchment-400"
						>Username</label
					>
					<input
						id="user-username"
						name="user-username"
						autocomplete="off"
						spellcheck="false"
						type="text"
						bind:value={formUsername}
						placeholder="Username"
						class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
				<div>
					<label for="user-role" class="mb-1 block text-xs font-medium text-parchment-400"
						>Role</label
					>
					<select
						id="user-role"
						name="user-role"
						bind:value={formRole}
						class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						<option value="viewer">Viewer</option>
						<option value="editor">Editor</option>
						<option value="admin">Admin</option>
					</select>
				</div>
				<div>
					<label for="user-password" class="mb-1 block text-xs font-medium text-parchment-400"
						>Password</label
					>
					<input
						id="user-password"
						name="user-password"
						autocomplete={editingUser ? 'current-password' : 'new-password'}
						type="password"
						bind:value={formPassword}
						placeholder={editingUser ? 'Leave blank to keep current' : 'Password'}
						class="w-full rounded-md border border-clay-700 bg-clay-900 px-3 py-1.5 text-sm text-parchment-200 placeholder-parchment-600 focus:border-gold-500 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					/>
				</div>
				{#if userError}
					<p class="text-sm text-terracotta-500" aria-live="polite">{userError}</p>
				{/if}
				<div class="flex justify-end gap-2">
					<button
						type="button"
						onclick={() => (showUserModal = false)}
						class="rounded-md px-3 py-1.5 text-xs font-medium text-parchment-400 hover:bg-clay-800 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none"
					>
						Cancel
					</button>
					<button
						type="submit"
						disabled={savingUser}
						class="rounded-md bg-gold-500 px-3 py-1.5 text-xs font-medium text-clay-950 hover:bg-gold-600 focus-visible:ring-2 focus-visible:ring-gold-500 focus-visible:outline-none disabled:opacity-50"
					>
						{savingUser ? 'Saving…' : editingUser ? 'Save' : 'Create'}
					</button>
				</div>
			</div>
		</form>
	</Modal>
</section>
