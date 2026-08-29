import { describe, it, expect, beforeEach } from 'vitest';
import {
	init,
	login,
	logout,
	getToken,
	getUser,
	isAuthenticated,
	isAdmin,
	isEditor,
	roleBadgeClass
} from './authStore.js';

const ADMIN = { username: 'admin', role: 'admin' };
const EDITOR = { username: 'editor', role: 'editor' };
const VIEWER = { username: 'viewer', role: 'viewer' };

describe('authStore', () => {
	beforeEach(() => {
		localStorage.clear();
		logout();
	});

	it('init restores token and user from localStorage', () => {
		localStorage.setItem('token', 'tok-1');
		localStorage.setItem('user', JSON.stringify(ADMIN));
		init();
		expect(getToken()).toBe('tok-1');
		expect(getUser()).toEqual(ADMIN);
		expect(isAuthenticated()).toBe(true);
	});

	it('init tolerates a corrupt user blob', () => {
		localStorage.setItem('token', 'tok-1');
		localStorage.setItem('user', '{not json');
		init();
		expect(getUser()).toBeNull();
		expect(isAuthenticated()).toBe(false);
	});

	it('login persists credentials and logout clears them', () => {
		login('tok-2', EDITOR);
		expect(getToken()).toBe('tok-2');
		expect(getUser()).toEqual(EDITOR);
		expect(localStorage.getItem('token')).toBe('tok-2');
		expect(JSON.parse(localStorage.getItem('user'))).toEqual(EDITOR);

		logout();
		expect(getToken()).toBe('');
		expect(getUser()).toBeNull();
		expect(localStorage.getItem('token')).toBeNull();
		expect(localStorage.getItem('user')).toBeNull();
		expect(isAuthenticated()).toBe(false);
	});

	it('isAdmin and isEditor follow the role hierarchy', () => {
		login('t', ADMIN);
		expect(isAdmin()).toBe(true);
		expect(isEditor()).toBe(true);

		login('t', EDITOR);
		expect(isAdmin()).toBe(false);
		expect(isEditor()).toBe(true);

		login('t', VIEWER);
		expect(isAdmin()).toBe(false);
		expect(isEditor()).toBe(false);
	});

	it('roleBadgeClass maps roles and falls back to viewer styling', () => {
		expect(roleBadgeClass('admin')).toBe('bg-lapis-500/20 text-lapis-400');
		expect(roleBadgeClass('editor')).toBe('bg-gold-500/20 text-gold-400');
		expect(roleBadgeClass('viewer')).toBe('bg-clay-700/50 text-parchment-400');
		expect(roleBadgeClass('unknown')).toBe('bg-clay-700/50 text-parchment-400');
	});
});
