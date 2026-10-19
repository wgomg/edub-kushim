<script>
	import { api } from '$lib/api';
	import DataTable from '$lib/components/DataTable.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import { EDIT_ICON, DELETE_ICON, actionButton } from '$lib/icons.js';
	import { escapeHtml } from '$lib/utils/html.js';
	import { confirmStore } from '$lib/stores/confirmStore.svelte.js';
	import { toastStore } from '$lib/stores/toastStore.svelte.js';
	import * as authStore from '$lib/stores/authStore.js';
	import Card from './Card.svelte';
	import GroupHead from './GroupHead.svelte';
	import Switch from './Switch.svelte';

	let { cfg, showHead = true } = $props();

	let showUserModal = $state(false);
	let editingUser = $state(null);
	let formUsername = $state('');
	let formPassword = $state('');
	let formRole = $state('viewer');
	let userError = $state('');
	let savingUser = $state(false);
	let refreshKey = $state(0);

	const userColumns = [
		{
			key: 'username',
			label: 'Username',
			sortable: true,
			width: '100%'
		},
		{
			key: 'role',
			label: 'Role',
			sortable: true,
			cell: (_v, row) => {
				const cls = authStore.roleBadgeClass(row.role);
				return `<span class="inline-block rounded-full px-2 py-0.5 text-xs font-medium ${cls}">${escapeHtml(row.role)}</span>`;
			}
		},
		{
			key: 'created_at',
			label: 'Created At',
			sortable: true,
			cell: (_v, row) => {
				if (!row.created_at) return '-';
				const d = new Date(row.created_at);
				return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
			}
		},
		{
			key: 'actions',
			label: 'Actions',
			sortable: false,
			cellClass: 'whitespace-nowrap',
			cell: (_v, row) => {
				const safeName = escapeHtml(row.username);
				const safeRole = escapeHtml(row.role || 'viewer');
				return `${actionButton(EDIT_ICON, 'Edit', 'text-parchment-400 hover:text-gold-500', { 'data-edit-user': row.id, 'data-user-name': safeName, 'data-user-role': safeRole })}
${actionButton(DELETE_ICON, 'Delete', 'text-parchment-400 hover:text-terracotta-500', { 'data-delete-user': row.id, 'data-user-name': safeName })}`;
			}
		}
	];

	async function fetchUsers({ limit, offset }) {
		return await api.users.list(limit, offset);
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

	function handleUserPageClick(e) {
		const editBtn = e.target.closest('[data-edit-user]');
		if (editBtn) {
			const id = parseInt(editBtn.getAttribute('data-edit-user'));
			const name = editBtn.getAttribute('data-user-name');
			const role = editBtn.getAttribute('data-user-role') || 'viewer';
			openEditUser(id, name, role);
			return;
		}
		const deleteBtn = e.target.closest('[data-delete-user]');
		if (deleteBtn) {
			const id = parseInt(deleteBtn.getAttribute('data-delete-user'));
			const name = deleteBtn.getAttribute('data-user-name');
			handleDeleteUser(id, name);
		}
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
			refreshKey++;
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
		refreshKey++;
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

			<DataTable
				columns={userColumns}
				fetch={fetchUsers}
				title=""
				defaultPageSize={25}
				pageSizes={[10, 25, 50, 100]}
				{refreshKey}
				onActionClick={handleUserPageClick}
				urlSync="users"
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
